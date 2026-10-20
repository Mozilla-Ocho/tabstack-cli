package client

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Retry defaults. A rate-limited call is retried a couple of times, but only
// when the server's requested wait is short enough that blocking a script on it
// is reasonable; anything longer fails straight away with the wait in the error
// so the caller can schedule its own retry.
const (
	DefaultMaxRetries   = 2
	DefaultMaxRetryWait = 30 * time.Second

	// fallbackBackoff is the base wait when a 429 carries no Retry-After or
	// rate-limit reset: 1s, then 2s, then 4s.
	fallbackBackoff = time.Second
)

// RetryInfo describes one retry the client is about to make, so the command
// layer can tell the user why the call is pausing.
type RetryInfo struct {
	Attempt    int           // 1-based retry number
	MaxRetries int           // retry budget for this call
	Status     int           // the status that triggered the retry
	Wait       time.Duration // how long the client will sleep before retrying
}

// retryPolicy holds the retry settings for a Client. The zero value disables
// retries; New installs the defaults.
type retryPolicy struct {
	maxRetries int
	maxWait    time.Duration
	notify     func(RetryInfo)
	sleep      func(context.Context, time.Duration) error
	now        func() time.Time
}

// WithRetry sets how many times a rate-limited request is retried and the
// longest single wait the client will honour (maxWait <= 0 means no cap).
// maxRetries <= 0 disables retries.
func WithRetry(maxRetries int, maxWait time.Duration) Option {
	return func(c *Client) {
		c.retry.maxRetries = max(maxRetries, 0)
		c.retry.maxWait = maxWait
	}
}

// WithRetryNotify registers a callback invoked before each retry sleep. A nil
// callback is a no-op.
func WithRetryNotify(fn func(RetryInfo)) Option {
	return func(c *Client) { c.retry.notify = fn }
}

// retryDelay decides whether resp is worth retrying and how long to wait first.
// attempt is the 0-based retry about to be made.
//
// Every endpoint is a POST and some (automate) have side effects, so only
// responses where the server has said the request was not processed qualify: a
// 429 always, and a 503 only when it carries Retry-After, which is the server
// explicitly inviting a retry. Other 5xx are left to the caller.
func (p retryPolicy) retryDelay(resp *http.Response, attempt int) (time.Duration, bool) {
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		if d, ok := serverWait(resp.Header, p.now()); ok {
			return d, true
		}
		return fallbackBackoff << attempt, true
	case http.StatusServiceUnavailable:
		return parseRetryAfter(resp.Header.Get("Retry-After"), p.now())
	}
	return 0, false
}

// serverWait reads the wait the server asked for: Retry-After first, then the
// rate-limit reset headers as a fallback.
func serverWait(h http.Header, now time.Time) (time.Duration, bool) {
	if d, ok := parseRetryAfter(h.Get("Retry-After"), now); ok {
		return d, true
	}
	if rl := ParseRateLimit(h, now); !rl.Reset.IsZero() {
		return max(rl.Reset.Sub(now), 0), true
	}
	return 0, false
}

// parseRetryAfter parses a Retry-After value, either delta seconds or an HTTP
// date (RFC 9110 section 10.2.3). A date in the past means "now".
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0), true
	}
	return 0, false
}

// sleepCtx waits for d or until ctx is done, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// send performs the request built by build, retrying rate-limited responses per
// the client's policy. build is called once per attempt so each try gets a
// fresh body. On a non-2xx final response it returns the decoded *APIError,
// carrying the server's requested wait when there was one. A request that
// fails before any response (network error) is never retried here.
func (c *Client) send(ctx context.Context, build func() (*http.Request, error)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		c.deprecation.check(req, resp)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		wait, retryable := c.retry.retryDelay(resp, attempt)
		if retryable && attempt < c.retry.maxRetries && (c.retry.maxWait <= 0 || wait <= c.retry.maxWait) {
			drain(resp)
			if c.retry.notify != nil {
				c.retry.notify(RetryInfo{Attempt: attempt + 1, MaxRetries: c.retry.maxRetries, Status: resp.StatusCode, Wait: wait})
			}
			if err := c.retry.sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}

		// Only report a wait the server actually asked for, never the local
		// fallback backoff, so the error never invents a retry time.
		apiErr := decodeError(resp)
		resp.Body.Close()
		if retryable {
			apiErr.RetryAfter, _ = serverWait(resp.Header, c.retry.now())
		}
		return nil, apiErr
	}
}

// drain discards and closes a response body so the connection can be reused.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
}
