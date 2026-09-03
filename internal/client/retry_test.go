package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fastRetryClient is a client pointed at srv whose backoff is negligible, so
// the tests exercise the retry logic without paying real wall-clock delays.
func fastRetryClient(url string, retries int, opts ...Option) *Client {
	c := New("k", url, append([]Option{WithRetries(retries)}, opts...)...)
	c.retryBase = time.Millisecond
	return c
}

// countingServer returns a server that records how many requests it saw and
// always answers with the given status.
func countingServer(t *testing.T, status int, headers map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":"nope"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestRetryStatusPolicy is the core table: which statuses are worth repeating.
// 400 and 404 mean the request itself is wrong, so replaying it verbatim cannot
// help and would just multiply the cost of a mistake.
func TestRetryStatusPolicy(t *testing.T) {
	cases := []struct {
		status       int
		wantAttempts int32
	}{
		{http.StatusRequestTimeout, 3},      // 408
		{http.StatusConflict, 3},            // 409
		{http.StatusTooManyRequests, 3},     // 429
		{http.StatusInternalServerError, 3}, // 500
		{http.StatusBadGateway, 3},          // 502
		{http.StatusServiceUnavailable, 3},  // 503
		{http.StatusGatewayTimeout, 3},      // 504

		{http.StatusBadRequest, 1},   // 400
		{http.StatusNotFound, 1},     // 404
		{http.StatusUnauthorized, 1}, // 401
		{http.StatusForbidden, 1},    // 403
		{http.StatusUnprocessableEntity, 1},
	}

	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv, hits := countingServer(t, tc.status, nil)
			c := fastRetryClient(srv.URL, 2)

			err := c.doJSON(context.Background(), "/x", nil, nil)
			if err == nil {
				t.Fatal("expected an error")
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
				t.Errorf("err = %v, want *APIError with status %d", err, tc.status)
			}
			if got := hits.Load(); got != tc.wantAttempts {
				t.Errorf("made %d attempts, want %d", got, tc.wantAttempts)
			}
		})
	}
}

// TestRetryEventuallySucceeds is the point of the whole feature: a transient
// failure the SDKs absorb should not kill a CI job.
func TestRetryEventuallySucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := fastRetryClient(srv.URL, 2)
	var out map[string]any
	if err := c.doJSON(context.Background(), "/x", nil, &out); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if out["ok"] != true {
		t.Errorf("out = %v", out)
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("made %d attempts, want 3", got)
	}
}

// TestRetriesZeroMakesOneAttempt: --retries 0 must mean exactly one request.
func TestRetriesZeroMakesOneAttempt(t *testing.T) {
	srv, hits := countingServer(t, http.StatusServiceUnavailable, nil)
	c := fastRetryClient(srv.URL, 0)

	if err := c.doJSON(context.Background(), "/x", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("made %d attempts, want exactly 1", got)
	}
}

// TestRetryBodyIsReplayed guards the subtle one: a request built once and
// reused would send an already-drained body on the second attempt.
func TestRetryBodyIsReplayed(t *testing.T) {
	var bodies []string
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if hits.Add(1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := fastRetryClient(srv.URL, 2)
	if err := c.doJSON(context.Background(), "/x", map[string]string{"a": "b"}, nil); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("got %d requests, want 2", len(bodies))
	}
	for i, b := range bodies {
		if b != `{"a":"b"}` {
			t.Errorf("attempt %d sent body %q, want the payload replayed", i+1, b)
		}
	}
}

// TestRetryAfterIsHonoured checks the server's own timing wins over the
// computed backoff, and that a hostile value cannot stall a build.
func TestRetryAfterIsHonoured(t *testing.T) {
	t.Run("seconds header is preferred over backoff", func(t *testing.T) {
		c := fastRetryClient("http://unused", 2)
		if got := c.retryDelay(1, "2"); got != 2*time.Second {
			t.Errorf("delay = %v, want 2s from the header", got)
		}
	})

	t.Run("an absurd value is capped", func(t *testing.T) {
		c := fastRetryClient("http://unused", 2)
		if got := c.retryDelay(1, "86400"); got != maxRetryAfter {
			t.Errorf("delay = %v, want it capped at %v", got, maxRetryAfter)
		}
	})

	t.Run("an http date is understood", func(t *testing.T) {
		c := fastRetryClient("http://unused", 2)
		when := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
		got := c.retryDelay(1, when)
		if got < time.Second || got > 4*time.Second {
			t.Errorf("delay = %v, want roughly 3s", got)
		}
	})

	t.Run("a past date yields no wait", func(t *testing.T) {
		c := fastRetryClient("http://unused", 2)
		when := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
		if got := c.retryDelay(1, when); got != 0 {
			t.Errorf("delay = %v, want 0", got)
		}
	})

	t.Run("garbage falls back to backoff", func(t *testing.T) {
		c := fastRetryClient("http://unused", 2)
		if got := c.retryDelay(1, "soon"); got <= 0 || got > maxRetryDelay {
			t.Errorf("delay = %v, want a computed backoff", got)
		}
	})

	t.Run("the header is used end to end", func(t *testing.T) {
		srv, hits := countingServer(t, http.StatusTooManyRequests, map[string]string{"Retry-After": "0"})
		c := fastRetryClient(srv.URL, 2)
		if err := c.doJSON(context.Background(), "/x", nil, nil); err == nil {
			t.Fatal("expected an error")
		}
		if got := hits.Load(); got != 3 {
			t.Errorf("made %d attempts, want 3", got)
		}
	})
}

