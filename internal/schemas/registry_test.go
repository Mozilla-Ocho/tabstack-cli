package schemas

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testIndex = `{
  "name": "tabstack-schemas",
  "count": 3,
  "schemas": [
    {"category": "jobs", "title": "Job Posting", "path": "jobs/job-posting.json"},
    {"category": "jobs", "title": "Layoff Event", "path": "jobs/layoff-event.json"},
    {"category": "finance", "title": "Crypto Asset", "path": "finance/crypto-asset.json"}
  ]
}`

func testServer(t *testing.T) *Fetcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write([]byte(testIndex))
		case "/jobs/job-posting.json":
			_, _ = w.Write([]byte(`{"title":"Job Posting"}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return NewFetcher(WithRawBase(srv.URL), WithHTTPClient(srv.Client()))
}

func TestIndex(t *testing.T) {
	idx, err := testServer(t).Index(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Schemas) != 3 {
		t.Fatalf("got %d schemas, want 3", len(idx.Schemas))
	}
}

func TestFetch(t *testing.T) {
	data, err := testServer(t).Fetch(context.Background(), "jobs/job-posting.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"title":"Job Posting"}` {
		t.Errorf("body = %q", data)
	}
}

func TestFetchNotFound(t *testing.T) {
	_, err := testServer(t).Fetch(context.Background(), "missing.json")
	var fe *FetchError
	if !errors.As(err, &fe) {
		t.Fatalf("want *FetchError, got %v", err)
	}
	if fe.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", fe.StatusCode)
	}
}

func TestResolve(t *testing.T) {
	idx, err := testServer(t).Index(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		sel       string
		wantPaths []string
		wantErr   bool
	}{
		{"job-posting", []string{"jobs/job-posting.json"}, false},
		{"job-posting.json", []string{"jobs/job-posting.json"}, false},
		{"jobs/job-posting.json", []string{"jobs/job-posting.json"}, false},
		{"jobs/job-posting", []string{"jobs/job-posting.json"}, false},
		{"jobs", []string{"jobs/job-posting.json", "jobs/layoff-event.json"}, false},
		{"", nil, true},
		{"nope", nil, true},
		{"jobs/nope.json", nil, true},
	}
	for _, tc := range cases {
		got, err := idx.Resolve(tc.sel)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Resolve(%q): expected error", tc.sel)
			}
			continue
		}
		if err != nil {
			t.Errorf("Resolve(%q): %v", tc.sel, err)
			continue
		}
		if len(got) != len(tc.wantPaths) {
			t.Errorf("Resolve(%q): got %d entries, want %d", tc.sel, len(got), len(tc.wantPaths))
			continue
		}
		for i, p := range tc.wantPaths {
			if got[i].Path != p {
				t.Errorf("Resolve(%q)[%d] = %q, want %q", tc.sel, i, got[i].Path, p)
			}
		}
	}
}

func TestResolveAmbiguous(t *testing.T) {
	idx := Index{Schemas: []Entry{
		{Category: "a", Path: "a/news-article.json"},
		{Category: "b", Path: "b/news-article.json"},
	}}
	if _, err := idx.Resolve("news-article"); err == nil {
		t.Error("expected ambiguity error")
	}
}

func TestResolveNameCategoryCollision(t *testing.T) {
	// A selector that is both a category and a bare schema name is ambiguous.
	idx := Index{Schemas: []Entry{
		{Category: "jobs", Path: "jobs/something.json"},
		{Category: "other", Path: "other/jobs.json"}, // bare name "jobs"
	}}
	if _, err := idx.Resolve("jobs"); err == nil {
		t.Error("expected error for name/category collision")
	}
}

func TestFetchErrorMessage(t *testing.T) {
	e := &FetchError{URL: "https://x/y.json", StatusCode: 404}
	if got := e.Error(); got != "fetch https://x/y.json: status 404" {
		t.Errorf("Error() = %q", got)
	}
}

func TestIndexDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()
	f := NewFetcher(WithRawBase(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := f.Index(context.Background()); err == nil {
		t.Error("expected decode error for malformed index.json")
	}
}

func TestIndexFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	f := NewFetcher(WithRawBase(srv.URL), WithHTTPClient(srv.Client()))
	var fe *FetchError
	if _, err := f.Index(context.Background()); !errors.As(err, &fe) || fe.StatusCode != 500 {
		t.Errorf("err = %v, want *FetchError 500", err)
	}
}

func TestNewFetcherTrimsRawBase(t *testing.T) {
	f := NewFetcher(WithRawBase("https://x/base/"))
	if f.rawBase != "https://x/base" {
		t.Errorf("rawBase = %q, want trailing slash trimmed", f.rawBase)
	}
}
