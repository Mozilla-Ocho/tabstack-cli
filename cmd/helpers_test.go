package cmd

import (
	"context"
	"errors"
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
