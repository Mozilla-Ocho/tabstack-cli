package schemas

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"title":"Job Posting"}`)

	if err := Write(dir, "jobs/job-posting.json", data); err != nil {
		t.Fatal(err)
	}

	// File lands at the mirrored repo path.
	if _, err := os.Stat(filepath.Join(dir, "jobs", "job-posting.json")); err != nil {
		t.Fatalf("expected file at mirrored path: %v", err)
	}

	got, exists, err := Read(dir, "jobs/job-posting.json")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("exists = false, want true")
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestWriteParentIsFile(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file where a category directory is expected; MkdirAll
	// under it must fail.
	if err := os.WriteFile(filepath.Join(dir, "jobs"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, "jobs/job-posting.json", []byte(`{}`)); err == nil {
		t.Error("expected error writing under a file")
	}
}

func TestReadDirectoryIsError(t *testing.T) {
	dir := t.TempDir()
	// A directory at the schema path is neither missing nor readable as a file.
	if err := os.MkdirAll(LocalPath(dir, "a/x.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read(dir, "a/x.json"); err == nil {
		t.Error("expected error reading a directory as a schema")
	}
}

func TestReadMissing(t *testing.T) {
	_, exists, err := Read(t.TempDir(), "nope.json")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if exists {
		t.Error("exists = true, want false")
	}
}

func TestFindLocal(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "jobs/job-posting.json")
	mustWrite(t, dir, "jobs/layoff-event.json")
	mustWrite(t, dir, "finance/crypto-asset.json")

	cases := []struct {
		sel     string
		want    string
		wantErr bool
	}{
		{"job-posting", "jobs/job-posting.json", false},
		{"job-posting.json", "jobs/job-posting.json", false},
		{"jobs/job-posting.json", "jobs/job-posting.json", false},
		{"jobs/job-posting", "jobs/job-posting.json", false},
		{"crypto-asset", "finance/crypto-asset.json", false},
		{"", "", true},
		{"missing", "", true},
		{"jobs/missing.json", "", true},
	}
	for _, tc := range cases {
		got, err := FindLocal(dir, tc.sel)
		if tc.wantErr {
			if err == nil {
				t.Errorf("FindLocal(%q): expected error", tc.sel)
			}
			continue
		}
		if err != nil {
			t.Errorf("FindLocal(%q): %v", tc.sel, err)
			continue
		}
		if got != tc.want {
			t.Errorf("FindLocal(%q) = %q, want %q", tc.sel, got, tc.want)
		}
	}
}

func TestListLocalSkipsHidden(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "jobs/job-posting.json")
	mustWrite(t, dir, "finance/crypto-asset.json")
	// Bookkeeping files must not appear as schemas.
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, IndexCacheName), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"finance/crypto-asset.json", "jobs/job-posting.json"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestListLocalMissingDir(t *testing.T) {
	got, err := ListLocal(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestFindLocalAmbiguous(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "a/news-article.json")
	mustWrite(t, dir, "b/news-article.json")
	if _, err := FindLocal(dir, "news-article"); err == nil {
		t.Error("expected ambiguity error")
	}
}

func TestFindLocalEmptyStore(t *testing.T) {
	// A missing store directory should yield a helpful "pull one" error, not a
	// filesystem error.
	if _, err := FindLocal(filepath.Join(t.TempDir(), "nope"), "job-posting"); err == nil {
		t.Error("expected error for empty store")
	}
}

func mustWrite(t *testing.T, dir, rel string) {
	t.Helper()
	if err := Write(dir, rel, []byte(`{"type":"object"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", `{"a":1}`, `{"a":1}`, true},
		{"formatting only", "{\n  \"a\": 1,\n  \"b\": 2\n}", `{"b":2,"a":1}`, true},
		{"different", `{"a":1}`, `{"a":2}`, false},
		{"non-json falls back to bytes", "not json", "not json", true},
		{"non-json differs", "not json", "other", false},
	}
	for _, tc := range cases {
		if got := Equal([]byte(tc.a), []byte(tc.b)); got != tc.want {
			t.Errorf("%s: Equal = %v, want %v", tc.name, got, tc.want)
		}
	}
}
