package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
)

func TestReadInputLiteral(t *testing.T) {
	got, err := readInput("hello")
	if err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadInputEmpty(t *testing.T) {
	got, err := readInput("")
	if err != nil || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadInputFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, []byte("file contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readInput("@" + path)
	if err != nil || got != "file contents" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadInputFileMissing(t *testing.T) {
	_, err := readInput("@/no/such/file/here")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadInputStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	go func() {
		_, _ = w.WriteString("piped data")
		w.Close()
	}()

	got, err := readInput("-")
	if err != nil || got != "piped data" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadJSONValid(t *testing.T) {
	got, err := readJSON(`{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("got %s", got)
	}
}

func TestReadJSONInvalid(t *testing.T) {
	_, err := readJSON(`{not json`)
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestReadJSONEmpty(t *testing.T) {
	_, err := readJSON("   ")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestGeoTarget(t *testing.T) {
	if g := geoTarget(""); g != nil {
		t.Errorf("empty country should yield nil, got %+v", g)
	}
	if g := geoTarget("   "); g != nil {
		t.Errorf("whitespace country should yield nil, got %+v", g)
	}
	g := geoTarget("gb")
	if g == nil || g.Country != "GB" {
		t.Errorf("got %+v, want Country=GB", g)
	}
}

func TestWithCode(t *testing.T) {
	if withCode(3, nil) != nil {
		t.Error("withCode(_, nil) should be nil")
	}
	err := withCode(7, errors.New("boom"))
	var c *exitErr
	if !errors.As(err, &c) {
		t.Fatalf("err = %v, want *exitErr", err)
	}
	if c.Code() != 7 {
		t.Errorf("Code = %d", c.Code())
	}
	if err.Error() != "boom" {
		t.Errorf("Error = %q", err.Error())
	}
	if !errors.Is(err, err) {
		t.Error("unwrap chain broken")
	}
}

func TestCheckLen(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		max     int
		wantErr bool
	}{
		{"empty", "", 3, false},
		{"ascii under", "ab", 3, false},
		{"ascii at cap", "abc", 3, false},
		{"ascii over", "abcd", 3, true},
		// "世界語" is 3 runes but 9 bytes: a byte-based check would wrongly
		// reject it at max=3. checkLen must count runes.
		{"multibyte at cap", "世界語", 3, false},
		{"multibyte over", "世界語", 2, true},
	}
	for _, tc := range cases {
		err := checkLen("query", tc.value, tc.max)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error", tc.name)
				continue
			}
			var c *exitErr
			if !errors.As(err, &c) || c.Code() != 2 {
				t.Errorf("%s: want exit code 2, got %v", tc.name, err)
			}
		} else if err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
	}
}

func TestClassifyError(t *testing.T) {
	if classifyError(nil) != nil {
		t.Error("nil should classify to nil")
	}

	// API error -> code 3.
	apiErr := &client.APIError{StatusCode: 400, Message: "bad"}
	out := classifyError(apiErr)
	var c *exitErr
	if !errors.As(out, &c) || c.Code() != 3 {
		t.Errorf("API error -> %v, want code 3", out)
	}

	// Generic error -> code 1.
	out = classifyError(errors.New("network down"))
	if !errors.As(out, &c) || c.Code() != 1 {
		t.Errorf("generic error -> %v, want code 1", out)
	}
}

func TestFailureError(t *testing.T) {
	withMsg := failureError("research", "rate limited")
	if withMsg.Error() != "research failed: rate limited" {
		t.Errorf("got %q", withMsg.Error())
	}
	noMsg := failureError("automation", "")
	if noMsg.Error() != "automation reported failure" {
		t.Errorf("got %q", noMsg.Error())
	}
}

func TestValidEffort(t *testing.T) {
	for _, valid := range []string{"", "min", "standard", "max"} {
		if err := validEffort(valid); err != nil {
			t.Errorf("validEffort(%q) = %v, want nil", valid, err)
		}
	}
	for _, bad := range []string{"turbo", "STANDARD", "Max"} {
		if err := validEffort(bad); err == nil {
			t.Errorf("validEffort(%q) should error", bad)
		}
	}
}

func TestValidResearchMode(t *testing.T) {
	for _, valid := range []string{"", "fast", "balanced"} {
		if err := validResearchMode(valid); err != nil {
			t.Errorf("validResearchMode(%q) = %v, want nil", valid, err)
		}
	}
	for _, bad := range []string{"deep", "FAST", "turbo"} {
		if err := validResearchMode(bad); err == nil {
			t.Errorf("validResearchMode(%q) should error", bad)
		}
	}
}

func TestClassifyErrorTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1)
	cancel()
	<-ctx.Done()
	timeoutErr := ctx.Err()

	out := classifyError(timeoutErr)
	var c *exitErr
	if !errors.As(out, &c) || c.Code() != 1 {
		t.Fatalf("timeout error -> %v, want code 1", out)
	}
	if !strings.Contains(out.Error(), "timed out") {
		t.Errorf("error message should mention 'timed out': %q", out.Error())
	}
	if strings.Contains(out.Error(), "context deadline exceeded") {
		t.Errorf("raw Go error leaked into message: %q", out.Error())
	}
}

func TestValidGeo(t *testing.T) {
	for _, ok := range []string{"", "GB", "gb", "us", " DE "} {
		if err := validGeo(ok); err != nil {
			t.Errorf("validGeo(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"GBR", "G", "G1", "12", "united kingdom"} {
		if err := validGeo(bad); err == nil {
			t.Errorf("validGeo(%q) = nil, want an error", bad)
		}
	}
}

func TestValidURL(t *testing.T) {
	for _, ok := range []string{
		"https://example.com",
		"http://example.com/a/b?c=d",
		"https://example.com:8443/x",
	} {
		if err := validURL(ok); err != nil {
			t.Errorf("validURL(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"not a url", "example.com", "ftp://example.com", "https://", ""} {
		if err := validURL(bad); err == nil {
			t.Errorf("validURL(%q) = nil, want an error", bad)
		}
	}
}

// TestExactArgsNamed checks the messages name the missing argument, and that
// neither leads with the command path: fang title-cases the first word, which
// would turn "tabstack extract markdown" into "Tabstack extract markdown".
func TestExactArgsNamed(t *testing.T) {
	cmd := &cobra.Command{Use: "markdown <url>"}
	args := exactArgsNamed("<url>")

	if err := args(cmd, []string{"https://example.com"}); err != nil {
		t.Fatalf("correct arity rejected: %v", err)
	}

	err := args(cmd, nil)
	if err == nil {
		t.Fatal("missing argument accepted")
	}
	if !strings.Contains(err.Error(), "<url>") {
		t.Errorf("message does not name the argument: %q", err)
	}
	if strings.HasPrefix(err.Error(), "markdown") {
		t.Errorf("message leads with the command path, fang will capitalise it: %q", err)
	}

	err = args(cmd, []string{"a", "b"})
	if err == nil {
		t.Fatal("extra argument accepted")
	}
	if strings.HasPrefix(err.Error(), "markdown") {
		t.Errorf("message leads with the command path, fang will capitalise it: %q", err)
	}
}

func TestFixedCompletions(t *testing.T) {
	fn := fixedCompletions("min", "standard", "max")
	got, directive := fn(nil, nil, "")
	if len(got) != 3 || got[0] != "min" || got[2] != "max" {
		t.Errorf("values = %v", got)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp (paths are not valid here)", directive)
	}
}

// TestCompleteOrgs checks org completion reads the local config and offers both
// names and ids, since both are valid selectors. It must not need a session:
// completion runs on every tab press and --org resolves locally by design.
func TestCompleteOrgs(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "tabstack"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "tabstack", "config.toml")
	body := "version = 1\nactive_org = \"org_a\"\n\n[orgs.org_a]\nname = \"Alpha\"\n\n[orgs.org_b]\nname = \"Bravo\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, directive := completeOrgs(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"Alpha", "org_a", "Bravo", "org_b"} {
		if !strings.Contains(joined, want) {
			t.Errorf("completions %v missing %q", got, want)
		}
	}
}

// TestWarnUnlikelySchema pins the heuristic in both directions. False negatives
// cost a confusing API 400; false positives cry wolf on valid input, which is
// worse, so the "does not warn" rows matter more than the "warns" ones.
func TestWarnUnlikelySchema(t *testing.T) {
	cases := []struct {
		name     string
		schema   string
		wantWarn bool
	}{
		// The mistake this exists for: example values, not a shape.
		{"plain value map", `{"title":"string"}`, true},
		{"nested value map", `{"job":{"title":"string","salary":"number"}}`, true},
		{"empty object", `{}`, true},
		{"metadata only", `{"$schema":"https://json-schema.org/draft/2020-12/schema","title":"Job"}`, true},

		// Legitimate schemas that must stay quiet.
		{"type only", `{"type":"object"}`, false},
		{"properties only", `{"properties":{"title":{"type":"string"}}}`, false},
		{"full schema", `{"type":"object","properties":{"title":{"type":"string"}}}`, false},
		{"ref", `{"$ref":"#/$defs/Job"}`, false},
		{"oneOf", `{"oneOf":[{"type":"string"},{"type":"number"}]}`, false},
		{"anyOf", `{"anyOf":[{"type":"string"}]}`, false},
		{"allOf", `{"allOf":[{"type":"object"}]}`, false},
		{"enum", `{"enum":["a","b"]}`, false},
		{"const", `{"const":42}`, false},
		{"items", `{"items":{"type":"string"}}`, false},
		{"defs", `{"$defs":{"Job":{"type":"object"}}}`, false},

		// Not an object: `true` and `false` are valid schemas, and anything else
		// is the server's call.
		{"true", `true`, false},
		{"false", `false`, false},
		{"array", `[1,2,3]`, false},
		{"string", `"hello"`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			warnUnlikelySchema(&buf, json.RawMessage(tc.schema))
			got := buf.Len() > 0
			if got != tc.wantWarn {
				t.Errorf("warned = %v, want %v (output: %q)", got, tc.wantWarn, buf.String())
			}
			if tc.wantWarn {
				for _, want := range []string{"schema list", "tabstack-schemas"} {
					if !strings.Contains(buf.String(), want) {
						t.Errorf("hint does not mention %q: %s", want, buf.String())
					}
				}
			}
		})
	}
}

// TestWarnUnlikelySchemaIsAdvisoryOnly: the hint must go to stderr, leave
// stdout clean for piping, and never change the exit code. A local heuristic
// blocking a request the server would have accepted is the failure mode.
func TestWarnUnlikelySchemaIsAdvisoryOnly(t *testing.T) {
	isolate(t)
	out := setTestAppWithClient(t, mockClient(200, `{"title":"Example"}`))
	var errBuf bytes.Buffer
	rootApp.renderer.Err = &errBuf

	cmd := newExtractJSONCmd()
	cmd.SetContext(context.Background())
	// A schema that trips the heuristic.
	if err := cmd.Flags().Set("schema", `{"title":"string"}`); err != nil {
		t.Fatal(err)
	}

	if err := cmd.RunE(cmd, []string{"https://example.com"}); err != nil {
		t.Fatalf("the hint changed the outcome: %v", err)
	}
	if !strings.Contains(errBuf.String(), "hint:") {
		t.Errorf("no hint on stderr: %q", errBuf.String())
	}
	if strings.Contains(out.String(), "hint:") {
		t.Errorf("hint leaked into stdout, which breaks piping: %q", out.String())
	}
	// The result itself is still the only thing on stdout.
	if !strings.Contains(out.String(), "Example") {
		t.Errorf("result missing from stdout: %q", out.String())
	}
}

// TestValidSchemaStaysSilent is the counterpart: a correct schema produces no
// stderr noise at all.
func TestValidSchemaStaysSilent(t *testing.T) {
	isolate(t)
	setTestAppWithClient(t, mockClient(200, `{"title":"Example"}`))
	var errBuf bytes.Buffer
	rootApp.renderer.Err = &errBuf

	cmd := newExtractJSONCmd()
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("schema", `{"type":"object","properties":{"title":{"type":"string"}}}`); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"https://example.com"}); err != nil {
		t.Fatal(err)
	}
	if errBuf.Len() != 0 {
		t.Errorf("valid schema produced stderr noise: %q", errBuf.String())
	}
}

// TestGeoValidationExitsTwo checks the local --geo check is wired into every
// command that offers the flag, and that it lands on exit 2 like --effort.
// Format only: a real but unsupported country is the server's call, not ours.
func TestGeoValidationExitsTwo(t *testing.T) {
	commands := map[string]func() *cobra.Command{
		"extract markdown": newExtractMarkdownCmd,
		"extract json":     newExtractJSONCmd,
		"generate json":    newGenerateJSONCmd,
		"agent automate":   newAutomateCmd,
	}

	// Values that must be rejected before any request is made. Blank is
	// deliberately absent: `--geo ""` is the ordinary shell idiom for an
	// optional flag value, and geoTarget() already omits the field entirely
	// when it is blank, so it means "no geotargeting" rather than "invalid".
	bad := []string{"England", "GBR", "G", "G1", "12", "g b", "🇬🇧"}

	for name, ctor := range commands {
		for _, geo := range bad {
			t.Run(name+"/"+geo, func(t *testing.T) {
				isolate(t)
				called := false
				setTestAppWithClient(t, mockClientFunc(func(*http.Request) (*http.Response, error) {
					called = true
					return nil, errors.New("should not be reached")
				}))

				cmd := ctor()
				// Satisfy each command's other required flags.
				if f := cmd.Flags().Lookup("schema"); f != nil {
					_ = cmd.Flags().Set("schema", `{"type":"object"}`)
				}
				if f := cmd.Flags().Lookup("instructions"); f != nil {
					_ = cmd.Flags().Set("instructions", "do a thing")
				}
				if err := cmd.Flags().Set("geo", geo); err != nil {
					t.Fatal(err)
				}

				arg := "https://example.com"
				if name == "agent automate" {
					arg = "do something"
				}
				err := cmd.RunE(cmd, []string{arg})
				if got := codeOf(err); got != 2 {
					t.Errorf("exit code = %d, want 2 (err: %v)", got, err)
				}
				if called {
					t.Error("a request was made despite invalid --geo")
				}
			})
		}
	}
}

// TestBlankGeoMeansUnset pins the counterpart to TestGeoValidationExitsTwo:
// a blank value is not an error, it is the absence of geotargeting, so
// `--geo "$COUNTRY"` with an unset variable keeps working.
func TestBlankGeoMeansUnset(t *testing.T) {
	for _, blank := range []string{"", "   ", "\t"} {
		if err := validGeo(blank); err != nil {
			t.Errorf("validGeo(%q) = %v, want nil", blank, err)
		}
		if got := geoTarget(blank); got != nil {
			t.Errorf("geoTarget(%q) = %+v, want nil so the field is omitted", blank, got)
		}
	}
}
