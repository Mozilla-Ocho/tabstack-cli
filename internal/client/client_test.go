package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewTrimsTrailingSlash(t *testing.T) {
	c := New("k", "https://api.example/v1/")
	if c.baseURL != "https://api.example/v1" {
		t.Errorf("baseURL = %q, want no trailing slash", c.baseURL)
	}
}

func TestWithTimeout(t *testing.T) {
	c := New("k", "https://x", WithTimeout(5*time.Second))
	if c.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", c.timeout)
	}
	// The timeout must not land on the http.Client: that would cover reading
	// the response body and so cut SSE streams off. See TestTimeoutSpares...
	if c.http.Timeout != 0 {
		t.Errorf("http.Client.Timeout = %v, want 0 (would cut streams)", c.http.Timeout)
	}
}

// TestTimeoutAppliesToJSON checks the configured timeout actually bounds a
// non-streaming call whose server is slower than the deadline.
func TestTimeoutAppliesToJSON(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	defer close(release)

	c := New("k", srv.URL, WithTimeout(50*time.Millisecond))
	var out map[string]any
	err := c.doJSON(context.Background(), "/slow", nil, &out)
	if err == nil {
		t.Fatal("doJSON succeeded, want a timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

// TestTimeoutSparesStreams is the regression test for the bug this replaces:
// WithTimeout used to set http.Client.Timeout, which bounds the whole exchange
// including the body, so `--timeout 30s` silently killed every SSE stream at
// 30s. A stream that dribbles events past the deadline must still complete.
func TestTimeoutSparesStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for range 3 {
			_, _ = io.WriteString(w, "event: tick\ndata: {\"n\":1}\n\n")
			flusher.Flush()
			time.Sleep(30 * time.Millisecond)
		}
	}))
	defer srv.Close()

	// A timeout far shorter than the stream's total lifetime.
	c := New("k", srv.URL, WithTimeout(20*time.Millisecond))
	var events int
	err := c.doStream(context.Background(), "/stream", nil, func(Event) error {
		events++
		return nil
	})
	if err != nil {
		t.Fatalf("doStream returned %v, want the stream to outlive the timeout", err)
	}
	if events != 3 {
		t.Errorf("got %d events, want 3", events)
	}
}

func TestNewRequestHeaders(t *testing.T) {
	c := New("secret", "https://api.example")
	req, err := c.newRequest(context.Background(), "/extract/json", map[string]string{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost {
		t.Errorf("method = %s", req.Method)
	}
	if req.URL.String() != "https://api.example/extract/json" {
		t.Errorf("url = %s", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer secret" {
		t.Errorf("Authorization = %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	body, _ := io.ReadAll(req.Body)
	if strings.TrimSpace(string(body)) != `{"a":"b"}` {
		t.Errorf("body = %q", body)
	}
}

func TestNewRequestNilBody(t *testing.T) {
	c := New("k", "https://x")
	req, err := c.newRequest(context.Background(), "/p", nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Body != nil {
		t.Error("expected nil body")
	}
}

// newTestClient wires a Client at a test server's URL.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New("test-key", srv.URL)
}

func TestExtractMarkdownSuccess(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/extract/markdown" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer")
		}
		_, _ = io.WriteString(w, `{"content":"# Hi","url":"https://e","metadata":{"title":"T"}}`)
	})

	resp, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "https://e"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "# Hi" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Metadata == nil || resp.Metadata.Title != "T" {
		t.Errorf("Metadata = %+v", resp.Metadata)
	}
}

func TestExtractJSONReturnsRaw(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"any":"shape","n":[1,2]}`)
	})
	raw, err := c.ExtractJSON(context.Background(), ExtractJSONRequest{
		URL:        "https://e",
		JSONSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"any":"shape","n":[1,2]}` {
		t.Errorf("raw = %s", raw)
	}
}

