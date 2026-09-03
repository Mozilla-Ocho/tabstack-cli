package cmd

import (
	"strconv"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/config"
)

func intPtr(v int) *int { return &v }

// testCmd builds a command carrying the flags project config can feed, with
// the same defaults the real ones have. Those non-zero defaults are the reason
// applyProjectConfig has to consult Changed() rather than checking for a zero
// value.
func testCmd() *cobra.Command {
	c := &cobra.Command{Use: "x"}
	f := c.Flags()
	f.String("storage", "", "")
	f.String("output", "", "")
	f.String("effort", "", "")
	f.String("geo", "", "")
	f.Duration("timeout", 2*time.Minute, "")
	f.Duration("max-duration", 0, "")
	f.Int("concurrency", 4, "")
	f.Int("retries", 2, "")
	return c
}

// TestApplyProjectConfigFillsUnsetFlags is the crux of the feature: without
// the Changed() check the flags' own defaults would always win and project
// config would silently never apply.
func TestApplyProjectConfigFillsUnsetFlags(t *testing.T) {
	c := testCmd()
	pc := &config.ProjectConfig{
		Path:        "/repo/.tabstack.toml",
		Storage:     "/repo/schemas",
		Output:      "json",
		Effort:      "max",
		Geo:         "GB",
		Timeout:     "45s",
		MaxDuration: "10m",
		Concurrency: intPtr(8),
		Retries:     intPtr(0),
	}
	if err := applyProjectConfig(c, pc); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"storage":      "/repo/schemas",
		"output":       "json",
		"effort":       "max",
		"geo":          "GB",
		"timeout":      "45s",
		"max-duration": "10m0s", // pflag canonicalises the duration
		"concurrency":  "8",
		"retries":      "0", // an explicit zero must land, not be read as unset
	}
	for name, exp := range want {
		if got := c.Flags().Lookup(name).Value.String(); got != exp {
			t.Errorf("--%s = %q, want %q", name, got, exp)
		}
	}
}

// TestApplyProjectConfigNeverBeatsAnExplicitFlag pins the precedence rung:
// flags outrank project config.
func TestApplyProjectConfigNeverBeatsAnExplicitFlag(t *testing.T) {
	c := testCmd()
	if err := c.Flags().Parse([]string{
		"--effort", "min", "--concurrency", "1", "--timeout", "5s", "--retries", "7",
	}); err != nil {
		t.Fatal(err)
	}

	pc := &config.ProjectConfig{
		Path:        "/repo/.tabstack.toml",
		Effort:      "max",
		Timeout:     "45s",
		Concurrency: intPtr(8),
		Retries:     intPtr(0),
	}
	if err := applyProjectConfig(c, pc); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"effort": "min", "concurrency": "1", "timeout": "5s", "retries": "7"}
	for name, exp := range want {
		if got := c.Flags().Lookup(name).Value.String(); got != exp {
			t.Errorf("--%s = %q, want the explicit %q to win", name, got, exp)
		}
	}
}

// TestApplyProjectConfigIgnoresFlagsACommandLacks: a project pinning
// --concurrency must not break `auth status`, which has no such flag.
func TestApplyProjectConfigIgnoresFlagsACommandLacks(t *testing.T) {
	bare := &cobra.Command{Use: "auth"}
	bare.Flags().String("output", "", "")

	pc := &config.ProjectConfig{
		Path:        "/repo/.tabstack.toml",
		Output:      "json",
		Concurrency: intPtr(8),
		Effort:      "max",
	}
	if err := applyProjectConfig(bare, pc); err != nil {
		t.Fatalf("a setting the command lacks should be skipped, got: %v", err)
	}
	if got := bare.Flags().Lookup("output").Value.String(); got != "json" {
		t.Errorf("--output = %q, want the setting it does have to apply", got)
	}
}

// TestApplyProjectConfigRejectsBadValues: a value that the flag's own parser
// refuses fails with exit 2 and names the file, rather than being dropped.
func TestApplyProjectConfigRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		pc   *config.ProjectConfig
	}{
		{"a bad duration", &config.ProjectConfig{Path: "/repo/.tabstack.toml", Timeout: "not-a-duration"}},
		{"a bad max-duration", &config.ProjectConfig{Path: "/repo/.tabstack.toml", MaxDuration: "soon"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := applyProjectConfig(testCmd(), tc.pc)
			if codeOf(err) != 2 {
				t.Fatalf("exit code = %d, want 2 (err: %v)", codeOf(err), err)
			}
			if got := err.Error(); got[:len("project config")] != "project config" {
				t.Errorf("message should lead with a plain word so fang does not title-case the path: %q", got)
			}
		})
	}
}

func TestApplyProjectConfigNil(t *testing.T) {
	c := testCmd()
	if err := applyProjectConfig(c, nil); err != nil {
		t.Fatal(err)
	}
	if got := c.Flags().Lookup("concurrency").Value.String(); got != strconv.Itoa(4) {
		t.Errorf("concurrency = %q, want the default untouched", got)
	}
}
