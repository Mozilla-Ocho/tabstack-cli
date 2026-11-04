package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// addProvenanceFlag registers --provenance (-p) on a fetch-based command.
func addProvenanceFlag(f *pflag.FlagSet, v *bool) {
	f.BoolVarP(v, "provenance", "p", false, "print the source document's final URL, origin status, retrieval time, checksum and cache status to stderr")
}

// printProvenance reports where a response's source document came from. Like
// --debug it writes to stderr, so the result on stdout stays exactly what the
// API returned and `| jq` is unaffected. In JSON output mode it emits one JSON
// object; otherwise an aligned block.
func printProvenance(r ui.Renderer, s client.Source) {
	if r.Mode == ui.ModeJSON {
		fmt.Fprintln(r.Err, provenanceJSON(s))
		return
	}
	fmt.Fprint(r.Err, provenanceBlock(r, s))
}

// provenanceRows is the ordered label/value list shared by the pretty block.
// Unknown values are left out rather than shown blank: the API omits a header
// when it does not know the value.
func provenanceRows(s client.Source) [][2]string {
	var rows [][2]string
	add := func(label, value string) {
		if value != "" {
			rows = append(rows, [2]string{label, value})
		}
	}
	add("final url", s.FinalURL)
	if s.OriginStatus > 0 {
		add("origin status", strconv.Itoa(s.OriginStatus))
	}
	if !s.RetrievedAt.IsZero() {
		add("retrieved", s.RetrievedAt.UTC().Format(time.RFC3339))
	}
	add("checksum", s.Checksum)
	cache := s.Cache
	if cache != "" && s.HasAge {
		cache += fmt.Sprintf(" (age %s)", s.Age)
	}
	add("cache", cache)
	add("trace id", s.TraceID)
	return rows
}

// provenanceBlock is the human-readable form.
func provenanceBlock(r ui.Renderer, s client.Source) string {
	var b strings.Builder
	b.WriteString("\n" + r.Styles.Label.Render("provenance") + "\n")
	rows := provenanceRows(s)
	if len(rows) == 0 {
		b.WriteString("  " + r.Styles.Muted.Render("not reported by this API") + "\n")
		return b.String()
	}
	width := 0
	for _, row := range rows {
		width = max(width, len(row[0]))
	}
	for _, row := range rows {
		label := r.Styles.Muted.Render(fmt.Sprintf("%-*s", width, row[0]))
		fmt.Fprintf(&b, "  %s  %s\n", label, row[1])
	}
	return b.String()
}

// provenanceJSON is the machine-readable form, one line, for --output json.
func provenanceJSON(s client.Source) string {
	payload := struct {
		Provenance struct {
			FinalURL     string `json:"final_url,omitempty"`
			OriginStatus int    `json:"origin_status,omitempty"`
			RetrievedAt  string `json:"retrieved_at,omitempty"`
			Checksum     string `json:"checksum,omitempty"`
			Cache        string `json:"cache,omitempty"`
			AgeSeconds   *int64 `json:"age_seconds,omitempty"`
			TraceID      string `json:"trace_id,omitempty"`
		} `json:"provenance"`
	}{}
	p := &payload.Provenance
	p.FinalURL = s.FinalURL
	p.OriginStatus = s.OriginStatus
	if !s.RetrievedAt.IsZero() {
		p.RetrievedAt = s.RetrievedAt.UTC().Format(time.RFC3339)
	}
	p.Checksum = s.Checksum
	p.Cache = s.Cache
	if s.HasAge {
		age := int64(s.Age / time.Second)
		p.AgeSeconds = &age
	}
	p.TraceID = s.TraceID
	out, _ := json.Marshal(payload)
	return string(out)
}
