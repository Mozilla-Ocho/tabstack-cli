package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/charmbracelet/lipgloss"
)

// OutputMode selects how results are rendered. Pretty is the styled,
// human-facing mode; JSON is the machine-facing mode that emits raw JSON (and
// NDJSON for streams) so output composes with tools like jq.
type OutputMode string

const (
	ModePretty OutputMode = "pretty"
	ModeJSON   OutputMode = "json"
)

// Renderer carries everything the output helpers need: where to write, which
// mode we are in, and the resolved styles.
type Renderer struct {
	Out    io.Writer
	Err    io.Writer
	Mode   OutputMode
	Styles Styles
}

// PrintJSON writes raw JSON. In pretty mode it indents; in JSON mode it writes
// the bytes through as-is so we never reshape a caller-defined schema result.
func (r Renderer) PrintJSON(raw json.RawMessage) error {
	if r.Mode == ModeJSON {
		_, err := fmt.Fprintln(r.Out, string(raw))
		return err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		// Not valid JSON for some reason, fall back to raw.
		_, err := fmt.Fprintln(r.Out, string(raw))
		return err
	}
	_, err := fmt.Fprintln(r.Out, buf.String())
	return err
}

// PrintMarkdown renders an extract/markdown response. In JSON mode it prints the
// full response object; in pretty mode it prints the content, with metadata as
// a short styled header when present.
func (r Renderer) PrintMarkdown(resp client.ExtractMarkdownResponse) error {
	if r.Mode == ModeJSON {
		raw, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(r.Out, string(raw))
		return err
	}

	if resp.Metadata != nil {
		m := resp.Metadata
		if m.Title != "" {
			fmt.Fprintln(r.Out, r.Styles.Label.Render(m.Title))
		}
		if m.SiteName != "" || m.Author != "" {
			line := strings.TrimSpace(strings.Join(nonEmpty(m.SiteName, m.Author), " - "))
			if line != "" {
				fmt.Fprintln(r.Out, r.Styles.Muted.Render(line))
			}
		}
		fmt.Fprintln(r.Out)
	}

	_, err := fmt.Fprintln(r.Out, resp.Content)
	return err
}

// PrintRaw writes a document body and nothing else: no styling, no header, no
// JSON envelope, and deliberately **the same in either output mode**. It backs
// `extract markdown --raw`, whose whole purpose is to produce the artifact the
// user asked for when stdout is redirected. Mode-awareness is what makes
// `> page.md` yield JSON, so raw output must not consult the mode.
//
// The body is emitted with exactly one trailing newline, so a redirect gives a
// well-formed text file and `$(...)` capture (which strips trailing newlines)
// behaves. Empty content writes nothing at all rather than a lone newline: an
// empty page should produce an empty file.
func (r Renderer) PrintRaw(body string) error {
	if body == "" {
		return nil
	}
	_, err := fmt.Fprintln(r.Out, strings.TrimRight(body, "\n"))
	return err
}

// RenderEvent prints a single streamed SSE event. In JSON mode it emits one
// NDJSON line per event so a stream becomes a clean line-delimited log. In
// pretty mode it formats a timeline line styled by event type.
func (r Renderer) RenderEvent(e client.Event) error {
	if r.Mode == ModeJSON {
		line, err := json.Marshal(struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data,omitempty"`
		}{Event: e.Name, Data: e.Data})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(r.Out, string(line))
		return err
	}

	label, style := r.eventStyle(e.Name)
	summary := summariseEvent(e)

	tag := style.Render(label)
	if summary == "" {
		_, err := fmt.Fprintln(r.Out, tag)
		return err
	}
	_, err := fmt.Fprintf(r.Out, "%s %s\n", tag, r.Styles.Muted.Render(summary))
	return err
}

// eventStyle maps an event name to a display label and a style. Names are
// namespaced (agent:processing, browser:navigated) so we switch on the prefix.
func (r Renderer) eventStyle(name string) (string, lipgloss.Style) {
	switch {
	case name == "complete" || name == "done" || name == "task:completed":
		return name, r.Styles.Success
	case name == "error":
		return name, r.Styles.ErrorTag
	case strings.HasPrefix(name, "agent:"):
		return name, r.Styles.Agent
	case strings.HasPrefix(name, "browser:"):
		return name, r.Styles.Browser
	case name == "start" || name == "phase":
		return name, r.Styles.Agent
	default:
		return name, r.Styles.Muted
	}
}