// TestRetryStopsOnContextCancellation: retries are bounded by the same deadline
// as the request, so a cancelled context must end the loop promptly rather than
// sleeping out the backoff.
func TestRetryStopsOnContextCancellation(t *testing.T) {
	srv, hits := countingServer(t, http.StatusServiceUnavailable, nil)

	c := New("k", srv.URL, WithRetries(5))
	c.retryBase = 2 * time.Second // long enough that a retry would be obvious

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.doJSON(ctx, "/x", nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error")
	}
	if elapsed > time.Second {
		t.Errorf("took %v, want the cancellation to cut the backoff short", elapsed)
	}
	if got := hits.Load(); got > 2 {
		t.Errorf("made %d attempts after cancellation, want at most 2", got)
	}
}

// TestRetryRespectsTheTimeoutDeadline: backoff must not extend --timeout.
func TestRetryRespectsTheTimeoutDeadline(t *testing.T) {
	srv, _ := countingServer(t, http.StatusServiceUnavailable, nil)

	c := New("k", srv.URL, WithRetries(10), WithTimeout(200*time.Millisecond))
	c.retryBase = 100 * time.Millisecond

	start := time.Now()
	if err := c.doJSON(context.Background(), "/x", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v, want it bounded by the 200ms timeout", elapsed)
	}
}

// TestRetryNotifyReportsEachAttempt checks the sink sees one call per retry,
// with the numbers a human needs to understand a pause.
func TestRetryNotifyReportsEachAttempt(t *testing.T) {
	srv, _ := countingServer(t, http.StatusTooManyRequests, map[string]string{
		"Retry-After": "0",
		"X-Trace-Id":  "trace-9",
	})

	var seen []RetryInfo
	c := fastRetryClient(srv.URL, 2, WithRetryNotify(func(i RetryInfo) { seen = append(seen, i) }))

	if err := c.doJSON(context.Background(), "/x", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if len(seen) != 2 {
		t.Fatalf("sink saw %d retries, want 2", len(seen))
	}
	for i, info := range seen {
		if info.Attempt != i+1 {
			t.Errorf("retry %d: Attempt = %d", i, info.Attempt)
		}
		if info.Remaining != 2-(i+1) {
			t.Errorf("retry %d: Remaining = %d", i, info.Remaining)
		}
		if info.StatusCode != http.StatusTooManyRequests {
			t.Errorf("retry %d: StatusCode = %d", i, info.StatusCode)
		}
		if info.TraceID != "trace-9" {
			t.Errorf("retry %d: TraceID = %q", i, info.TraceID)
		}
	}
}

// TestStreamRetriesEstablishmentOnly is the important stream case. A non-2xx
// arrives before any event, so replaying is safe; but once the server has
// answered 2xx the stream is live and must never be replayed, even if it fails
// partway, because the caller has already seen events.
func TestStreamRetriesEstablishmentOnly(t *testing.T) {
	t.Run("a retryable status before any event is retried", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if hits.Add(1) < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: done\ndata: {}\n\n")
		}))
		defer srv.Close()

		c := fastRetryClient(srv.URL, 2)
		var events int
		if err := c.doStream(context.Background(), "/s", nil, func(Event) error {
			events++
			return nil
		}); err != nil {
			t.Fatalf("doStream: %v", err)
		}
		if hits.Load() != 3 {
			t.Errorf("made %d attempts, want 3", hits.Load())
		}
		if events != 1 {
			t.Errorf("got %d events, want 1", events)
		}
	})

	t.Run("a non-retryable status is not retried", func(t *testing.T) {
		srv, hits := countingServer(t, http.StatusBadRequest, nil)
		c := fastRetryClient(srv.URL, 2)
		if err := c.doStream(context.Background(), "/s", nil, func(Event) error { return nil }); err == nil {
			t.Fatal("expected an error")
		}
		if got := hits.Load(); got != 1 {
			t.Errorf("made %d attempts, want 1", got)
		}
	})

	t.Run("a stream that already emitted an event is never retried", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// One good event, then the handler returns and the body ends
			// mid-stream with no terminal event.
			_, _ = io.WriteString(w, "event: tick\ndata: {}\n\n")
			w.(http.Flusher).Flush()
		}))
		defer srv.Close()

		c := fastRetryClient(srv.URL, 2)
		var events int
		// The consumer fails after the first event, standing in for any
		// mid-stream failure.
		err := c.doStream(context.Background(), "/s", nil, func(Event) error {
			events++
			return errors.New("consumer gave up")
		})
		if err == nil {
			t.Fatal("expected the consumer error to surface")
		}
		if got := hits.Load(); got != 1 {
			t.Errorf("made %d attempts, want exactly 1: a live stream must never be replayed", got)
		}
		if events != 1 {
			t.Errorf("got %d events, want 1", events)
		}
	})
}

// TestBackoffIsJittered checks successive delays are not identical, so parallel
// CLI invocations that hit the same rate limit do not retry in lockstep.
func TestBackoffIsJittered(t *testing.T) {
	c := New("k", "http://unused")
	c.retryBase = time.Second

	seen := map[time.Duration]bool{}
	for range 20 {
		seen[c.backoff(3)] = true
	}
	if len(seen) < 5 {
		t.Errorf("only %d distinct delays across 20 draws, jitter looks absent", len(seen))
	}
	for d := range seen {
		if d <= 0 || d > maxRetryDelay {
			t.Errorf("delay %v out of range", d)
		}
	}
}