func TestAPIErrorDecoding(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad schema"}`)
	})
	_, err := c.ExtractJSON(context.Background(), ExtractJSONRequest{URL: "https://e"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if apiErr.Message != "bad schema" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestAPIErrorPlainBody(t *testing.T) {
	// Non-JSON error body: the whole trimmed body becomes the message.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "  upstream exploded  ")
	})
	_, err := c.ExtractMarkdown(context.Background(), ExtractMarkdownRequest{URL: "x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.Message != "upstream exploded" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

// TestAPIErrorMessageFormat also guards the leading word: fang title-cases the
// first word of a rendered error, so a message starting "api error" surfaced to
// the user as "Api error". Leading with "product" keeps the acronym intact and
// distinguishes these from console.APIError's "console error".
func TestAPIErrorMessageFormat(t *testing.T) {
	withMsg := &APIError{StatusCode: 400, Message: "nope"}
	if withMsg.Error() != "product API error (400): nope" {
		t.Errorf("Error() = %q", withMsg.Error())
	}
	noMsg := &APIError{StatusCode: 503}
	if noMsg.Error() != "product API error: status 503" {
		t.Errorf("Error() = %q", noMsg.Error())
	}
}

func TestStreamingEndpoint(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		_, _ = io.WriteString(w, "event: phase\ndata: {\"phase\":\"search\"}\n\n"+
			"event: complete\ndata: {\"success\":true}\n\n")
	})

	var names []string
	err := c.Research(context.Background(), ResearchRequest{Query: "q"}, func(e Event) error {
		names = append(names, e.Name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "phase" || names[1] != "complete" {
		t.Errorf("events = %v", names)
	}
}

func TestStreamingNon2xxIsAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"no key"}`)
	})
	err := c.Automate(context.Background(), AutomateRequest{Task: "t"}, func(e Event) error {
		t.Fatal("callback should not run on error status")
		return nil
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want 401 APIError", err)
	}
}

func TestAutomateInputRequestBody(t *testing.T) {
	var gotBody []byte
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"accepted"}`)
	})

	req := AutomateInputRequest{
		Fields: []AutomateInputFieldValue{
			{Ref: "email", Value: "test@example.com"},
		},
	}
	err := c.AutomateInput(context.Background(), "req-abc", req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotBody), `"fields"`) {
		t.Errorf("body missing 'fields' key: %s", gotBody)
	}
	if strings.Contains(string(gotBody), `"data"`) {
		t.Errorf("body still contains old 'data' key: %s", gotBody)
	}
}

func TestAutomateInputCancelled(t *testing.T) {
	var gotBody []byte
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"accepted"}`)
	})

	if err := c.AutomateInput(context.Background(), "req-xyz", AutomateInputRequest{Cancelled: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotBody), `"cancelled":true`) {
		t.Errorf("body missing cancelled: %s", gotBody)
	}
}

func TestAutomateRequestInteractiveOmitempty(t *testing.T) {
	// Default (false) must be omitted so the server applies its own default;
	// true must be sent so the task is allowed to pause for input.
	off, err := json.Marshal(AutomateRequest{Task: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(off), "interactive") {
		t.Errorf("interactive=false should be omitted: %s", off)
	}

	on, err := json.Marshal(AutomateRequest{Task: "t", Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(on), `"interactive":true`) {
		t.Errorf("interactive=true should be present: %s", on)
	}
}

func TestAutomateInputPathEscaping(t *testing.T) {
	var gotRawPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// RawPath preserves percent-encoding; Path is always decoded by the
		// stdlib router, so we must check RawPath to confirm the slashes were
		// escaped before the request was sent.
		gotRawPath = r.URL.RawPath
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"accepted"}`)
	})

	_ = c.AutomateInput(context.Background(), "id/with/slashes", AutomateInputRequest{Cancelled: true})
	if strings.Contains(gotRawPath, "id/with/slashes") {
		t.Errorf("requestID slashes were not escaped: raw path = %s", gotRawPath)
	}
	if !strings.Contains(gotRawPath, "id%2Fwith%2Fslashes") {
		t.Errorf("requestID not percent-encoded in raw path: raw path = %s", gotRawPath)
	}
}

func TestContextCancellation(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.ExtractMarkdown(ctx, ExtractMarkdownRequest{URL: "x"})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
