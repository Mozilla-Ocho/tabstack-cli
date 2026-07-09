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
	"time"
)

// DefaultRawBase is the raw.githubusercontent.com root for the schema repo's
// main branch. Every schema and the index hang off this.
const DefaultRawBase = "https://raw.githubusercontent.com/Mozilla-Ocho/tabstack-schemas/main"

// defaultTimeout bounds each schema fetch. Unlike the SSE streams in
// internal/client (which deliberately run untimed), these are finite GETs, so a
// stalled connection to raw.githubusercontent.com must not hang the command —
// or, worse, <TAB> completion — forever. Callers that need a tighter bound
// (e.g. shell completion) still layer a shorter context deadline on top.
const defaultTimeout = 30 * time.Second

// maxResponseBytes caps how much we read from a single GitHub response. Schemas
// and the index are small JSON documents; this guards against a hostile or
// misconfigured endpoint streaming an unbounded body into memory. 8MB is far
// above any real schema or index while still bounding the worst case.
const maxResponseBytes = 8 << 20

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
		http:    &http.Client{Timeout: defaultTimeout},
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("fetch %s: response exceeds %d bytes", url, maxResponseBytes)
	}
	return data, nil
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

// selectorKind classifies a parsed selector.
type selectorKind int

const (
	selectorPath selectorKind = iota // explicit repo path, ".json" suffix ensured
	selectorName                     // bare schema name, ".json" suffix trimmed
)

// parseSelector normalises a user selector into its kind and canonical value. A
// selector containing a slash is an explicit repo path (".json" appended if
// absent); otherwise it is a bare schema name (".json" trimmed). FindLocal
// (offline) and Index.Resolve (online) share this parsing and differ only in
// how they look the result up.
func parseSelector(selector string) (selectorKind, string, error) {
	q := strings.TrimSpace(selector)
	if q == "" {
		return 0, "", fmt.Errorf("empty schema selector")
	}
	if strings.Contains(q, "/") {
		if !strings.HasSuffix(q, ".json") {
			q += ".json"
		}
		return selectorPath, q, nil
	}
	return selectorName, strings.TrimSuffix(q, ".json"), nil
}

// Resolve maps a user selector onto manifest entries. A selector may be:
//   - a repo-relative path ("jobs/job-posting.json", trailing .json optional)
//   - a category ("jobs") -> every schema in that category
//   - a bare schema name ("job-posting") -> the matching schema
//
// Bare names that match more than one schema, or that collide with a category,
// return an ambiguity error so the user can disambiguate with a full path.
func (idx Index) Resolve(selector string) ([]Entry, error) {
	kind, val, err := parseSelector(selector)
	if err != nil {
		return nil, err
	}

	if kind == selectorPath {
		for _, e := range idx.Schemas {
			if e.Path == val {
				return []Entry{e}, nil
			}
		}
		return nil, fmt.Errorf("no schema at path %q (run `tabstack schema list`)", val)
	}

	name := val
	var byName, byCategory []Entry
	for _, e := range idx.Schemas {
		if base := strings.TrimSuffix(path.Base(e.Path), ".json"); base == name {
			byName = append(byName, e)
		}
		if e.Category == name {
			byCategory = append(byCategory, e)
		}
	}

	switch {
	case len(byName) == 1 && len(byCategory) == 0:
		return byName, nil
	case len(byName) == 0 && len(byCategory) > 0:
		return byCategory, nil
	case len(byName) > 1:
		return nil, fmt.Errorf("%q is ambiguous, matches %s; use a full path", name, pathsOf(byName))
	case len(byName) >= 1 && len(byCategory) > 0:
		return nil, fmt.Errorf("%q matches both a schema and a category; use a full path or the category name", name)
	default:
		return nil, fmt.Errorf("no schema named %q (run `tabstack schema list`)", name)
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
