package client

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestSourceFromHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Tabstack-Final-Url", "https://www.mozilla.org/")
	h.Set("X-Tabstack-Origin-Status", "200")
	h.Set("X-Tabstack-Retrieved-At", "2026-11-04T17:52:38Z")
	h.Set("X-Tabstack-Checksum", "sha256:abc")
	h.Set("X-Tabstack-Cache", "hit")
	h.Set("Age", "3")
	h.Set("X-Trace-Id", "trace-1")

	got := sourceFrom(h)
	want := Source{
		FinalURL:     "https://www.mozilla.org/",
		OriginStatus: 200,
		RetrievedAt:  time.Date(2026, 11, 4, 17, 52, 38, 0, time.UTC),
		Checksum:     "sha256:abc",
		Cache:        "hit",
		Age:          3 * time.Second,
		HasAge:       true,
		TraceID:      "trace-1",
	}
	if !got.RetrievedAt.Equal(want.RetrievedAt) {
		t.Errorf("RetrievedAt = %v, want %v", got.RetrievedAt, want.RetrievedAt)
	}
	got.RetrievedAt, want.RetrievedAt = time.Time{}, time.Time{}
	if got != want {
		t.Errorf("sourceFrom = %+v\nwant %+v", got, want)
	}
}

// Metadata that does not parse is dropped, never fatal.
func TestSourceFromIgnoresMalformedHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Tabstack-Origin-Status", "teapot")
	h.Set("X-Tabstack-Retrieved-At", "yesterday")
	h.Set("Age", "-1")

	if got := sourceFrom(h); !got.Empty() {
		t.Errorf("sourceFrom = %+v, want empty", got)
	}
}

func TestSourceFromNoHeadersIsEmpty(t *testing.T) {
	if got := sourceFrom(http.Header{}); !got.Empty() {
		t.Errorf("sourceFrom = %+v, want empty", got)
	}
}

func TestFetchEndpointsReturnSource(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tabstack-Final-Url", "https://final.example/")
		w.Header().Set("X-Tabstack-Cache", "miss")
		_, _ = io.WriteString(w, `{"content":"x"}`)
	})
	ctx := context.Background()

	_, md, err := c.ExtractMarkdown(ctx, ExtractMarkdownRequest{URL: "https://e"})
	if err != nil {
		t.Fatal(err)
	}
	_, ej, err := c.ExtractJSON(ctx, ExtractJSONRequest{URL: "https://e"})
	if err != nil {
		t.Fatal(err)
	}
	_, gj, err := c.GenerateJSON(ctx, GenerateJSONRequest{URL: "https://e"})
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Source{"extract markdown": md, "extract json": ej, "generate json": gj} {
		if s.FinalURL != "https://final.example/" || s.Cache != "miss" {
			t.Errorf("%s: Source = %+v", name, s)
		}
	}
}
