package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
	"github.com/Mozilla-Ocho/tabstack-cli/internal/ui"
)

// debugSink renders one API round trip's debug line. It always writes to
// stderr, so it never contaminates piped stdout or an NDJSON stream. In JSON
// output mode it emits a JSON object; otherwise a compact human line. The clock
// is injected so the relative rate-limit reset is testable.
func debugSink(r ui.Renderer, now func() time.Time) func(client.DebugInfo) {
	if now == nil {
		now = time.Now
	}
	return func(d client.DebugInfo) {
		if r.Mode == ui.ModeJSON {
			fmt.Fprintln(r.Err, debugJSON(d, now))
			return
		}
		fmt.Fprintln(r.Err, r.Styles.Muted.Render(debugLine(d, now)))
	}
}

// debugLine is the human-readable one-liner.
func debugLine(d client.DebugInfo, now func() time.Time) string {
	var b strings.Builder
	b.WriteString("debug ")
	if d.Method != "" {
		b.WriteString(d.Method + " ")
	}
	if d.Status > 0 {
		fmt.Fprintf(&b, "%d ", d.Status)
	}
	b.WriteString(d.Elapsed.Round(time.Millisecond).String())
	if d.TraceID != "" {
		b.WriteString("  trace=" + d.TraceID)
	}
	if d.RateLimitRemaining != "" && d.RateLimitLimit != "" {
		fmt.Fprintf(&b, "  ratelimit=%s/%s", d.RateLimitRemaining, d.RateLimitLimit)
		if reset, ok := resetIn(d.RateLimitReset, now); ok {
			fmt.Fprintf(&b, " (resets in %s)", reset)
		}
	}
	return b.String()
}

// debugJSON is the machine-readable form, one line, for --output json.
func debugJSON(d client.DebugInfo, now func() time.Time) string {
	type rateLimit struct {
		Limit     string `json:"limit,omitempty"`
		Remaining string `json:"remaining,omitempty"`
		Reset     string `json:"reset,omitempty"`
		ResetIn   string `json:"reset_in,omitempty"`
	}
	payload := struct {
		Debug struct {
			Method    string     `json:"method,omitempty"`
			URL       string     `json:"url,omitempty"`
			Status    int        `json:"status,omitempty"`
			ElapsedMS int64      `json:"elapsed_ms"`
			TraceID   string     `json:"trace_id,omitempty"`
			RateLimit *rateLimit `json:"ratelimit,omitempty"`
		} `json:"debug"`
	}{}
	payload.Debug.Method = d.Method
	payload.Debug.URL = d.URL
	payload.Debug.Status = d.Status
	payload.Debug.ElapsedMS = d.Elapsed.Milliseconds()
	payload.Debug.TraceID = d.TraceID
	if d.RateLimitLimit != "" || d.RateLimitRemaining != "" || !d.RateLimitReset.IsZero() {
		rl := &rateLimit{Limit: d.RateLimitLimit, Remaining: d.RateLimitRemaining}
		if reset, ok := resetIn(d.RateLimitReset, now); ok {
			rl.Reset = d.RateLimitReset.UTC().Format(time.RFC3339)
			rl.ResetIn = reset
		}
		payload.Debug.RateLimit = rl
	}
	out, _ := json.Marshal(payload)
	return string(out)
}

// resetIn renders a rate-limit reset as a relative, non-negative duration
// string, e.g. "42s". ok is false when there is no reset time.
func resetIn(reset time.Time, now func() time.Time) (string, bool) {
	if reset.IsZero() {
		return "", false
	}
	d := max(reset.Sub(now()).Round(time.Second), 0)
	return d.String(), true
}
