package client

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimit is the caller's rate-limit state as reported by response headers.
// Fields are empty (or zero) when the server did not send them.
type RateLimit struct {
	Limit     string    // requests allowed in the window
	Remaining string    // requests left in the window
	Reset     time.Time // when the window resets
}

// unixThreshold separates a reset given as unix seconds from one given as a
// delta: no window is a billion seconds long, and no current timestamp is
// shorter than that.
const unixThreshold = 1_000_000_000

// ParseRateLimit reads rate-limit headers in the three shapes servers send,
// taking each field from the first shape that carries it:
//
//  1. IETF structured fields (draft-ietf-httpapi-ratelimit-headers-08 and
//     later): `RateLimit: "default";r=50;t=30` for remaining and reset delta,
//     with the limit from `RateLimit-Policy: "default";q=100;w=60`.
//  2. Earlier IETF drafts: RateLimit-Limit, RateLimit-Remaining, and
//     RateLimit-Reset (delta seconds).
//  3. The de facto X-RateLimit-Limit, X-RateLimit-Remaining, and
//     X-RateLimit-Reset (unix seconds, or a delta when the value is small).
//
// Reading all three means --debug and retry timing keep working whichever one
// the API settles on.
func ParseRateLimit(h http.Header, now time.Time) RateLimit {
	var rl RateLimit

	if params := firstItemParams(h.Get("RateLimit")); params != nil {
		rl.Remaining = params["r"]
		if t, ok := parseSeconds(params["t"]); ok {
			rl.Reset = now.Add(time.Duration(t) * time.Second)
		}
	}
	if params := firstItemParams(h.Get("RateLimit-Policy")); params != nil {
		rl.Limit = params["q"]
	}

	if rl.Limit == "" {
		rl.Limit = firstToken(h.Get("RateLimit-Limit"))
	}
	if rl.Remaining == "" {
		rl.Remaining = firstToken(h.Get("RateLimit-Remaining"))
	}
	if rl.Reset.IsZero() {
		if t, ok := parseSeconds(firstToken(h.Get("RateLimit-Reset"))); ok {
			rl.Reset = now.Add(time.Duration(t) * time.Second)
		}
	}

	if rl.Limit == "" {
		rl.Limit = h.Get("X-RateLimit-Limit")
	}
	if rl.Remaining == "" {
		rl.Remaining = h.Get("X-RateLimit-Remaining")
	}
	if rl.Reset.IsZero() {
		if t, ok := parseSeconds(h.Get("X-RateLimit-Reset")); ok {
			if t >= unixThreshold {
				rl.Reset = time.Unix(t, 0)
			} else {
				rl.Reset = now.Add(time.Duration(t) * time.Second)
			}
		}
	}
	return rl
}

// firstItemParams returns the parameters of the first member of a structured
// field list, e.g. `"default";r=50;t=30, "burst";r=5` gives {r:50, t:30}. It
// returns nil for an empty header. This covers the subset the rate-limit
// headers use (a token or string item with integer parameters), not the full
// RFC 9651 grammar.
func firstItemParams(v string) map[string]string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	item, _, _ := strings.Cut(v, ",")
	parts := strings.Split(item, ";")
	params := make(map[string]string, len(parts)-1)
	for _, p := range parts[1:] {
		k, val, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok {
			continue
		}
		params[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(val), `"`)
	}
	return params
}

// firstToken returns the leading value of a header that may carry a list or
// parameters, e.g. "100, 100;w=60" gives "100".
func firstToken(v string) string {
	v, _, _ = strings.Cut(v, ",")
	v, _, _ = strings.Cut(v, ";")
	return strings.TrimSpace(v)
}

// parseSeconds parses a non-negative integer number of seconds.
func parseSeconds(v string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
