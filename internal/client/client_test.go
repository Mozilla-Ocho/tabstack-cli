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
	if c.http.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v", c.http.Timeout)
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

func TestAPIErrorMessageFormat(t *testing.T) {
	withMsg := &APIError{StatusCode: 400, Message: "nope"}
	if withMsg.Error() != "api error (400): nope" {
		t.Errorf("Error() = %q", withMsg.Error())
	}
	noMsg := &APIError{StatusCode: 503}
	if noMsg.Error() != "api error: status 503" {
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
