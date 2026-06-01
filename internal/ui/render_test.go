package ui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
)

// newTestRenderer builds a renderer writing into buffers with color disabled so
// assertions match plain text.
func newTestRenderer(mode OutputMode) (Renderer, *bytes.Buffer, *bytes.Buffer) {
	var out, errBuf bytes.Buffer
	r := Renderer{Out: &out, Err: &errBuf, Mode: mode, Styles: NewStyles(true)}
	return r, &out, &errBuf
}

func TestPrintJSONModePassthrough(t *testing.T) {
	r, out, _ := newTestRenderer(ModeJSON)
	if err := r.PrintJSON(json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != `{"a":1}` {
		t.Errorf("got %q, want passthrough", out.String())
	}
}

func TestPrintJSONPrettyIndents(t *testing.T) {
	r, out, _ := newTestRenderer(ModePretty)
	if err := r.PrintJSON(json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\n  \"a\": 1") {
		t.Errorf("expected indented output, got %q", out.String())
	}
}

func TestPrintJSONPrettyInvalidFallsBack(t *testing.T) {
	r, out, _ := newTestRenderer(ModePretty)
	if err := r.PrintJSON(json.RawMessage(`not json`)); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "not json" {
		t.Errorf("got %q, want raw fallback", out.String())
	}
}

func TestRenderEventJSONIsNDJSON(t *testing.T) {
	r, out, _ := newTestRenderer(ModeJSON)
	e := client.Event{Name: "phase", Data: json.RawMessage(`{"phase":"search"}`)}
	if err := r.RenderEvent(e); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &got); err != nil {
		t.Fatalf("not valid NDJSON: %v (%q)", err, out.String())
	}
	if got.Event != "phase" {
		t.Errorf("Event = %q", got.Event)
	}
}

func TestRenderEventPrettyShowsSummary(t *testing.T) {
	r, out, _ := newTestRenderer(ModePretty)
	e := client.Event{Name: "agent:processing", Data: json.RawMessage(`{"operation":"planning"}`)}
	if err := r.RenderEvent(e); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "agent:processing") || !strings.Contains(s, "planning") {
		t.Errorf("output missing label or summary: %q", s)
	}
}

func TestPrintFinalAnswer(t *testing.T) {
	// Pretty mode prints the text; JSON mode and empty text print nothing.
	r, out, _ := newTestRenderer(ModePretty)
	r.PrintFinalAnswer("the answer")
	if !strings.Contains(out.String(), "the answer") {
		t.Errorf("pretty: missing answer in %q", out.String())
	}

	r, out, _ = newTestRenderer(ModeJSON)
	r.PrintFinalAnswer("the answer")
	if out.String() != "" {
		t.Errorf("json mode should print nothing, got %q", out.String())
	}

	r, out, _ = newTestRenderer(ModePretty)
	r.PrintFinalAnswer("   ")
	if out.String() != "" {
		t.Errorf("blank answer should print nothing, got %q", out.String())
	}
}

func TestPrintFinalAnswerShortIsBoxed(t *testing.T) {
	// A short, single-line answer goes through the box path: no "Result"
	// heading or rule.
	r, out, _ := newTestRenderer(ModePretty)
	r.PrintFinalAnswer("short answer")
	s := out.String()
	if strings.Contains(s, "Result") || strings.Contains(s, "─") {
		t.Errorf("short answer should be boxed, not headed: %q", s)
	}
	if !strings.Contains(s, "short answer") {
		t.Errorf("missing text: %q", s)
	}
}

func TestPrintFinalAnswerLongIsHeaded(t *testing.T) {
	// A multi-line report takes the plain heading path.
	r, out, _ := newTestRenderer(ModePretty)
	report := "# Findings\n\nParagraph one.\n\nParagraph two."
	r.PrintFinalAnswer(report)
	s := out.String()
	if !strings.Contains(s, "Result") || !strings.Contains(s, "─") {
		t.Errorf("long report should have heading and rule: %q", s)
	}
	if !strings.Contains(s, "Paragraph two.") {
		t.Errorf("missing body: %q", s)
	}
}

func TestPrintCitations(t *testing.T) {
	r, out, _ := newTestRenderer(ModePretty)
	r.PrintCitations([]client.Citation{
		{Number: 1, Title: "First", URL: "https://a"},
		{URL: "https://b"}, // no number -> falls back to position (2)
	})
	s := out.String()
	for _, want := range []string{"Sources", "[1]", "First", "https://a", "[2]", "https://b"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q in %q", want, s)
		}
	}
}

