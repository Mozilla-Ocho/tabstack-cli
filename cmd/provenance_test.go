package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

func sampleSource() client.Source {
	return client.Source{
		FinalURL:     "https://www.mozilla.org/",
		OriginStatus: 200,
		RetrievedAt:  time.Date(2026, 11, 4, 17, 52, 38, 0, time.UTC),
		Checksum:     "sha256:abc",
		Cache:        "hit",
		Age:          3 * time.Second,
		HasAge:       true,
		TraceID:      "trace-1",
	}
}

func provenanceRenderer(mode ui.OutputMode) (ui.Renderer, *bytes.Buffer, *bytes.Buffer) {
	var out, errBuf bytes.Buffer
	return ui.Renderer{Out: &out, Err: &errBuf, Mode: mode, Styles: ui.NewStyles(true)}, &out, &errBuf
}

func TestProvenancePretty(t *testing.T) {
	r, out, errBuf := provenanceRenderer(ui.ModePretty)
	printProvenance(r, sampleSource())

	if out.Len() != 0 {
		t.Errorf("provenance must not touch stdout, got %q", out.String())
	}
	got := errBuf.String()
	for _, want := range []string{
		"provenance",
		"final url      https://www.mozilla.org/",
		"origin status  200",
		"retrieved      2026-11-04T17:52:38Z",
		"checksum       sha256:abc",
		"cache          hit (age 3s)",
		"trace id       trace-1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// Unknown values are left out, not printed blank.
func TestProvenancePrettyOmitsUnknown(t *testing.T) {
	r, _, errBuf := provenanceRenderer(ui.ModePretty)
	printProvenance(r, client.Source{Checksum: "sha256:abc", Cache: "miss"})

	got := errBuf.String()
	for _, absent := range []string{"final url", "origin status", "retrieved", "age", "trace id"} {
		if strings.Contains(got, absent) {
			t.Errorf("unexpected %q in:\n%s", absent, got)
		}
	}
}

func TestProvenancePrettyEmpty(t *testing.T) {
	r, _, errBuf := provenanceRenderer(ui.ModePretty)
	printProvenance(r, client.Source{})
	if !strings.Contains(errBuf.String(), "not reported by this API") {
		t.Errorf("got:\n%s", errBuf.String())
	}
}

func TestProvenanceJSON(t *testing.T) {
	r, out, errBuf := provenanceRenderer(ui.ModeJSON)
	printProvenance(r, sampleSource())

	if out.Len() != 0 {
		t.Errorf("provenance must not touch stdout, got %q", out.String())
	}
	var got struct {
		Provenance map[string]any `json:"provenance"`
	}
	if err := json.Unmarshal(errBuf.Bytes(), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, errBuf.String())
	}
	want := map[string]any{
		"final_url":     "https://www.mozilla.org/",
		"origin_status": float64(200),
		"retrieved_at":  "2026-11-04T17:52:38Z",
		"checksum":      "sha256:abc",
		"cache":         "hit",
		"age_seconds":   float64(3),
		"trace_id":      "trace-1",
	}
	for k, v := range want {
		if got.Provenance[k] != v {
			t.Errorf("%s = %v, want %v", k, got.Provenance[k], v)
		}
	}
}

// A fetch-hit can legitimately have age 0; it must still be reported.
func TestProvenanceJSONKeepsZeroAge(t *testing.T) {
	r, _, errBuf := provenanceRenderer(ui.ModeJSON)
	printProvenance(r, client.Source{Cache: "fetch-hit", HasAge: true})
	if !strings.Contains(errBuf.String(), `"age_seconds":0`) {
		t.Errorf("got %s", errBuf.String())
	}
}
