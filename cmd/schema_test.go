package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/schemas"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// cmdWithStorage builds a bare command exposing a --storage flag set to dir, for
// exercising completion functions that read it.
func cmdWithStorage(dir string) *cobra.Command {
	c := &cobra.Command{Use: "x"}
	c.Flags().String("storage", "", "")
	_ = c.Flags().Set("storage", dir)
	return c
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// setTestApp installs a rootApp with a buffer-backed JSON renderer and restores
// the previous one on cleanup. Returns the buffer command output is written to.
func setTestApp(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := rootApp
	rootApp = &app{renderer: ui.Renderer{
		Out:    buf,
		Err:    buf,
		Mode:   ui.ModeJSON,
		Styles: ui.NewStyles(true),
	}}
	t.Cleanup(func() { rootApp = prev })
	return buf
}

// serveBodies returns a fetcher backed by a test server that serves the given
// repo-relative path -> body map.
func serveBodies(t *testing.T, bodies map[string]string) *schemas.Fetcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := bodies[strings.TrimPrefix(r.URL.Path, "/")]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return schemas.NewFetcher(schemas.WithRawBase(srv.URL), schemas.WithHTTPClient(srv.Client()))
}

// codeOf extracts the exit code from a coded error, or -1 if absent.
func codeOf(err error) int {
	var e *exitErr
	if errors.As(err, &e) {
		return e.Code()
	}
	return -1
}

