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

// retryTestClient points a client at h with sleeps recorded instead of taken.
func retryTestClient(t *testing.T, h http.HandlerFunc, opts ...Option) (*Client, *[]time.Duration, *[]RetryInfo) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var slept []time.Duration
	var notified []RetryInfo
	opts = append(opts, WithRetryNotify(func(ri RetryInfo) { notified = append(notified, ri) }))
	c := New("k", srv.URL, opts...)
	c.retry.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	c.retry.now = func() time.Time { return time.Unix(1000, 0) }
	return c, &slept, &notified
}

// failThen answers the first n requests with status (and headers), then 200.
func failThen(n int32, status int, hdr map[string]string, calls *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The body must be resent on every attempt.
		if b, _ := io.ReadAll(r.Body); len(b) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if calls.Add(1) <= n {
			for k, v := range hdr {
				w.Header().Set(k, v)
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"slow down"}`)
			return
		}
		_, _ = io.WriteString(w, `{"content":"ok","url":"u"}`)
	}
}

func TestRetryHonoursRetryAfter(t *testing.T) {
	var calls atomic.Int32
	c, slept, notified := retryTestClient(t, failThen(1, 429, map[string]string{"Retry-After": "3"}, &calls))

	res, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"})
	if err != nil {
		t.Fatalf("ExtractMarkdown: %v", err)
	}
	if res.Content != "ok" || calls.Load() != 2 {
		t.Errorf("content=%q calls=%d", res.Content, calls.Load())
	}
	if len(*slept) != 1 || (*slept)[0] != 3*time.Second {
		t.Errorf("slept = %v, want [3s]", *slept)
	}
	if len(*notified) != 1 || (*notified)[0] != (RetryInfo{Attempt: 1, MaxRetries: DefaultMaxRetries, Status: 429, Wait: 3 * time.Second}) {
		t.Errorf("notified = %+v", *notified)
	}
}

func TestRetryFallsBackToRateLimitReset(t *testing.T) {
	var calls atomic.Int32
	c, slept, _ := retryTestClient(t, failThen(1, 429, map[string]string{"RateLimit": `"default";r=0;t=5`}, &calls))
	if _, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"}); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 || (*slept)[0] != 5*time.Second {
		t.Errorf("slept = %v, want [5s]", *slept)
	}
}

func TestRetryBackoffWithoutServerHint(t *testing.T) {
	var calls atomic.Int32
	c, slept, _ := retryTestClient(t, failThen(2, 429, nil, &calls))
	if _, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"}); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 2 || (*slept)[0] != time.Second || (*slept)[1] != 2*time.Second {
		t.Errorf("slept = %v, want [1s 2s]", *slept)
	}
}

func TestRetryExhaustedReturnsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	c, slept, _ := retryTestClient(t, failThen(99, 429, map[string]string{"Retry-After": "2"}, &calls))
	_, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != 429 || apiErr.RetryAfter != 2*time.Second {
		t.Errorf("apiErr = %+v", apiErr)
	}
	if got, want := apiErr.Error(), "api error (429): slow down (retry after 2s)"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if calls.Load() != 1+DefaultMaxRetries || len(*slept) != DefaultMaxRetries {
		t.Errorf("calls=%d slept=%v", calls.Load(), *slept)
	}
}

func TestRetrySkipsWaitOverCap(t *testing.T) {
	var calls atomic.Int32
	c, slept, _ := retryTestClient(t, failThen(1, 429, map[string]string{"Retry-After": "120"}, &calls))
	_, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfter != 120*time.Second {
		t.Fatalf("err = %v, want 429 with RetryAfter 2m", err)
	}
	if calls.Load() != 1 || len(*slept) != 0 {
		t.Errorf("calls=%d slept=%v, want one call and no sleep", calls.Load(), *slept)
	}
}

func TestRetryBackoffErrorHasNoInventedWait(t *testing.T) {
	var calls atomic.Int32
	c, _, _ := retryTestClient(t, failThen(99, 429, nil, &calls))
	_, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfter != 0 {
		t.Fatalf("err = %v, want 429 with no RetryAfter", err)
	}
}

func TestRetryDisabled(t *testing.T) {
	var calls atomic.Int32
	c, slept, _ := retryTestClient(t, failThen(1, 429, map[string]string{"Retry-After": "1"}, &calls), WithRetry(0, 0))
	if _, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"}); err == nil {
		t.Fatal("want error with retries disabled")
	}
	if calls.Load() != 1 || len(*slept) != 0 {
		t.Errorf("calls=%d slept=%v", calls.Load(), *slept)
	}
}

func TestRetry503OnlyWithRetryAfter(t *testing.T) {
	var calls atomic.Int32
	c, _, _ := retryTestClient(t, failThen(1, 503, nil, &calls))
	if _, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"}); err == nil {
		t.Fatal("bare 503 should not be retried")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}

	calls.Store(0)
	c, _, _ = retryTestClient(t, failThen(1, 503, map[string]string{"Retry-After": "1"}, &calls))
	if _, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"}); err != nil {
		t.Fatalf("503 with Retry-After should be retried: %v", err)
	}
}

func TestRetryNotOnOtherErrors(t *testing.T) {
	var calls atomic.Int32
	c, _, _ := retryTestClient(t, failThen(1, 500, map[string]string{"Retry-After": "1"}, &calls))
	if _, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"}); err == nil {
		t.Fatal("500 should not be retried")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
}

func TestRetryStreamInitialResponse(t *testing.T) {
	var calls atomic.Int32
	c, _, _ := retryTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: complete\ndata: {}\n\n")
	})
	var events int
	err := c.Research(context.Background(), ResearchRequest{Query: "q"}, func(Event) error { events++; return nil })
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if calls.Load() != 2 || events != 1 {
		t.Errorf("calls=%d events=%d", calls.Load(), events)
	}
}

func TestRetrySleepCancelled(t *testing.T) {
	var calls atomic.Int32
	c, _, _ := retryTestClient(t, failThen(1, 429, map[string]string{"Retry-After": "1"}, &calls))
	// Cancel once the client has decided to wait, so the cancellation lands in
	// the sleep rather than the request.
	ctx, cancel := context.WithCancel(context.Background())
	c.retry.sleep = sleepCtx
	c.retry.notify = func(RetryInfo) { cancel() }
	_, err := c.ExtractMarkdown(ctx, ExtractMarkdownRequest{URL: "https://e"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (no retry after cancel)", calls.Load())
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", 0, false},
		{"7", 7 * time.Second, true},
		{" 0 ", 0, true},
		{"-1", 0, false},
		{"soon", 0, false},
		{now.Add(10 * time.Second).Format(http.TimeFormat), 10 * time.Second, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, true},
	}
	for _, tc := range cases {
		got, ok := parseRetryAfter(tc.in, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseRetryAfter(%q) = %v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
