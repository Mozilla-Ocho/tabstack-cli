package client

import (
	"net/http"
	"strconv"
	"time"
)

// DebugInfo is what a single API round trip reveals when --debug is on: the
// request line, the response status, how long the server took to send headers,
// and the trace and rate-limit headers the API returns. It carries no bodies
// and no credentials.
type DebugInfo struct {
	Method  string
	URL     string
	Status  int           // 0 when the round trip failed before a response
	Elapsed time.Duration // request sent -> response headers received
	TraceID string        // x-trace-id, the id to quote in a support request

	RateLimitLimit     string    // x-ratelimit-limit
	RateLimitRemaining string    // x-ratelimit-remaining
	RateLimitReset     time.Time // x-ratelimit-reset, parsed from unix seconds; zero if absent
}

// WithDebug wraps the client's transport so each round trip is timed and its
// trace/rate-limit headers are reported to sink. A nil sink is a no-op.
//
// It measures to response headers, not to end of body: for a streaming endpoint
// that is time-to-first-byte (the stream then continues), and for a JSON
// endpoint it is the server's latency without the body download. Pass it after
// WithHTTPClient so it wraps whatever transport that installed.
func WithDebug(sink func(DebugInfo)) Option {
	return func(c *Client) {
		if sink == nil {
			return
		}
		base := c.http.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		c.http.Transport = &debugTransport{base: base, sink: sink, now: time.Now}
	}
}

// debugTransport times a round trip and reports its headers, then passes the
// response through untouched.
type debugTransport struct {
	base http.RoundTripper
	sink func(DebugInfo)
	now  func() time.Time
}

func (t *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := t.now()
	resp, err := t.base.RoundTrip(req)
	info := DebugInfo{
		Method:  req.Method,
		URL:     req.URL.String(),
		Elapsed: t.now().Sub(start),
	}
	if resp != nil {
		info.Status = resp.StatusCode
		info.TraceID = resp.Header.Get("X-Trace-Id")
		info.RateLimitLimit = resp.Header.Get("X-RateLimit-Limit")
		info.RateLimitRemaining = resp.Header.Get("X-RateLimit-Remaining")
		if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
			if secs, perr := strconv.ParseInt(reset, 10, 64); perr == nil {
				info.RateLimitReset = time.Unix(secs, 0)
			}
		}
	}
	t.sink(info)
	return resp, err
}