func TestSelectTargetsAll(t *testing.T) {
	idx := schemas.Index{Schemas: []schemas.Entry{
		{Path: "a/one.json"}, {Path: "b/two.json"},
	}}
	got, err := selectTargets(idx, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
}

func TestSelectTargetsDedup(t *testing.T) {
	idx := schemas.Index{Schemas: []schemas.Entry{
		{Category: "jobs", Path: "jobs/job-posting.json"},
		{Category: "jobs", Path: "jobs/layoff-event.json"},
	}}
	// "jobs" (category) overlaps with the explicit path: result must dedup.
	got, err := selectTargets(idx, []string{"jobs", "jobs/job-posting.json"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d entries, want 2 (deduped)", len(got))
	}
}

func TestSelectTargetsUnknown(t *testing.T) {
	idx := schemas.Index{Schemas: []schemas.Entry{{Path: "a/one.json"}}}
	if _, err := selectTargets(idx, []string{"nope"}, false); err == nil {
		t.Error("expected error for unknown selector")
	}
}

func TestPromptChoiceNonTerminal(t *testing.T) {
	// In `go test` stdin is not a TTY, so promptChoice must report errNotTerminal
	// rather than block on input.
	if _, err := promptChoice("? ", "okq", 'k'); err != errNotTerminal {
		t.Errorf("err = %v, want errNotTerminal", err)
	}
}

const jpPath = "jobs/job-posting.json"

func TestRunPullFresh(t *testing.T) {
	setTestApp(t)
	dir := t.TempDir()
	remote := `{"title":"Job Posting"}`
	f := serveBodies(t, map[string]string{jpPath: remote})
	targets := []schemas.Entry{{Category: "jobs", Path: jpPath}}

	if err := runPull(context.Background(), f, dir, targets, false); err != nil {
		t.Fatalf("runPull: %v", err)
	}

	data, exists, err := schemas.Read(dir, jpPath)
	if err != nil || !exists {
		t.Fatalf("schema not written (exists=%v, err=%v)", exists, err)
	}
	if !schemas.Equal(data, []byte(remote)) {
		t.Errorf("written content = %q, want %q", data, remote)
	}
	m, _ := schemas.LoadManifest(dir)
	if _, ok := m.Schemas[jpPath]; !ok {
		t.Error("manifest entry not recorded")
	}
}

func TestRunPullConflictNonTerminal(t *testing.T) {
	setTestApp(t)
	dir := t.TempDir()
	mine := `{"title":"my edit"}`
	if err := schemas.Write(dir, jpPath, []byte(mine)); err != nil {
		t.Fatal(err)
	}
	f := serveBodies(t, map[string]string{jpPath: `{"title":"upstream"}`})
	targets := []schemas.Entry{{Category: "jobs", Path: jpPath}}

	// stdin is not a TTY in tests, so a conflict must fail with exit 2 rather
	// than clobber the local edit.
	err := runPull(context.Background(), f, dir, targets, false)
	if codeOf(err) != 2 {
		t.Fatalf("err = %v, want exit code 2", err)
	}
	data, _, _ := schemas.Read(dir, jpPath)
	if !schemas.Equal(data, []byte(mine)) {
		t.Errorf("local edit was overwritten: %q", data)
	}
}

func TestRunPullForceOverwrites(t *testing.T) {
	setTestApp(t)
	dir := t.TempDir()
	if err := schemas.Write(dir, jpPath, []byte(`{"title":"my edit"}`)); err != nil {
		t.Fatal(err)
	}
	remote := `{"title":"upstream"}`
	f := serveBodies(t, map[string]string{jpPath: remote})
	targets := []schemas.Entry{{Category: "jobs", Path: jpPath}}

	if err := runPull(context.Background(), f, dir, targets, true); err != nil {
		t.Fatalf("runPull --force: %v", err)
	}
	data, _, _ := schemas.Read(dir, jpPath)
	if !schemas.Equal(data, []byte(remote)) {
		t.Errorf("content = %q, want remote %q", data, remote)
	}
	m, _ := schemas.LoadManifest(dir)
	if m.Schemas[jpPath].SHA256 != schemas.CanonicalSHA([]byte(remote)) {
		t.Error("manifest sha not updated to remote")
	}
}

// statusMap runs computeStatus and returns a path->state map.
func statusMap(t *testing.T, dir string, f *schemas.Fetcher) map[string]string {
	t.Helper()
	rows, err := computeStatus(context.Background(), dir, f)
	if err != nil {
		t.Fatalf("computeStatus: %v", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Path] = r.State
	}
	return out
}

func TestComputeStatusLocal(t *testing.T) {
	dir := t.TempDir()
	clean := `{"title":"clean"}`

	if err := schemas.Write(dir, "a/clean.json", []byte(clean)); err != nil {
		t.Fatal(err)
	}
	if err := schemas.Write(dir, "a/modified.json", []byte(`{"title":"edited"}`)); err != nil {
		t.Fatal(err)
	}
	if err := schemas.Write(dir, "a/untracked.json", []byte(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	m, _ := schemas.LoadManifest(dir)
	m.Set("a/clean.json", schemas.CanonicalSHA([]byte(clean)), "t")
	m.Set("a/modified.json", schemas.CanonicalSHA([]byte(`{"title":"orig"}`)), "t")
	m.Set("a/missing.json", "deadbeef", "t") // tracked but no file
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}

	// nil fetcher => local-only, no network, never "outdated".
	got := statusMap(t, dir, nil)
	want := map[string]string{
		"a/clean.json":     "up to date",
		"a/modified.json":  "modified",
		"a/untracked.json": "untracked",
		"a/missing.json":   "missing",
	}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("status[%s] = %q, want %q", p, got[p], w)
		}
	}
}

func TestComputeStatusOutdated(t *testing.T) {
	dir := t.TempDir()
	local := `{"title":"v1"}`
	if err := schemas.Write(dir, jpPath, []byte(local)); err != nil {
		t.Fatal(err)
	}
	m, _ := schemas.LoadManifest(dir)
	// Recorded hash matches the local file (so not modified) but the remote will
	// differ, so it must read as outdated.
	m.Set(jpPath, schemas.CanonicalSHA([]byte(local)), "t")
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}

	f := serveBodies(t, map[string]string{jpPath: `{"title":"v2"}`})
	if got := statusMap(t, dir, f)[jpPath]; got != "outdated" {
		t.Errorf("status = %q, want outdated", got)
	}
}

func TestComputeStatusModifiedAndOutdated(t *testing.T) {
	dir := t.TempDir()
	if err := schemas.Write(dir, jpPath, []byte(`{"title":"my edit"}`)); err != nil {
		t.Fatal(err)
	}
	m, _ := schemas.LoadManifest(dir)
	m.Set(jpPath, "recordedhash", "t") // matches neither local nor remote
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}

	f := serveBodies(t, map[string]string{jpPath: `{"title":"upstream"}`})
	if got := statusMap(t, dir, f)[jpPath]; got != "modified, outdated" {
		t.Errorf("status = %q, want \"modified, outdated\"", got)
	}
}

func TestComputeStatusRemoteUnknown(t *testing.T) {
	dir := t.TempDir()
	local := `{"title":"v1"}`
	if err := schemas.Write(dir, jpPath, []byte(local)); err != nil {
		t.Fatal(err)
	}
	m, _ := schemas.LoadManifest(dir)
	m.Set(jpPath, schemas.CanonicalSHA([]byte(local)), "t")
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}

	// Fetcher serves no bodies, so the remote check 404s. A failed check must
	// read as "remote unknown", not be silently swallowed into "up to date".
	f := serveBodies(t, nil)
	if got := statusMap(t, dir, f)[jpPath]; got != "remote unknown" {
		t.Errorf("status = %q, want \"remote unknown\"", got)
	}
}

