package client

import (
	"net/http"
	"testing"
	"time"
)

// roundTripFunc adapts a function to an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDebugTransportReportsHeadersAndTiming(t *testing.T) {
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		h := http.Header{}
		h.Set("X-Trace-Id", "trace-abc")
		h.Set("X-RateLimit-Limit", "1000")
		h.Set("X-RateLimit-Remaining", "997")
		h.Set("X-RateLimit-Reset", "1785348780")
		return &http.Response{StatusCode: 200, Header: h, Body: http.NoBody}, nil
	})

	var got DebugInfo
	// A clock that advances 250ms across the round trip.
	times := []time.Time{
		time.Unix(1000, 0),
		time.Unix(1000, 0).Add(250 * time.Millisecond),
	}
	var i int
	tr := &debugTransport{
		base: base,
		sink: func(d DebugInfo) { got = d },
		now:  func() time.Time { ti := times[i]; i++; return ti },
	}

	req, _ := http.NewRequest(http.MethodPost, "https://api.example/v1/extract/markdown", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if got.Method != http.MethodPost || got.Status != 200 {
		t.Errorf("method/status = %s/%d", got.Method, got.Status)
	}
	if got.URL != "https://api.example/v1/extract/markdown" {
		t.Errorf("url = %q", got.URL)
	}
	if got.Elapsed != 250*time.Millisecond {
		t.Errorf("elapsed = %s, want 250ms", got.Elapsed)
	}
	if got.TraceID != "trace-abc" {
		t.Errorf("trace = %q", got.TraceID)
	}
	if got.RateLimitLimit != "1000" || got.RateLimitRemaining != "997" {
		t.Errorf("ratelimit = %s/%s", got.RateLimitRemaining, got.RateLimitLimit)
	}
	if want := time.Unix(1785348780, 0); !got.RateLimitReset.Equal(want) {
		t.Errorf("reset = %v, want %v", got.RateLimitReset, want)
	}
}

func TestDebugTransportReportsAFailedRoundTrip(t *testing.T) {
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, http.ErrHandlerTimeout
	})
	var got DebugInfo
	var called bool
	tr := &debugTransport{
		base: base,
		sink: func(d DebugInfo) { called = true; got = d },
		now:  time.Now,
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.example/v1/x", nil)
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("expected the transport error to propagate")
	}
	if !called {
		t.Fatal("sink not called on a failed round trip")
	}
	if got.Status != 0 || got.Method != http.MethodPost {
		t.Errorf("info = %+v, want status 0 and the method set", got)
	}
}

func TestWithDebugNilSinkIsNoOp(t *testing.T) {
	c := New("k", "https://api.example")
	before := c.http.Transport
	WithDebug(nil)(c)
	if c.http.Transport != before {
		t.Error("nil sink should not wrap the transport")
	}
}

func TestWithDebugWrapsTransport(t *testing.T) {
	c := New("k", "https://api.example")
	WithDebug(func(DebugInfo) {})(c)
	if _, ok := c.http.Transport.(*debugTransport); !ok {
		t.Errorf("transport = %T, want *debugTransport", c.http.Transport)
	}
}