func TestPrintCitationsExplicitNumber(t *testing.T) {
	// An explicit Number must win over list position.
	r, out, _ := newTestRenderer(ModePretty)
	r.PrintCitations([]client.Citation{{Number: 7, URL: "https://x"}})
	if !strings.Contains(out.String(), "[7]") {
		t.Errorf("expected [7], got %q", out.String())
	}
}

func TestPrintCitationsEmptyAndJSON(t *testing.T) {
	// Nothing to print, or JSON mode -> no output.
	r, out, _ := newTestRenderer(ModePretty)
	r.PrintCitations(nil)
	if out.String() != "" {
		t.Errorf("empty should print nothing, got %q", out.String())
	}

	r, out, _ = newTestRenderer(ModeJSON)
	r.PrintCitations([]client.Citation{{URL: "https://a"}})
	if out.String() != "" {
		t.Errorf("json mode should print nothing, got %q", out.String())
	}
}

func TestHighlightCites(t *testing.T) {
	wrap := func(s string) string { return "<" + s + ">" }
	cases := []struct {
		in, want string
	}{
		{"see [1] and [23]", "see <[1]> and <[23]>"},
		{"no markers here", "no markers here"},
		{"link [text](url) untouched", "link [text](url) untouched"},
		{"decimals [1.2] untouched", "decimals [1.2] untouched"},
		{"[1][2] adjacent", "<[1]><[2]> adjacent"},
	}
	for _, tc := range cases {
		if got := highlightCitesWith(tc.in, wrap); got != tc.want {
			t.Errorf("highlightCitesWith(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsLongAnswer(t *testing.T) {
	if isLongAnswer("short") {
		t.Error("short single line should not be long")
	}
	if !isLongAnswer("line1\nline2") {
		t.Error("multi-line should be long")
	}
	if !isLongAnswer(strings.Repeat("x", 201)) {
		t.Error("over-width single line should be long")
	}
}

func TestPrintErrorJSON(t *testing.T) {
	r, _, errBuf := newTestRenderer(ModeJSON)
	r.PrintError(errStr("boom"))
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(errBuf.String())), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got.Error != "boom" {
		t.Errorf("Error = %q", got.Error)
	}
}

func TestPrintErrorPretty(t *testing.T) {
	r, _, errBuf := newTestRenderer(ModePretty)
	r.PrintError(errStr("boom"))
	if !strings.Contains(errBuf.String(), "boom") {
		t.Errorf("got %q", errBuf.String())
	}
}

func TestSummariseEvent(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"operation wins", `{"operation":"op","phase":"ph"}`, "op"},
		{"phase", `{"phase":"searching"}`, "searching"},
		{"message", `{"message":"hi"}`, "hi"},
		{"title+url", `{"title":"T","url":"https://x"}`, "T (https://x)"},
		{"url only", `{"url":"https://x"}`, "https://x"},
		{"task", `{"task":"do thing"}`, "do thing"},
		{"final answer", `{"finalAnswer":"42"}`, "42"},
		{"empty", `{}`, ""},
		{"invalid json", `nope`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summariseEvent(client.Event{Data: json.RawMessage(tc.data)})
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEventStyle(t *testing.T) {
	r, _, _ := newTestRenderer(ModePretty)
	cases := []string{"complete", "done", "task:completed", "error",
		"agent:processing", "browser:navigated", "start", "phase", "unknown:thing"}
	for _, name := range cases {
		label, _ := r.eventStyle(name)
		if label != name {
			t.Errorf("eventStyle(%q) label = %q", name, label)
		}
	}
}

func TestNonEmpty(t *testing.T) {
	got := nonEmpty("a", "", "  ", "b")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v, want [a b]", got)
	}
}

func TestPrintMarkdownPretty(t *testing.T) {
	r, out, _ := newTestRenderer(ModePretty)
	resp := client.ExtractMarkdownResponse{
		Content:  "# Body",
		Metadata: &client.Metadata{Title: "My Title", SiteName: "Site", Author: "Me"},
	}
	if err := r.PrintMarkdown(resp); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"My Title", "Site - Me", "# Body"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q in %q", want, s)
		}
	}
}

func TestPrintMarkdownJSON(t *testing.T) {
	r, out, _ := newTestRenderer(ModeJSON)
	resp := client.ExtractMarkdownResponse{Content: "x", URL: "https://e"}
	if err := r.PrintMarkdown(resp); err != nil {
		t.Fatal(err)
	}
	var got client.ExtractMarkdownResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got.Content != "x" || got.URL != "https://e" {
		t.Errorf("got %+v", got)
	}
}

// errStr is a tiny error helper for the renderer tests.
type errStr string

func (e errStr) Error() string { return string(e) }
