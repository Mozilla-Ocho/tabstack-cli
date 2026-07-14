package cmd

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
)

// rtFunc adapts a function to http.RoundTripper.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// mockClient returns a client whose HTTP layer always responds with the given
// status and body, so command RunE paths can be exercised without a network.
func mockClient(status int, body string) *client.Client {
	h := &http.Client{Transport: rtFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	return client.New("test-key", "https://api.test", client.WithHTTPClient(h))
}

// setTestAppWithClient installs a rootApp with a JSON renderer and the given
// client, returning the output buffer.
func setTestAppWithClient(t *testing.T, c *client.Client) *bytes.Buffer {
	t.Helper()
	buf := setTestApp(t)
	rootApp.client = c
	return buf
}

func TestExtractMarkdownCmd(t *testing.T) {
	out := setTestAppWithClient(t, mockClient(200, `{"content":"hello world","url":"https://x"}`))
	cmd := newExtractMarkdownCmd()
	if err := cmd.RunE(cmd, []string{"https://x"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), "hello world") {
		t.Errorf("output = %q", out.String())
	}
}

func TestExtractMarkdownInvalidEffort(t *testing.T) {
	setTestAppWithClient(t, mockClient(200, `{}`))
	cmd := newExtractMarkdownCmd()
	_ = cmd.Flags().Set("effort", "turbo")
	if err := cmd.RunE(cmd, []string{"https://x"}); codeOf(err) != 2 {
		t.Errorf("err = %v, want code 2", err)
	}
}

func TestExtractJSONCmd(t *testing.T) {
	out := setTestAppWithClient(t, mockClient(200, `{"title":"Result"}`))
	cmd := newExtractJSONCmd()
	_ = cmd.Flags().Set("schema", `{"type":"object"}`)
	if err := cmd.RunE(cmd, []string{"https://x"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), "Result") {
		t.Errorf("output = %q", out.String())
	}
}

func TestExtractJSONMissingSchema(t *testing.T) {
	setTestAppWithClient(t, mockClient(200, `{}`))
	cmd := newExtractJSONCmd()
	if err := cmd.RunE(cmd, []string{"https://x"}); codeOf(err) != 2 {
		t.Errorf("err = %v, want code 2", err)
	}
}

func TestExtractJSONAPIError(t *testing.T) {
	setTestAppWithClient(t, mockClient(400, `{"error":"bad schema"}`))
	cmd := newExtractJSONCmd()
	_ = cmd.Flags().Set("schema", `{"type":"object"}`)
	if err := cmd.RunE(cmd, []string{"https://x"}); codeOf(err) != 3 {
		t.Errorf("err = %v, want code 3 (API error)", err)
	}
}

func TestGenerateJSONCmd(t *testing.T) {
	out := setTestAppWithClient(t, mockClient(200, `{"summary":"ok"}`))
	cmd := newGenerateJSONCmd()
	_ = cmd.Flags().Set("instructions", "summarise this")
	_ = cmd.Flags().Set("schema", `{"type":"object"}`)
	if err := cmd.RunE(cmd, []string{"https://x"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Errorf("output = %q", out.String())
	}
}

func TestGenerateJSONMissingInstructions(t *testing.T) {
	setTestAppWithClient(t, mockClient(200, `{}`))
	cmd := newGenerateJSONCmd()
	_ = cmd.Flags().Set("schema", `{"type":"object"}`)
	if err := cmd.RunE(cmd, []string{"https://x"}); codeOf(err) != 2 {
		t.Errorf("err = %v, want code 2", err)
	}
}

func TestInputCmd(t *testing.T) {
	out := setTestAppWithClient(t, mockClient(200, `{}`))
	cmd := newInputCmd()
	_ = cmd.Flags().Set("data", `{"fields":[{"ref":"f1","value":"yes"}]}`)
	if err := cmd.RunE(cmd, []string{"req-123"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), "submitted") {
		t.Errorf("output = %q", out.String())
	}
}

func TestInputCmdMissingData(t *testing.T) {
	setTestAppWithClient(t, mockClient(200, `{}`))
	cmd := newInputCmd()
	if err := cmd.RunE(cmd, []string{"req-123"}); codeOf(err) != 2 {
		t.Errorf("err = %v, want code 2", err)
	}
}

func TestInputCmdEmptyPayload(t *testing.T) {
	setTestAppWithClient(t, mockClient(200, `{}`))
	cmd := newInputCmd()
	_ = cmd.Flags().Set("data", `{}`) // neither fields nor cancelled
	if err := cmd.RunE(cmd, []string{"req-123"}); codeOf(err) != 2 {
		t.Errorf("err = %v, want code 2", err)
	}
}

func TestInputCmdAPIError(t *testing.T) {
	setTestAppWithClient(t, mockClient(404, `{"error":"no such task"}`))
	cmd := newInputCmd()
	_ = cmd.Flags().Set("data", `{"cancelled":true}`)
	if err := cmd.RunE(cmd, []string{"req-123"}); codeOf(err) != 3 {
		t.Errorf("err = %v, want code 3", err)
	}
}

func TestRunStreamSuccess(t *testing.T) {
	setTestApp(t)
	events := []client.Event{
		{Name: "agent:processing", Data: []byte(`{"operation":"navigate"}`)},
		{Name: "complete", Data: []byte(`{"success":true,"finalAnswer":"all done"}`)},
	}
	res, err := runStream(func(fn func(client.Event) error) error {
		for _, e := range events {
			if err := fn(e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runStream: %v", err)
	}
	if res.failed {
		t.Error("res.failed = true, want false")
	}
	if res.finalAnswer != "all done" {
		t.Errorf("finalAnswer = %q", res.finalAnswer)
	}
}

func TestRunStreamErrorEvent(t *testing.T) {
	setTestApp(t)
	res, err := runStream(func(fn func(client.Event) error) error {
		return fn(client.Event{Name: "error", Data: []byte(`{"message":"boom"}`)})
	})
	if err != nil {
		t.Fatalf("runStream: %v", err)
	}
	if !res.failed {
		t.Error("res.failed = false, want true")
	}
	if res.failMessage != "boom" {
		t.Errorf("failMessage = %q", res.failMessage)
	}
}

func TestRunStreamCompletedUnsuccessful(t *testing.T) {
	setTestApp(t)
	res, err := runStream(func(fn func(client.Event) error) error {
		return fn(client.Event{Name: "task:completed", Data: []byte(`{"success":false}`)})
	})
	if err != nil {
		t.Fatalf("runStream: %v", err)
	}
	if !res.failed {
		t.Error("res.failed = false, want true for success:false")
	}
}
