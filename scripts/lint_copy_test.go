// Package scripts holds tests for the repo's shell tooling. It has no Go source
// of its own: lint-copy.sh is a CI gate with non-obvious escape-hatch and
// scope semantics, so it is covered by the same `go test ./...` run as
// everything else rather than by hand.
package scripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// exitClean, exitViolation, and exitError are the script's three outcomes: no
// findings, findings, and a git-level failure (which must never look clean).
const (
	exitClean     = 0
	exitViolation = 1
	exitError     = 2
)

// Spelled as escapes, not literals, so this file does not trip the dash rule it
// exercises when the lint scans *.go.
const (
	emDash        = "\u2014"
	horizontalBar = "\u2015"
	enDash        = "\u2013"
)

// bannedTerm is spelled indirectly so this test file does not trip the very rule
// it exercises when the lint scans *.go.
var bannedTerm = "scrap" + "ing"

func scriptPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("lint-copy.sh")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("stat script: %v", err)
	}
	return p
}

// newRepo makes a git repo in a temp dir with the script installed at
// scripts/lint-copy.sh, mirroring the layout the script assumes.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src, err := os.ReadFile(scriptPath(t))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "lint-copy.sh"), src, 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// run executes the script in dir and returns its exit code and combined output.
func run(t *testing.T, dir string) (int, string) {
	t.Helper()
	cmd := exec.Command("./scripts/lint-copy.sh")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run script: %v: %s", err, out)
	}
	return exitErr.ExitCode(), string(out)
}

func TestLintCopy(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		commit  bool
		want    int
	}{
		{
			name:    "clean prose passes",
			file:    "README.md",
			content: "A plain sentence, with a comma.\n",
			commit:  true,
			want:    exitClean,
		},
		{
			name:    "em dash in a linted doc fails",
			file:    "README.md",
			content: "a " + emDash + " b\n",
			commit:  true,
			want:    exitViolation,
		},
		{
			name:    "horizontal bar fails like an em dash",
			file:    "README.md",
			content: "a " + horizontalBar + " b\n",
			commit:  true,
			want:    exitViolation,
		},
		{
			name:    "en dash in a numeric range passes",
			file:    "README.md",
			content: "Latency is ~1" + enDash + "5s.\n",
			commit:  true,
			want:    exitClean,
		},
		{
			name:    "em dash in Go source fails",
			file:    "help.go",
			content: "package p\n\n// help text " + emDash + " here\n",
			commit:  true,
			want:    exitViolation,
		},
		{
			name:    "banned term in Go source fails",
			file:    "help.go",
			content: "package p\n\nconst use = \"" + bannedTerm + " a page\"\n",
			commit:  true,
			want:    exitViolation,
		},
		{
			name:    "banned term in any doc fails",
			file:    "SECURITY.md",
			content: "we do no " + bannedTerm + " here\n",
			commit:  true,
			want:    exitViolation,
		},
		{
			name:    "em dash outside the dash pathspec passes",
			file:    "SECURITY.md",
			content: "a " + emDash + " b\n",
			commit:  true,
			want:    exitClean,
		},
		{
			name:    "untracked file is linted before it is added",
			file:    "NEWDOC.md",
			content: "new doc with " + bannedTerm + "\n",
			commit:  false,
			want:    exitViolation,
		},
		{
			name:    "markdown comment marker exempts the line",
			file:    "README.md",
			content: "a " + emDash + " b <!-- lint-copy: allow -->\n",
			commit:  true,
			want:    exitClean,
		},
		{
			name:    "go comment marker exempts the line",
			file:    "help.go",
			content: "package p\n\nconst a = \"x\" // fine " + emDash + " really, lint-copy: allow\n",
			commit:  true,
			want:    exitClean,
		},
		{
			name:    "a URL is not a comment leader",
			file:    "README.md",
			content: "see https://x.dev for " + bannedTerm + " tips, lint-copy: allow\n",
			commit:  true,
			want:    exitViolation,
		},
		{
			name:    "a go marker does not exempt a markdown line",
			file:    "README.md",
			content: "a " + emDash + " b // lint-copy: allow\n",
			commit:  true,
			want:    exitViolation,
		},
		{
			name:    "a markdown marker outside a go comment does not exempt",
			file:    "help.go",
			content: "package p\n\nconst a = \"text " + emDash + " here\" <!-- lint-copy: allow -->\n",
			commit:  true,
			want:    exitViolation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newRepo(t)
			write(t, dir, tt.file, tt.content)
			if tt.commit {
				commitAll(t, dir)
			}
			code, out := run(t, dir)
			if code != tt.want {
				t.Fatalf("exit = %d, want %d\n%s", code, tt.want, out)
			}
			if code == exitClean && !strings.Contains(out, "lint-copy: clean") {
				t.Fatalf("clean run did not report clean:\n%s", out)
			}
		})
	}
}

// TestLintCopyIgnoresIgnoredFiles keeps the untracked scan from reaching into
// build output or vendored trees.
func TestLintCopyIgnoresIgnoredFiles(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, ".gitignore", "vendored.md\n")
	write(t, dir, "README.md", "clean\n")
	commitAll(t, dir)
	write(t, dir, "vendored.md", "upstream "+bannedTerm+"\n")

	if code, out := run(t, dir); code != exitClean {
		t.Fatalf("exit = %d, want %d\n%s", code, exitClean, out)
	}
}

// TestLintCopyFailsClosed is the important one: a git-level failure must not be
// reported as clean. Running outside a repo stands in for any git error (dubious
// ownership, a broken checkout, git missing).
func TestLintCopyFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src, err := os.ReadFile(scriptPath(t))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "lint-copy.sh"), src, 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	write(t, dir, "README.md", "a "+emDash+" b with "+bannedTerm+"\n")

	code, out := run(t, dir)
	if code != exitError {
		t.Fatalf("exit = %d, want %d\n%s", code, exitError, out)
	}
	if strings.Contains(out, "lint-copy: clean") {
		t.Fatalf("git failure reported as clean:\n%s", out)
	}
}
