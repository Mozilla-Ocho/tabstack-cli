package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/fang"

	"github.com/Mozilla-Ocho/tabstack-cli/cmd"
)

// TestErrorHandlerRendersCancellationPlainly: Ctrl-C is the user getting what
// they asked for, so it gets a bare line rather than fang's red error block.
// Everything else still goes through the default rendering.
func TestErrorHandlerRendersCancellationPlainly(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantExact string // cancellation is rendered as exactly this and nothing else
		wantHas   string
	}{
		{name: "cancellation", err: cmd.ErrInterrupted, wantExact: "cancelled\n"},
		{name: "wrapped cancellation", err: fmt.Errorf("stream: %w", cmd.ErrInterrupted), wantExact: "cancelled\n"},
		{name: "anything else goes to the default handler", err: errors.New("boom"), wantHas: "boom"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			errorHandler(&buf, fang.Styles{}, tc.err)
			got := buf.String()

			if tc.wantExact != "" {
				if got != tc.wantExact {
					t.Errorf("got %q, want exactly %q", got, tc.wantExact)
				}
				return
			}
			if !strings.Contains(got, tc.wantHas) {
				t.Errorf("got %q, want it to contain %q", got, tc.wantHas)
			}
			if got == "cancelled\n" {
				t.Error("an ordinary error was rendered as a cancellation")
			}
		})
	}
}

// buildCLI compiles the binary once so the exit-code table can exercise the
// real process, which is the only place the contract actually holds.
func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tabstack")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// TestExitCodes is the contract, asserted end to end.
//
// These used to be recognised by string-matching Cobra's message prefixes in
// isCobraUsageError, which made a documented public behaviour hostage to
// Cobra's wording. Every one of them now carries its code on the error, so
// this table is what proves the string matching is genuinely gone.
func TestExitCodes(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir() // empty config, so nothing depends on the developer's machine

	cases := []struct {
		name string
		args []string
		want int
	}{
		// Usage errors, all exit 2.
		{"too few args", []string{"extract", "markdown"}, 2},
		{"too many args", []string{"schema", "path", "a", "b"}, 2},
		{"too many args, optional positional", []string{"auth", "switch", "a", "b"}, 2},
		{"no args accepted", []string{"schema", "list", "stray"}, 2},
		{"unknown command", []string{"extrct"}, 2},
		{"unknown subcommand of a group", []string{"extract", "nope"}, 2},
		{"unknown subcommand of auth", []string{"auth", "nope"}, 2},
		{"unknown flag", []string{"schema", "list", "--bogus"}, 2},
		{"unknown shorthand flag", []string{"schema", "list", "-Z"}, 2},
		{"bad typed flag value", []string{"extract", "markdown", "https://a.com", "--timeout", "notaduration"}, 2},
		{"bad int flag value", []string{"extract", "markdown", "https://a.com", "--concurrency", "notanint"}, 2},
		{"bad enum flag value", []string{"extract", "markdown", "https://a.com", "--effort", "turbo"}, 2},
		{"malformed url", []string{"extract", "markdown", "not-a-url"}, 2},
		{"missing credential", []string{"extract", "markdown", "https://a.com"}, 2},

		// Help and version are not failures.
		{"no args prints help", nil, 0},
		{"group with no args prints help", []string{"extract"}, 0},
		{"help flag", []string{"--help"}, 0},
		{"group help flag", []string{"extract", "markdown", "--help"}, 0},
		{"version", []string{"--version"}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := exec.CommandContext(context.Background(), bin, tc.args...)
			c.Env = append(os.Environ(),
				"XDG_CONFIG_HOME="+home,
				"TABSTACK_API_KEY=", // force the unauthenticated path
				"NO_COLOR=1",
			)
			out, err := c.CombinedOutput()

			got := 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				got = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("running the binary failed: %v", err)
			}

			if got != tc.want {
				t.Errorf("exit code = %d, want %d\nargs: %v\noutput:\n%s", got, tc.want, tc.args, out)
			}
		})
	}
}

// TestUsageErrorsExplainThemselves: exiting 2 is only half the job. A usage
// error has to say what was wrong, or the code is all the user gets.
func TestUsageErrorsExplainThemselves(t *testing.T) {
	bin := buildCLI(t)
	home := t.TempDir()

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"extract", "markdown"}, "<url>"},
		{[]string{"schema", "list", "stray"}, "takes no arguments"},
		{[]string{"extrct"}, "unknown command"},
		{[]string{"extract", "nope"}, "unknown command"},
		{[]string{"schema", "list", "--bogus"}, "unknown flag"},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			c := exec.Command(bin, tc.args...)
			c.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home, "NO_COLOR=1")
			out, _ := c.CombinedOutput()
			if !strings.Contains(strings.ToLower(string(out)), tc.want) {
				t.Errorf("output does not mention %q:\n%s", tc.want, out)
			}
		})
	}
}
