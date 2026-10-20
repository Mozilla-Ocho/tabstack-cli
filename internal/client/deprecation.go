package client

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Deprecation is what a response says about the endpoint's retirement, read
// from the Deprecation (RFC 9745) and Sunset (RFC 8594) headers and any Link
// with rel="deprecation" or rel="sunset". Zero times mean the server gave no
// date.
type Deprecation struct {
	Method   string
	Endpoint string    // request path, e.g. /v1/extract/json
	Since    time.Time // when the endpoint was (or will be) deprecated
	Sunset   time.Time // when the endpoint stops working
	Link     string    // documentation for the deprecation or migration
}

// WithDeprecationNotify registers a callback invoked when a response marks its
// endpoint deprecated or scheduled for sunset. It fires at most once per
// endpoint per Client, so retries and repeated calls (the MCP server) do not
// repeat the warning. A nil callback is a no-op.
func WithDeprecationNotify(fn func(Deprecation)) Option {
	return func(c *Client) {
		if fn == nil {
			return
		}
		c.deprecation = &deprecationNotifier{fn: fn, seen: map[string]bool{}}
	}
}

// deprecationNotifier dedupes warnings per endpoint. It is shared across
// goroutines when the MCP server runs tools concurrently, hence the mutex.
type deprecationNotifier struct {
	fn   func(Deprecation)
	mu   sync.Mutex
	seen map[string]bool
}

func (n *deprecationNotifier) check(req *http.Request, resp *http.Response) {
	if n == nil {
		return
	}
	d, ok := ParseDeprecation(resp.Header)
	if !ok {
		return
	}
	d.Method = req.Method
	d.Endpoint = req.URL.Path

	key := d.Method + " " + d.Endpoint
	n.mu.Lock()
	if n.seen[key] {
		n.mu.Unlock()
		return
	}
	n.seen[key] = true
	n.mu.Unlock()
	n.fn(d)
}

// ParseDeprecation reads the deprecation headers. ok is true when the response
// carries a Deprecation or Sunset header.
//
// Deprecation is an RFC 9651 date (`@1688169599`); the HTTP-date and bare
// `true` forms from earlier drafts are accepted too, since servers still send
// them. Sunset is an HTTP-date.
func ParseDeprecation(h http.Header) (Deprecation, bool) {
	dep := strings.TrimSpace(h.Get("Deprecation"))
	sun := strings.TrimSpace(h.Get("Sunset"))
	if dep == "" && sun == "" {
		return Deprecation{}, false
	}

	var d Deprecation
	switch {
	case strings.HasPrefix(dep, "@"):
		if secs, err := strconv.ParseInt(dep[1:], 10, 64); err == nil {
			d.Since = time.Unix(secs, 0)
		}
	case dep != "":
		if t, err := http.ParseTime(dep); err == nil {
			d.Since = t
		}
	}
	if sun != "" {
		if t, err := http.ParseTime(sun); err == nil {
			d.Sunset = t
		}
	}
	d.Link = linkWithRel(h.Values("Link"), "deprecation")
	if d.Link == "" {
		d.Link = linkWithRel(h.Values("Link"), "sunset")
	}
	return d, true
}

// linkWithRel returns the target of the first Link header entry whose rel
// includes want, e.g. `<https://x/dep>; rel="deprecation"`. It covers the
// common shape, not the full RFC 8288 grammar (no commas inside URLs).
func linkWithRel(values []string, want string) string {
	for _, v := range values {
		for entry := range strings.SplitSeq(v, ",") {
			target, params, ok := strings.Cut(strings.TrimSpace(entry), ";")
			if !ok {
				continue
			}
			target = strings.TrimSpace(target)
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
				continue
			}
			for p := range strings.SplitSeq(params, ";") {
				k, val, ok := strings.Cut(strings.TrimSpace(p), "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(k), "rel") {
					continue
				}
				for rel := range strings.FieldsSeq(strings.Trim(strings.TrimSpace(val), `"`)) {
					if strings.EqualFold(rel, want) {
						return target[1 : len(target)-1]
					}
				}
			}
		}
	}
	return ""
}
