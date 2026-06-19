package schemas

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Schemas) != 0 {
		t.Fatalf("fresh manifest not empty: %v", m.Schemas)
	}

	m.Set("jobs/job-posting.json", "abc123", "2026-06-19T00:00:00Z")
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}

	got, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := got.Schemas["jobs/job-posting.json"]
	if !ok {
		t.Fatal("entry not persisted")
	}
	if e.SHA256 != "abc123" || e.PulledAt != "2026-06-19T00:00:00Z" {
		t.Errorf("entry = %+v", e)
	}

	got.Remove("jobs/job-posting.json")
	if _, ok := got.Schemas["jobs/job-posting.json"]; ok {
		t.Error("entry not removed")
	}
}

func TestCanonicalSHAStableAcrossFormatting(t *testing.T) {
	a := CanonicalSHA([]byte("{\n  \"a\": 1,\n  \"b\": 2\n}"))
	b := CanonicalSHA([]byte(`{"b":2,"a":1}`))
	if a != b {
		t.Errorf("formatting changed hash: %s != %s", a, b)
	}
	if c := CanonicalSHA([]byte(`{"a":2}`)); c == a {
		t.Error("different content produced same hash")
	}
}

func TestCanonicalSHANonJSONFallback(t *testing.T) {
	// Non-JSON input falls back to hashing raw bytes; equal bytes -> equal hash,
	// different bytes -> different hash.
	a := CanonicalSHA([]byte("not json"))
	aAgain := CanonicalSHA([]byte("not json"))
	b := CanonicalSHA([]byte("other"))
	if a != aAgain {
		t.Error("same non-json bytes produced different hashes")
	}
	if a == b {
		t.Error("different non-json bytes produced same hash")
	}
}

func TestLoadManifestCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(dir); err == nil {
		t.Error("expected error loading corrupt manifest")
	}
}

func TestLoadManifestMissing(t *testing.T) {
	m, err := LoadManifest(t.TempDir())
	if err != nil {
		t.Fatalf("missing manifest should not error: %v", err)
	}
	if m.Schemas == nil || len(m.Schemas) != 0 {
		t.Errorf("want empty initialised map, got %v", m.Schemas)
	}
}
