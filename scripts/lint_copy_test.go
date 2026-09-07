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

// gitEnv isolates a fixture repo from the contributor's own git setup. Without
// it the throwaway repos inherit ~/.gitconfig, where commit.gpgsign (fails, or
// hangs on a passphrase prompt), core.hooksPath (a pre-commit framework), or
// grep.column (shifts git grep's record fields) would decide the result of a
// test that is not about any of them.
func gitEnv(extra ...string) []string {
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_ATTR_NOSYSTEM=1",
	)
	return append(env, extra...)
}

// git runs one git command in dir with the isolated environment.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// installScript lays the script out at scripts/lint-copy.sh in dir, the layout
// the script assumes when it cds to its own parent.
func installScript(t *testing.T, dir string) {
	t.Helper()
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
}

// newRepo makes a git repo in a temp dir with the script installed at
// scripts/lint-copy.sh.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	installScript(t, dir)
	git(t, dir, "-c", "init.defaultBranch=main", "init", "-q", ".")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "test")
	return dir
}

// write creates a fixture file, making any parent directories, so nested
// pathspec behaviour (docs/README.md) can be covered.
func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "add", "-A")
	// --no-gpg-sign and --no-verify belt-and-brace the config isolation: a
	// signing key or a hook must not decide whether the lint gets to run.
	git(t, dir, "commit", "-qm", "fixture", "--no-gpg-sign", "--no-verify")
}

// run executes the script in dir and returns its exit code and combined output.
func run(t *testing.T, dir string, env ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("./scripts/lint-copy.sh")
	cmd.Dir = dir
	cmd.Env = gitEnv(env...)
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
			// The URL refinement lives in the Go branch of strip_allowed, so
			// this has to be a .go file to exercise it: on a .md path only the
			// <!-- --> branch is consulted and the line fails either way.
			name:    "a URL is not a comment leader",
			file:    "help.go",
			content: "package p\n\nconst u = \"see https://x.dev for " + bannedTerm + " tips, lint-copy: allow\"\n",
			commit:  true,
			want:    exitViolation,
		},
		{
			name:    "a nested doc is in scope",
			file:    "docs/README.md",
			content: "a " + emDash + " b\n",
			commit:  true,
			want:    exitViolation,
		},
		{
			name:    "nested Go source is in scope",
			file:    "internal/sub/x.go",
			content: "package sub\n\nconst use = \"" + bannedTerm + " a page\"\n",
			commit:  true,
			want:    exitViolation,
		},
		{
			name:    "a nested untracked doc is in scope",
			file:    "docs/new/NOTES.md",
			content: "new doc with " + bannedTerm + "\n",
			commit:  false,
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
	installScript(t, dir)
	write(t, dir, "README.md", "a "+emDash+" b with "+bannedTerm+"\n")

	code, out := run(t, dir)
	if code != exitError {
		t.Fatalf("exit = %d, want %d\n%s", code, exitError, out)
	}
	if strings.Contains(out, "lint-copy: clean") {
		t.Fatalf("git failure reported as clean:\n%s", out)
	}
}

// TestLintCopyUnreadableFileFailsClosed covers the per-file half of failing
// closed: git grep reports an unreadable path on stderr but still exits 1, the
// same code as "no match", so the scan must key on stderr as well as the code.
func TestLintCopyUnreadableFileFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	dir := newRepo(t)
	write(t, dir, "README.md", "clean\n")
	commitAll(t, dir)
	write(t, dir, "secret.md", "upstream "+bannedTerm+"\n")
	if err := os.Chmod(filepath.Join(dir, "secret.md"), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "secret.md"), 0o644) })

	code, out := run(t, dir)
	if code != exitError {
		t.Fatalf("exit = %d, want %d\n%s", code, exitError, out)
	}
	if strings.Contains(out, "lint-copy: clean") {
		t.Fatalf("unreadable file reported as clean:\n%s", out)
	}
}

// TestLintCopyIgnoresGrepColumnConfig pins --no-column: with grep.column set,
// git grep -n emits "path:line:col:content", which shifts awk's fields and put
// a colon in front of a column-0 "//" so the Go escape hatch stops matching.
func TestLintCopyIgnoresGrepColumnConfig(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "help.go", "package p\n\n// fine "+emDash+" really, lint-copy: allow\n")
	commitAll(t, dir)

	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[grep]\n\tcolumn = true\n\tlineNumber = true\n"), 0o644); err != nil {
		t.Fatalf("write gitconfig: %v", err)
	}

	code, out := run(t, dir, "GIT_CONFIG_GLOBAL="+cfg)
	if code != exitClean {
		t.Fatalf("exit = %d, want %d\n%s", code, exitClean, out)
	}
}
