package cmd

import (
	"bytes"
	"encoding/json"
	"io"
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

// TestRetrySinkVisibility pins when a retry is mentioned. Pretty mode says so
// unprompted, because a command that pauses for seconds otherwise looks hung.
// JSON mode stays quiet unless --debug is set, so a machine consumer's stderr
// is no noisier than before.
func TestRetrySinkVisibility(t *testing.T) {
	cases := []struct {
		name    string
		mode    ui.OutputMode
		debug   bool
		wantNil bool
	}{
		{"pretty without debug still reports", ui.ModePretty, false, false},
		{"pretty with debug reports", ui.ModePretty, true, false},
		{"json without debug stays quiet", ui.ModeJSON, false, true},
		{"json with debug reports", ui.ModeJSON, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := ui.Renderer{Out: io.Discard, Err: &buf, Mode: tc.mode, Styles: ui.NewStyles(true)}
			sink := retrySink(r, tc.debug)

			if tc.wantNil {
				if sink != nil {
					t.Fatal("expected no sink, so the client is not asked to report")
				}
				return
			}
			if sink == nil {
				t.Fatal("expected a sink")
			}

			sink(client.RetryInfo{
				Method: "POST", URL: "https://api.test/x",
				Attempt: 1, Remaining: 1,
				StatusCode: 429, Wait: 500 * time.Millisecond, TraceID: "t-1",
			})
			got := buf.String()
			if tc.mode == ui.ModeJSON {
				var payload struct {
					Retry struct {
						Status    int    `json:"status"`
						WaitMS    int64  `json:"wait_ms"`
						Attempt   int    `json:"attempt"`
						Remaining int    `json:"remaining"`
						TraceID   string `json:"trace_id"`
					} `json:"retry"`
				}
				if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &payload); err != nil {
					t.Fatalf("not valid JSON: %v (%q)", err, got)
				}
				if payload.Retry.Status != 429 || payload.Retry.WaitMS != 500 || payload.Retry.TraceID != "t-1" {
					t.Errorf("payload = %+v", payload.Retry)
				}
				return
			}
			for _, want := range []string{"retry", "429", "500ms", "attempt 1", "1 left", "t-1"} {
				if !strings.Contains(got, want) {
					t.Errorf("line missing %q: %q", want, got)
				}
			}
		})
	}
}
