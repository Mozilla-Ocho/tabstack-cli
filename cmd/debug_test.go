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

// fixedClock returns a now func pinned to t.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func sampleDebug() client.DebugInfo {
	return client.DebugInfo{
		Method:             "POST",
		URL:                "https://api.example/v1/extract/markdown",
		Status:             200,
		Elapsed:            342 * time.Millisecond,
		TraceID:            "trace-abc",
		RateLimitLimit:     "1000",
		RateLimitRemaining: "997",
		RateLimitReset:     time.Unix(1000, 0).Add(30 * time.Second),
	}
}

func TestDebugLinePretty(t *testing.T) {
	now := fixedClock(time.Unix(1000, 0))
	line := debugLine(sampleDebug(), now)
	for _, want := range []string{"POST", "200", "342ms", "trace=trace-abc", "ratelimit=997/1000", "resets in 30s"} {
		if !strings.Contains(line, want) {
			t.Errorf("line missing %q:\n%s", want, line)
		}
	}
}

func TestDebugJSONShape(t *testing.T) {
	now := fixedClock(time.Unix(1000, 0))
	var got struct {
		Debug struct {
			Method    string `json:"method"`
			Status    int    `json:"status"`
			ElapsedMS int64  `json:"elapsed_ms"`
			TraceID   string `json:"trace_id"`
			RateLimit struct {
				Limit     string `json:"limit"`
				Remaining string `json:"remaining"`
				Reset     string `json:"reset"`
				ResetIn   string `json:"reset_in"`
			} `json:"ratelimit"`
		} `json:"debug"`
	}
	if err := json.Unmarshal([]byte(debugJSON(sampleDebug(), now)), &got); err != nil {
		t.Fatalf("debugJSON not valid JSON: %v", err)
	}
	d := got.Debug
	if d.Status != 200 || d.ElapsedMS != 342 || d.TraceID != "trace-abc" {
		t.Errorf("debug = %+v", d)
	}
	if d.RateLimit.Remaining != "997" || d.RateLimit.Limit != "1000" {
		t.Errorf("ratelimit = %+v", d.RateLimit)
	}
	// reset_in is a bare duration, not the human phrase.
	if d.RateLimit.ResetIn != "30s" {
		t.Errorf("reset_in = %q, want 30s", d.RateLimit.ResetIn)
	}
}

func TestDebugSinkWritesToStderrByMode(t *testing.T) {
	var out, errBuf bytes.Buffer
	r := ui.Renderer{Out: &out, Err: &errBuf, Mode: ui.ModeJSON, Styles: ui.NewStyles(true)}
	debugSink(r, fixedClock(time.Unix(1000, 0)))(sampleDebug())

	if out.Len() != 0 {
		t.Errorf("debug wrote to stdout: %q", out.String())
	}
	if !strings.Contains(errBuf.String(), `"trace_id":"trace-abc"`) {
		t.Errorf("stderr missing JSON debug: %s", errBuf.String())
	}
}

func TestDebugLineOmitsAbsentFields(t *testing.T) {
	// A failed round trip: status 0, no headers.
	line := debugLine(client.DebugInfo{Method: "POST", Elapsed: time.Second}, time.Now)
	if strings.Contains(line, "trace=") || strings.Contains(line, "ratelimit=") {
		t.Errorf("line should omit absent fields:\n%s", line)
	}
	if !strings.Contains(line, "1s") {
		t.Errorf("line missing elapsed:\n%s", line)
	}
}

func TestRetryLine(t *testing.T) {
	got := retryLine(client.RetryInfo{Attempt: 1, MaxRetries: 2, Status: 429, Wait: 3 * time.Second})
	if want := "429 too many requests, retrying in 3s (1/2)"; got != want {
		t.Errorf("retryLine = %q, want %q", got, want)
	}
}

func TestRetrySinkJSONToStderr(t *testing.T) {
	var out, errBuf bytes.Buffer
	r := ui.Renderer{Out: &out, Err: &errBuf, Mode: ui.ModeJSON, Styles: ui.NewStyles(true)}
	retrySink(r)(client.RetryInfo{Attempt: 2, MaxRetries: 2, Status: 503, Wait: 1500 * time.Millisecond})

	if out.Len() != 0 {
		t.Errorf("retry notice leaked to stdout: %q", out.String())
	}
	var got struct {
		Retry struct {
			Status     int   `json:"status"`
			Attempt    int   `json:"attempt"`
			MaxRetries int   `json:"max_retries"`
			WaitMS     int64 `json:"wait_ms"`
		} `json:"retry"`
	}
	if err := json.Unmarshal(errBuf.Bytes(), &got); err != nil {
		t.Fatalf("stderr not JSON: %v: %q", err, errBuf.String())
	}
	if got.Retry.Status != 503 || got.Retry.Attempt != 2 || got.Retry.MaxRetries != 2 || got.Retry.WaitMS != 1500 {
		t.Errorf("retry = %+v", got.Retry)
	}
}
