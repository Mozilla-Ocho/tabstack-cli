package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client is a thin wrapper around http.Client that knows how to talk to the
// Tabstack API: it attaches the bearer token, points at the right base URL,
// and centralises request building and error decoding. There is deliberately
// no generated code here, we map each endpoint by hand so the streaming
// behaviour stays under our control.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
	timeout time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient swaps the underlying http.Client. Mostly useful for tests.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithTimeout sets the per-request timeout for non-streaming calls. It is
// deliberately not an http.Client.Timeout: that covers reading the response
// body too, so it would cut a long-lived SSE stream off mid-flight. Instead the
// duration is stored here and doJSON alone applies it to its request context,
// leaving doStream genuinely untimed. Keeping it off the http.Client also means
// this composes with WithHTTPClient and WithDebug in any order.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.timeout = d }
}

// New constructs a Client. baseURL should not carry a trailing slash; we
// normalise it anyway to keep path joining predictable.
func New(apiKey, baseURL string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError is a decoded non-2xx response. The Tabstack endpoints return a flat
// {"error": "..."} body, which we surface alongside the status code.
type APIError struct {
	StatusCode int
	Message    string

	// TraceID is the x-trace-id response header, the id to quote in a support
	// request. It was previously only reachable under --debug, which is exactly
	// backwards: a failure is when you want it, and --debug is what you have to
	// have known to pass beforehand.
	TraceID string

	// RetryAfter is the Retry-After header verbatim, kept as sent rather than
	// parsed because the header may be either a seconds count or an HTTP date.
	RetryAfter string
}

func (e *APIError) Error() string {
	var b strings.Builder

	// Lead with a plain word. fang title-cases the first word of a rendered
	// error, so a message starting "api" reached users as "Api error (401)".
	// Keeping the literal "api error (NNN):" substring intact means anything
	// grepping stderr for it matches, which it did not while fang mangled it.
	b.WriteString("request failed: ")
	if e.Message != "" {
		fmt.Fprintf(&b, "api error (%d): %s", e.StatusCode, e.Message)
	} else {
		fmt.Fprintf(&b, "api error: status %d", e.StatusCode)
	}
	if g := e.guidance(); g != "" {
		b.WriteString(". ")
		b.WriteString(g)
	}
	if e.TraceID != "" {
		fmt.Fprintf(&b, " (trace id %s)", e.TraceID)
	}
	return b.String()
}

// guidance turns the status code into the next thing to try. Only the three
// statuses with a specific, actionable cause get one; a generic "check your
// request" on everything else would be noise. Kept out of Error's happy path so
// the wording lives in one readable place.
func (e *APIError) guidance() string {
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return "The key may be revoked or expired: run `tabstack auth login`, or check `tabstack auth status`"
	case http.StatusForbidden:
		return "The key may belong to a different organisation: check `tabstack auth status`, and --org if you passed it"
	case http.StatusTooManyRequests:
		if e.RetryAfter != "" {
			return "Rate limited: retry after " + humanRetryAfter(e.RetryAfter)
		}
		return "Rate limited: retry with backoff, or reduce concurrency"
	}
	return ""
}

// humanRetryAfter renders a Retry-After value for display. The header is either
// a seconds count or an HTTP date; a bare number reads better with a unit, and
// anything else is passed through untouched rather than guessed at.
func humanRetryAfter(v string) string {
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return (time.Duration(secs) * time.Second).String()
	}
	return v
}

// newRequest builds a POST request to path with body marshalled as JSON.
func (c *Client) newRequest(ctx context.Context, path string, body any) (*http.Request, error) {
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		buf = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// doJSON sends a request expecting a single JSON response, decoding it into out.
// It is used by the non-streaming endpoints (extract, generate, input), and is
// the only place the configured timeout applies: see WithTimeout.
func (c *Client) doJSON(ctx context.Context, path string, body, out any) error {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	req, err := c.newRequest(ctx, path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp)
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// doStream sends a request expecting an SSE stream, invoking fn for each event.
// It never applies the configured timeout, cancellation flows through ctx.
func (c *Client) doStream(ctx context.Context, path string, body any, fn func(Event) error) error {
	req, err := c.newRequest(ctx, path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp)
	}

	return ParseSSE(resp.Body, fn)
}

// decodeError reads an error response body and turns it into an *APIError. We
// read the whole body first so a malformed JSON error still yields something
// useful in the message.
func decodeError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)

	var payload struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &payload) == nil && payload.Error != "" {
		msg = payload.Error
	}
	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    msg,
		TraceID:    resp.Header.Get("X-Trace-Id"),
		RetryAfter: resp.Header.Get("Retry-After"),
	}
}