func TestStatusLabel(t *testing.T) {
	cases := []struct {
		mod, out, unknown bool
		want              string
	}{
		{false, false, false, "up to date"},
		{true, false, false, "modified"},
		{false, true, false, "outdated"},
		{true, true, false, "modified, outdated"},
		{false, false, true, "remote unknown"},
		{true, false, true, "modified, remote unknown"},
	}
	for _, c := range cases {
		if got := statusLabel(c.mod, c.out, c.unknown); got != c.want {
			t.Errorf("statusLabel(%v,%v,%v) = %q, want %q", c.mod, c.out, c.unknown, got, c.want)
		}
	}
}

func TestBaseName(t *testing.T) {
	cases := map[string]string{
		"jobs/job-posting.json": "job-posting",
		"job-posting.json":      "job-posting",
		"job-posting":           "job-posting",
		"a/b/c.json":            "c",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveSchemaArg(t *testing.T) {
	dir := t.TempDir()
	if err := schemas.Write(dir, "jobs/job-posting.json", []byte(`{"type":"object"}`)); err != nil {
		t.Fatal(err)
	}
	if err := schemas.Write(dir, "jobs/broken.json", []byte("not json")); err != nil {
		t.Fatal(err)
	}

	t.Run("inline", func(t *testing.T) {
		raw, err := resolveSchemaArg(`{"a":1}`, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != `{"a":1}` {
			t.Errorf("raw = %s", raw)
		}
	})
	t.Run("by name", func(t *testing.T) {
		raw, err := resolveSchemaArg("", "job-posting", dir)
		if err != nil {
			t.Fatal(err)
		}
		if !schemas.Equal(raw, []byte(`{"type":"object"}`)) {
			t.Errorf("raw = %s", raw)
		}
	})
	t.Run("both set", func(t *testing.T) {
		if _, err := resolveSchemaArg(`{}`, "job-posting", dir); codeOf(err) != 2 {
			t.Errorf("err = %v, want code 2", err)
		}
	})
	t.Run("neither set", func(t *testing.T) {
		if _, err := resolveSchemaArg("", "", dir); codeOf(err) != 2 {
			t.Errorf("err = %v, want code 2", err)
		}
	})
	t.Run("unknown name", func(t *testing.T) {
		if _, err := resolveSchemaArg("", "nope", dir); codeOf(err) != 2 {
			t.Errorf("err = %v, want code 2", err)
		}
	})
	t.Run("stored not valid json", func(t *testing.T) {
		if _, err := resolveSchemaArg("", "broken", dir); codeOf(err) != 2 {
			t.Errorf("err = %v, want code 2", err)
		}
	})
}

func TestSchemaStoreDir(t *testing.T) {
	if got, _ := schemaStoreDir("/tmp/custom"); got != "/tmp/custom" {
		t.Errorf("override = %q, want /tmp/custom", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	got, err := schemaStoreDir("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/xdg/tabstack/schemas" {
		t.Errorf("default = %q, want /xdg/tabstack/schemas", got)
	}
}

func TestRunPullUpToDateBackfillsManifest(t *testing.T) {
	setTestApp(t)
	dir := t.TempDir()
	body := `{"title":"Job Posting"}`
	if err := schemas.Write(dir, jpPath, []byte(body)); err != nil {
		t.Fatal(err)
	}
	f := serveBodies(t, map[string]string{jpPath: body})
	targets := []schemas.Entry{{Category: "jobs", Path: jpPath}}

	if err := runPull(context.Background(), f, dir, targets, false); err != nil {
		t.Fatalf("runPull: %v", err)
	}
	// File was already current, but the manifest should be backfilled.
	m, _ := schemas.LoadManifest(dir)
	if m.Schemas[jpPath].SHA256 != schemas.CanonicalSHA([]byte(body)) {
		t.Error("manifest not backfilled for up-to-date schema")
	}
}

// TestRunPullRejectsTraversal drives a hostile index Entry (a path that escapes
// the store) end-to-end through runPull. The remote-supplied path must never be
// written outside the store: SafePath in schemas.Read/Write rejects it and
// runPull fails rather than clobbering a file elsewhere on disk.
func TestRunPullRejectsTraversal(t *testing.T) {
	setTestApp(t)
	parent := t.TempDir()
	store := filepath.Join(parent, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}

	hostile := "../../etc/x.json"
	// The fetch itself succeeds (the server serves any path); the escape must be
	// caught at the local write boundary, not by a lucky 404.
	f := serveBodies(t, map[string]string{
		hostile:      `{"pwned":true}`,
		"etc/x.json": `{"pwned":true}`,
	})
	targets := []schemas.Entry{{Category: "jobs", Path: hostile}}

	if err := runPull(context.Background(), f, store, targets, true); err == nil {
		t.Fatal("expected runPull to reject a store-escaping path")
	}

	// The lexical target the hostile path would resolve to (outside the store)
	// must not have been created.
	escaped := filepath.Clean(filepath.Join(store, hostile))
	if _, err := os.Stat(escaped); err == nil {
		t.Fatalf("traversal wrote a file outside the store at %s", escaped)
	}
}

func TestRunPullFetchError(t *testing.T) {
	setTestApp(t)
	dir := t.TempDir()
	f := serveBodies(t, map[string]string{}) // serves 404 for everything
	targets := []schemas.Entry{{Path: jpPath}}
	if err := runPull(context.Background(), f, dir, targets, false); err == nil {
		t.Fatal("expected fetch error")
	}
}

func TestPrintStatusJSON(t *testing.T) {
	buf := setTestApp(t)
	rows := []statusRow{{"a/x.json", "modified"}, {"b/y.json", "up to date"}}
	if err := printStatus(rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"state":"modified"`) {
		t.Errorf("json output missing state: %s", buf.String())
	}
}

func TestPrintStatusPrettyEmpty(t *testing.T) {
	buf := setTestApp(t)
	rootApp.renderer.Mode = ui.ModePretty
	if err := printStatus(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No schemas pulled") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestPrintStatusPrettyStates(t *testing.T) {
	buf := setTestApp(t)
	rootApp.renderer.Mode = ui.ModePretty
	rows := []statusRow{
		{"a.json", "up to date"},
		{"b.json", "modified"},
		{"c.json", "missing"},
		{"d.json", "untracked"},
	}
	if err := printStatus(rows); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"up to date", "modified", "missing", "untracked", "a.json"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q: %s", want, buf.String())
		}
	}
}

func TestPrintLocalList(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		buf := setTestApp(t)
		if err := printLocalList([]string{"a.json", "b.json"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), `["a.json","b.json"]`) {
			t.Errorf("output = %s", buf.String())
		}
	})
	t.Run("pretty empty", func(t *testing.T) {
		buf := setTestApp(t)
		rootApp.renderer.Mode = ui.ModePretty
		if err := printLocalList(nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "No schemas pulled") {
			t.Errorf("output = %q", buf.String())
		}
	})
	t.Run("pretty list", func(t *testing.T) {
		buf := setTestApp(t)
		rootApp.renderer.Mode = ui.ModePretty
		if err := printLocalList([]string{"jobs/job-posting.json"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "jobs/job-posting.json") || !strings.Contains(buf.String(), "1 pulled") {
			t.Errorf("output = %q", buf.String())
		}
	})
}

func TestPrintSchemaList(t *testing.T) {
	idx := schemas.Index{Schemas: []schemas.Entry{
		{Category: "jobs", Title: "Job Posting", Path: "jobs/job-posting.json"},
		{Category: "finance", Title: "Crypto Asset", Path: "finance/crypto-asset.json"},
	}}

	t.Run("json", func(t *testing.T) {
		buf := setTestApp(t)
		if err := printSchemaList(idx, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), `"path":"jobs/job-posting.json"`) {
			t.Errorf("output = %s", buf.String())
		}
	})
	t.Run("pretty marks pulled", func(t *testing.T) {
		buf := setTestApp(t)
		rootApp.renderer.Mode = ui.ModePretty
		pulled := map[string]bool{"jobs/job-posting.json": true}
		if err := printSchemaList(idx, pulled); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "✓") || !strings.Contains(out, "2 schemas, 1 pulled") {
			t.Errorf("output = %q", out)
		}
	})
}

func TestRunStatusPrints(t *testing.T) {
	buf := setTestApp(t)
	dir := t.TempDir()
	if err := schemas.Write(dir, jpPath, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	// local=true so no network is needed.
	if err := runStatus(context.Background(), dir, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "untracked") {
		t.Errorf("output = %s", buf.String())
	}
}

func TestToSet(t *testing.T) {
	s := toSet([]string{"a", "b", "a"})
	if len(s) != 2 || !s["a"] || !s["b"] {
		t.Errorf("set = %v", s)
	}
}

func TestCompleteLocalSchemaNames(t *testing.T) {
	dir := t.TempDir()
	if err := schemas.Write(dir, "jobs/job-posting.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	out, directive := completeLocalSchemaNames(cmdWithStorage(dir), nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v", directive)
	}
	if !contains(out, "job-posting") || !contains(out, "jobs/job-posting.json") {
		t.Errorf("completions = %v", out)
	}
}

const testIdxJSON = `{"count":2,"schemas":[` +
	`{"category":"jobs","title":"Job Posting","path":"jobs/job-posting.json"},` +
	`{"category":"finance","title":"Crypto Asset","path":"finance/crypto-asset.json"}]}`

func TestCompletePullSelectors(t *testing.T) {
	dir := t.TempDir()
	// Seed the index cache so completion resolves offline (it builds a real
	// fetcher, but a fresh cache short-circuits the network).
	stub := serveBodies(t, map[string]string{"index.json": testIdxJSON})
	if _, err := schemas.CachedIndex(context.Background(), stub, dir, time.Hour, false); err != nil {
		t.Fatal(err)
	}

	out, directive := completePullSelectors(cmdWithStorage(dir), nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v", directive)
	}
	for _, want := range []string{"jobs/job-posting.json", "job-posting", "jobs", "finance", "crypto-asset"} {
		if !contains(out, want) {
			t.Errorf("completions missing %q: %v", want, out)
		}
	}
}

// runRunE sets the given flags on cmd and invokes its RunE with rootApp already
// installed, bypassing the root PersistentPreRun (which would build a real,
// stdout-bound renderer). Returns the RunE error.
func runRunE(t *testing.T, cmd *cobra.Command, flags map[string]string, args ...string) error {
	t.Helper()
	for k, v := range flags {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatalf("set --%s: %v", k, err)
		}
	}
	return cmd.RunE(cmd, args)
}

func TestSchemaRmCmd(t *testing.T) {
	buf := setTestApp(t)
	dir := t.TempDir()
	if err := schemas.Write(dir, jpPath, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	m, _ := schemas.LoadManifest(dir)
	m.Set(jpPath, "sha", "t")
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}

	if err := runRunE(t, newSchemaRmCmd(), map[string]string{"storage": dir}, "job-posting"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if _, exists, _ := schemas.Read(dir, jpPath); exists {
		t.Error("file not removed")
	}
	m2, _ := schemas.LoadManifest(dir)
	if _, ok := m2.Schemas[jpPath]; ok {
		t.Error("manifest entry not removed")
	}
	if !strings.Contains(buf.String(), "removed") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestSchemaRmCmdUnknown(t *testing.T) {
	setTestApp(t)
	dir := t.TempDir()
	err := runRunE(t, newSchemaRmCmd(), map[string]string{"storage": dir}, "nope")
	if codeOf(err) != 2 {
		t.Errorf("err = %v, want code 2", err)
	}
}

func TestSchemaPathCmd(t *testing.T) {
	buf := setTestApp(t)
	dir := t.TempDir()
	if err := schemas.Write(dir, jpPath, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := runRunE(t, newSchemaPathCmd(), map[string]string{"storage": dir}, "job-posting"); err != nil {
		t.Fatalf("path: %v", err)
	}
	if !strings.Contains(buf.String(), schemas.LocalPath(dir, jpPath)) {
		t.Errorf("output = %q, want path", buf.String())
	}
}

func TestSchemaPathCmdUnknown(t *testing.T) {
	setTestApp(t)
	err := runRunE(t, newSchemaPathCmd(), map[string]string{"storage": t.TempDir()}, "nope")
	if codeOf(err) != 2 {
		t.Errorf("err = %v, want code 2", err)
	}
}

func TestSchemaListCmdLocal(t *testing.T) {
	buf := setTestApp(t)
	dir := t.TempDir()
	if err := schemas.Write(dir, jpPath, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := schemas.Write(dir, "finance/crypto-asset.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := runRunE(t, newSchemaListCmd(), map[string]string{"storage": dir, "local": "true"}); err != nil {
		t.Fatalf("list --local: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, jpPath) || !strings.Contains(out, "finance/crypto-asset.json") {
		t.Errorf("output = %q", out)
	}
}

func TestSchemaStatusCmdLocal(t *testing.T) {
	buf := setTestApp(t)
	dir := t.TempDir()
	if err := schemas.Write(dir, jpPath, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := runRunE(t, newSchemaStatusCmd(), map[string]string{"storage": dir, "local": "true"}); err != nil {
		t.Fatalf("status --local: %v", err)
	}
	if !strings.Contains(buf.String(), "untracked") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestNewSchemaCmdWiring(t *testing.T) {
	sub := map[string]bool{}
	for _, c := range newSchemaCmd().Commands() {
		sub[c.Name()] = true
	}
	for _, want := range []string{"list", "pull", "status", "path", "rm"} {
		if !sub[want] {
			t.Errorf("schema subcommand %q not registered", want)
		}
	}
}
