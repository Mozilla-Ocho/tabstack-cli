package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	retry   retryPolicy

	deprecation *deprecationNotifier
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient swaps the underlying http.Client. Mostly useful for tests.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithTimeout sets a request timeout on the default http.Client. Note that for
// streaming endpoints a hard timeout will cut the stream off, so the streaming
// methods build their own request context rather than relying on this.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.http.Timeout = d }
}

// New constructs a Client. baseURL should not carry a trailing slash; we
// normalise it anyway to keep path joining predictable.
func New(apiKey, baseURL string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{},
		retry: retryPolicy{
			maxRetries: DefaultMaxRetries,
			maxWait:    DefaultMaxRetryWait,
			sleep:      sleepCtx,
			now:        time.Now,
		},
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

	// RetryAfter is the wait the server asked for on a rate-limited (429) or
	// unavailable (503) response, zero when it gave none. It is set once the
	// client's own retries are spent or the wait exceeded what it will block on.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	var msg string
	if e.Message != "" {
		msg = fmt.Sprintf("api error (%d): %s", e.StatusCode, e.Message)
	} else {
		msg = fmt.Sprintf("api error: status %d", e.StatusCode)
	}
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter.Round(time.Second))
	}
	return msg
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
// It is used by the non-streaming endpoints (extract, generate, input).
func (c *Client) doJSON(ctx context.Context, path string, body, out any) error {
	resp, err := c.send(ctx, func() (*http.Request, error) {
		req, err := c.newRequest(ctx, path, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

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
// It does not impose a client-level timeout, cancellation flows through ctx.
// Retries only cover the initial response: once the stream has started, a
// failure is reported in-band and never replayed.
func (c *Client) doStream(ctx context.Context, path string, body any, fn func(Event) error) error {
	resp, err := c.send(ctx, func() (*http.Request, error) {
		req, err := c.newRequest(ctx, path, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "text/event-stream")
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return ParseSSE(resp.Body, fn)
}

// decodeError reads an error response body and turns it into an *APIError. We
// read the whole body first so a malformed JSON error still yields something
// useful in the message.
func decodeError(resp *http.Response) *APIError {
	data, _ := io.ReadAll(resp.Body)

	var payload struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &payload) == nil && payload.Error != "" {
		msg = payload.Error
	}
	return &APIError{StatusCode: resp.StatusCode, Message: msg}
}
