// Package schemas talks to the public tabstack-schemas repository on GitHub and
// manages a local store of pulled schemas. It is deliberately separate from
// internal/client: that client is bound to the authenticated Tabstack API,
// whereas this fetches unauthenticated raw files from GitHub over a different
// host. Keeping them apart avoids leaking the API bearer token to GitHub and
// keeps each transport's concerns isolated.
package schemas

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
)

// DefaultRawBase is the raw.githubusercontent.com root for the schema repo's
// main branch. Every schema and the index hang off this.
const DefaultRawBase = "https://raw.githubusercontent.com/Mozilla-Ocho/tabstack-schemas/main"

// Entry is one schema's manifest record from index.json.
type Entry struct {
	Category    string `json:"category"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

// Index is the decoded index.json: a manifest of every available schema.
type Index struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Count       int     `json:"count"`
	Schemas     []Entry `json:"schemas"`
}

// FetchError is a non-2xx response from GitHub. We surface the URL and status
// so the command layer can produce a clear, actionable message.
type FetchError struct {
	URL        string
	StatusCode int
}

func (e *FetchError) Error() string {
	return fmt.Sprintf("fetch %s: status %d", e.URL, e.StatusCode)
}

// Fetcher retrieves the index and individual schemas from the repo. The
// http.Client and base URL are injectable so tests can point at a mock,
// mirroring client.WithHTTPClient.
type Fetcher struct {
	http    *http.Client
	rawBase string
}

// Option configures a Fetcher.
type Option func(*Fetcher)

// WithHTTPClient swaps the underlying http.Client. Mostly useful for tests.
func WithHTTPClient(h *http.Client) Option {
	return func(f *Fetcher) { f.http = h }
}

// WithRawBase overrides the raw content base URL. Mostly useful for tests.
func WithRawBase(base string) Option {
	return func(f *Fetcher) { f.rawBase = strings.TrimRight(base, "/") }
}

// NewFetcher constructs a Fetcher pointed at the public schema repo.
func NewFetcher(opts ...Option) *Fetcher {
	f := &Fetcher{
		http:    &http.Client{},
		rawBase: DefaultRawBase,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// get performs a GET and returns the body, mapping non-2xx to a *FetchError.
func (f *Fetcher) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &FetchError{URL: url, StatusCode: resp.StatusCode}
	}
	return io.ReadAll(resp.Body)
}

// Index fetches and decodes index.json.
func (f *Fetcher) Index(ctx context.Context) (Index, error) {
	var idx Index
	data, err := f.get(ctx, f.rawBase+"/index.json")
	if err != nil {
		return idx, err
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return idx, fmt.Errorf("decode index.json: %w", err)
	}
	return idx, nil
}

// Fetch retrieves one schema by its repo-relative path (e.g. jobs/job-posting.json).
func (f *Fetcher) Fetch(ctx context.Context, schemaPath string) ([]byte, error) {
	return f.get(ctx, f.rawBase+"/"+strings.TrimLeft(schemaPath, "/"))
}

// Resolve maps a user selector onto manifest entries. A selector may be:
//   - a repo-relative path ("jobs/job-posting.json", trailing .json optional)
//   - a category ("jobs") -> every schema in that category
//   - a bare schema name ("job-posting") -> the matching schema
//
// Bare names that match more than one schema, or that collide with a category,
// return an ambiguity error so the user can disambiguate with a full path.
func (idx Index) Resolve(selector string) ([]Entry, error) {
	q := strings.TrimSpace(selector)
	if q == "" {
		return nil, fmt.Errorf("empty schema selector")
	}

	// A selector containing a slash is treated as an explicit path.
	if strings.Contains(q, "/") {
		want := q
		if !strings.HasSuffix(want, ".json") {
			want += ".json"
		}
		for _, e := range idx.Schemas {
			if e.Path == want {
				return []Entry{e}, nil
			}
		}
		return nil, fmt.Errorf("no schema at path %q (run `tabstack schema list`)", q)
	}

	name := strings.TrimSuffix(q, ".json")
	var byName, byCategory []Entry
	for _, e := range idx.Schemas {
		if base := strings.TrimSuffix(path.Base(e.Path), ".json"); base == name {
			byName = append(byName, e)
		}
		if e.Category == q {
			byCategory = append(byCategory, e)
		}
	}

	switch {
	case len(byName) == 1 && len(byCategory) == 0:
		return byName, nil
	case len(byName) == 0 && len(byCategory) > 0:
		return byCategory, nil
	case len(byName) > 1:
		return nil, fmt.Errorf("%q is ambiguous, matches %s; use a full path", q, pathsOf(byName))
	case len(byName) >= 1 && len(byCategory) > 0:
		return nil, fmt.Errorf("%q matches both a schema and a category; use a full path or the category name", q)
	default:
		return nil, fmt.Errorf("no schema named %q (run `tabstack schema list`)", q)
	}
}

// pathsOf joins entry paths for an error message, sorted for stable output.
func pathsOf(entries []Entry) string {
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	sort.Strings(paths)
	return strings.Join(paths, ", ")
}
