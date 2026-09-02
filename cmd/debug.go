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

// retrySink renders one retried attempt, in the same shape as the debug line
// so a slow run reads consistently. It always writes to stderr.
//
// Visibility follows the output mode. In pretty mode a retry is worth
// mentioning unprompted, because otherwise a command that pauses for several
// seconds looks like a hang. In JSON mode it is suppressed unless --debug is
// set, so a machine consumer's stderr stays as quiet as it was before.
func retrySink(r ui.Renderer, debug bool) func(client.RetryInfo) {
	if r.Mode == ui.ModeJSON && !debug {
		return nil
	}
	return func(info client.RetryInfo) {
		if r.Mode == ui.ModeJSON {
			fmt.Fprintln(r.Err, retryJSON(info))
			return
		}
		fmt.Fprintln(r.Err, r.Styles.Muted.Render(retryLine(info)))
	}
}

// retryLine is the human-readable one-liner, mirroring debugLine.
func retryLine(info client.RetryInfo) string {
	var b strings.Builder
	b.WriteString("retry ")
	if info.StatusCode > 0 {
		fmt.Fprintf(&b, "%d ", info.StatusCode)
	} else if info.Err != "" {
		b.WriteString(info.Err + " ")
	}
	fmt.Fprintf(&b, "in %s", info.Wait.Round(time.Millisecond))
	fmt.Fprintf(&b, " (attempt %d, %d left)", info.Attempt, info.Remaining)
	if info.TraceID != "" {
		b.WriteString(" trace " + info.TraceID)
	}
	return b.String()
}

// retryJSON is the machine-readable form, nested under a "retry" key so it is
// distinguishable from a debug line on the same stream.
func retryJSON(info client.RetryInfo) string {
	payload := struct {
		Retry struct {
			Method    string `json:"method,omitempty"`
			URL       string `json:"url,omitempty"`
			Status    int    `json:"status,omitempty"`
			Error     string `json:"error,omitempty"`
			WaitMS    int64  `json:"wait_ms"`
			Attempt   int    `json:"attempt"`
			Remaining int    `json:"remaining"`
			TraceID   string `json:"trace_id,omitempty"`
		} `json:"retry"`
	}{}
	payload.Retry.Method = info.Method
	payload.Retry.URL = info.URL
	payload.Retry.Status = info.StatusCode
	payload.Retry.Error = info.Err
	payload.Retry.WaitMS = info.Wait.Milliseconds()
	payload.Retry.Attempt = info.Attempt
	payload.Retry.Remaining = info.Remaining
	payload.Retry.TraceID = info.TraceID

	raw, err := json.Marshal(payload)
	if err != nil {
		return `{"retry":{}}`
	}
	return string(raw)
}