// PrintFinalAnswer renders a terminal "final answer" block. In JSON mode
// nothing is printed here because the events already carried the data. In
// pretty mode a short, single-line answer (e.g. an automate result) is boxed,
// while a long or multi-line report (e.g. research output) is printed plain
// under a heading, since a border around multi-paragraph text reads poorly.
func (r Renderer) PrintFinalAnswer(text string) {
	if r.Mode == ModeJSON || strings.TrimSpace(text) == "" {
		return
	}
	fmt.Fprintln(r.Out)

	if isLongAnswer(text) {
		fmt.Fprintln(r.Out, r.Styles.Label.Render("Result"))
		fmt.Fprintln(r.Out, r.Styles.Muted.Render(strings.Repeat("─", 48)))
		fmt.Fprintln(r.Out, r.highlightCites(strings.TrimSpace(text)))
		return
	}
	fmt.Fprintln(r.Out, r.Styles.Box.Render(r.highlightCites(text)))
}

// citeMarker matches inline citation markers like [1] or [12] in report text.
var citeMarker = regexp.MustCompile(`\[\d+\]`)

// highlightCites styles every [n] marker in text with the citation style so the
// markers in the report match the numbers in the Sources list.
func (r Renderer) highlightCites(text string) string {
	return highlightCitesWith(text, func(s string) string { return r.Styles.Cite.Render(s) })
}

// highlightCitesWith is the testable core: it wraps each [n] marker using wrap.
func highlightCitesWith(text string, wrap func(string) string) string {
	return citeMarker.ReplaceAllStringFunc(text, wrap)
}

// PrintCitations renders the research source list under a "Sources" heading,
// numbered to match the [n] markers in the report. In JSON mode nothing is
// printed since the events already carried the sources. Each entry shows its
// number, title (when present), and URL.
func (r Renderer) PrintCitations(cites []client.Citation) {
	if r.Mode == ModeJSON || len(cites) == 0 {
		return
	}
	fmt.Fprintln(r.Out)
	fmt.Fprintln(r.Out, r.Styles.Label.Render("Sources"))

	for i, c := range cites {
		n := c.Number
		if n == 0 {
			n = i + 1
		}
		tag := r.Styles.Cite.Render(fmt.Sprintf("[%d]", n))

		body := c.URL
		switch {
		case c.Title != "" && c.URL != "":
			body = c.Title + " " + r.Styles.Muted.Render("("+c.URL+")")
		case c.Title != "":
			body = c.Title
		}
		fmt.Fprintf(r.Out, "%s %s\n", tag, body)
	}
}

// isLongAnswer reports whether text is too large to read well inside a border:
// anything spanning multiple lines, or a single line past a comfortable width.
func isLongAnswer(text string) bool {
	if strings.Contains(strings.TrimSpace(text), "\n") {
		return true
	}
	return len(text) > 200
}

// PrintError writes an error to stderr. Pretty mode tags it; JSON mode emits a
// single {"error": "..."} object so failures stay parseable on the error stream.
func (r Renderer) PrintError(err error) {
	if r.Mode == ModeJSON {
		line, _ := json.Marshal(struct {
			Error string `json:"error"`
		}{Error: err.Error()})
		fmt.Fprintln(r.Err, string(line))
		return
	}
	fmt.Fprintf(r.Err, "%s %s\n", r.Styles.ErrorTag.Render("error:"), err.Error())
}

// summariseEvent pulls a short, human-readable summary out of an event's data
// payload. The automate stream nests informative fields (operation, title, url,
// finalAnswer) which make far better timeline text than the raw JSON blob.
func summariseEvent(e client.Event) string {
	var d struct {
		Operation   string `json:"operation"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		Task        string `json:"task"`
		FinalAnswer string `json:"finalAnswer"`
		Message     string `json:"message"`
		Phase       string `json:"phase"`
		Success     *bool  `json:"success"`
	}
	if err := e.Decode(&d); err != nil {
		return ""
	}

	switch {
	case d.Operation != "":
		return d.Operation
	case d.Phase != "":
		return d.Phase
	case d.Message != "":
		return d.Message
	case d.Title != "" && d.URL != "":
		return d.Title + " (" + d.URL + ")"
	case d.URL != "":
		return d.URL
	case d.Task != "":
		return d.Task
	case d.FinalAnswer != "":
		return d.FinalAnswer
	default:
		return ""
	}
}

// nonEmpty returns the non-empty strings from the input, preserving order.
func nonEmpty(vals ...string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}
