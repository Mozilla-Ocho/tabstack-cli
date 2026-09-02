package cmd

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
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

// mockClientFunc is mockClient with the round trip supplied by the caller, for
// tests that need to assert whether a request was made at all.
func mockClientFunc(fn func(*http.Request) (*http.Response, error)) *client.Client {
	h := &http.Client{Transport: rtFunc(fn)}
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

// TestNoCacheAliases checks both spellings set the same value and that only the
// canonical one shows up in help. --nocache is kept working for existing
// scripts, so a regression here breaks users silently rather than loudly.
func TestNoCacheAliases(t *testing.T) {
	for _, spelling := range []string{"--no-cache", "--nocache"} {
		var v bool
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		addNoCacheFlag(fs, &v)
		if err := fs.Parse([]string{spelling}); err != nil {
			t.Fatalf("%s: %v", spelling, err)
		}
		if !v {
			t.Errorf("%s did not set the flag", spelling)
		}
	}

	var v bool
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	addNoCacheFlag(fs, &v)
	if !fs.Lookup("nocache").Hidden {
		t.Error("--nocache should be hidden so help lists one spelling")
	}
	if fs.Lookup("no-cache").Hidden {
		t.Error("--no-cache should be the visible spelling")
	}
}

// TestExtractMarkdownRaw covers the flag end to end. The bug it fixes is that
// `> page.md` resolves to JSON mode and writes an envelope into a .md file, so
// the mode-independence assertion is the important one here.
func TestExtractMarkdownRaw(t *testing.T) {
	const body = "# Example Domain\n\nThis domain is for use in examples."

	cases := []struct {
		name string
		mode ui.OutputMode
	}{
		{"json mode (the redirect case)", ui.ModeJSON},
		{"pretty mode", ui.ModePretty},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := `{"content":` + strconv.Quote(body) + `,"url":"https://example.com"}`
			out := setTestAppWithClient(t, mockClient(200, payload))
			rootApp.renderer.Mode = tc.mode

			cmd := newExtractMarkdownCmd()
			if err := cmd.Flags().Set("raw", "true"); err != nil {
				t.Fatal(err)
			}
			if err := cmd.RunE(cmd, []string{"https://example.com"}); err != nil {
				t.Fatalf("RunE: %v", err)
			}

			got := out.String()
			if got != body+"\n" {
				t.Errorf("got %q, want %q", got, body+"\n")
			}
			if strings.Contains(got, `"content"`) {
				t.Errorf("raw output contains the JSON envelope: %q", got)
			}
			if n := strings.Count(got, "\n") - strings.Count(strings.TrimRight(got, "\n"), "\n"); n != 1 {
				t.Errorf("want exactly one trailing newline, got %d in %q", n, got)
			}
		})
	}
}

// TestExtractMarkdownRawRejectsMetadata: the two flags contradict each other,
// so the command refuses with exit 2 and names both rather than silently
// dropping one.
func TestExtractMarkdownRawRejectsMetadata(t *testing.T) {
	setTestAppWithClient(t, mockClient(200, `{"content":"x"}`))

	cmd := newExtractMarkdownCmd()
	for _, f := range []string{"raw", "metadata"} {
		if err := cmd.Flags().Set(f, "true"); err != nil {
			t.Fatal(err)
		}
	}

	err := cmd.RunE(cmd, []string{"https://example.com"})
	if err == nil {
		t.Fatal("combining --raw and --metadata was accepted")
	}
	if got := codeOf(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	for _, want := range []string{"--raw", "--metadata"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not name %s: %v", want, err)
		}
	}
}

// TestExtractMarkdownWithoutRawKeepsEnvelope pins the default: --raw is opt-in,
// so existing scripts that parse the piped envelope are unaffected.
func TestExtractMarkdownWithoutRawKeepsEnvelope(t *testing.T) {
	out := setTestAppWithClient(t, mockClient(200, `{"content":"# Body","url":"https://e"}`))
	cmd := newExtractMarkdownCmd()
	if err := cmd.RunE(cmd, []string{"https://e"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), `"content"`) {
		t.Errorf("default output lost the envelope: %q", out.String())
	}
}
