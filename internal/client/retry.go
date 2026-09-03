package client

import (
	"context"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultRetries matches what the Tabstack SDKs do: two retries, so a transient
// 429 or 503 that the Python client absorbs silently does not kill a CI job.
// Without this the CLI was the least reliable way to call the API despite being
// the one aimed at CI.
const DefaultRetries = 2

const (
	// retryBaseDelay is the first backoff step; subsequent ones double it.
	retryBaseDelay = 500 * time.Millisecond

	// maxRetryDelay caps one computed backoff.
	maxRetryDelay = 8 * time.Second

	// maxRetryAfter caps a server-supplied Retry-After. The header is honoured
	// in preference to the computed backoff, so without a ceiling a hostile or
	// mistaken "Retry-After: 86400" would hang a build until the deadline.
	maxRetryAfter = 30 * time.Second
)

// RetryInfo describes one retried attempt. It is handed to the WithRetryNotify
// sink so the command layer can explain a slow run, and deliberately carries no
// ui dependency: the client package stays free of rendering concerns.
type RetryInfo struct {
	Method     string
	URL        string
	Attempt    int           // 1-based index of the attempt that just failed
	Remaining  int           // retries left after this one
	StatusCode int           // 0 when the failure was a transport error
	Err        string        // transport error text, empty for an HTTP status
	Wait       time.Duration // how long we are about to sleep
	TraceID    string
}

// retryableStatus reports whether a status is worth trying again. The set
// matches the SDKs: request timeout, conflict, rate limit, and any server
// error. Everything else, notably 400 and 404, means the request itself is
// wrong and repeating it verbatim cannot help.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, // 408
		http.StatusConflict,        // 409
		http.StatusTooManyRequests: // 429
		return true
	}
	return code >= 500 && code <= 599
}

// backoff returns the delay before attempt n (1-based), with full jitter.
//
// Jitter matters more than the exact curve: several CLI invocations started by
// the same CI job hit the same rate limit at the same moment, and a fixed
// schedule would have them all retry in lockstep and collide again.
func (c *Client) backoff(attempt int) time.Duration {
	base := c.retryBase
	if base <= 0 {
		base = retryBaseDelay
	}
	d := base << (attempt - 1)
	if d > maxRetryDelay || d <= 0 { // d <= 0 guards the shift overflowing
		d = maxRetryDelay
	}
	// Full jitter: anywhere in (0, d].
	return time.Duration(rand.Int64N(int64(d))) + 1
}

// retryDelay decides how long to wait before the next attempt. A server-sent
// Retry-After wins over the computed backoff, since the server knows when it
// will be ready, but is capped so it cannot stall a build indefinitely.
func (c *Client) retryDelay(attempt int, retryAfter string) time.Duration {
	if d, ok := parseRetryAfter(retryAfter); ok {
		if d > maxRetryAfter {
			return maxRetryAfter
		}
		if d < 0 {
			return 0
		}
		return d
	}
	return c.backoff(attempt)
}

// parseRetryAfter reads the header in either permitted form: a seconds count,
// or an HTTP date. An unparseable value is ignored rather than guessed at, and
// the caller falls back to the computed backoff.
func parseRetryAfter(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t), true
	}
	return 0, false
}

// sleepFor waits, but never past the context. Retries are bounded by the same
// deadline as the request they belong to, so `--timeout 30s` cannot be extended
// by a backoff and a hung server cannot outlive it.
func sleepFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// notifyRetry hands a retry to the sink, when one is installed.
func (c *Client) notifyRetry(info RetryInfo) {
	if c.retryNotify != nil {
		c.retryNotify(info)
	}
}
