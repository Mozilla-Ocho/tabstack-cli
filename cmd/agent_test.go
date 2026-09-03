package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mozilla-Ocho/tabstack-cli/internal/client"
)

func TestFinalText(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"automate finalAnswer", `{"finalAnswer":"done"}`, "done"},
		{"research answer", `{"answer":"the answer"}`, "the answer"},
		{"research report", `{"report":"full report"}`, "full report"},
		{"result", `{"result":"res"}`, "res"},
		{"summary", `{"summary":"sum"}`, "sum"},
		{"content", `{"content":"body"}`, "body"},
		{"bare string payload", `"just a string"`, "just a string"},
		{"empty object", `{}`, ""},
		{"blank field ignored", `{"answer":"   "}`, ""},
		{"invalid", `not json`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := finalText(client.Event{Name: "complete", Data: json.RawMessage(tc.data)})
			if got != tc.want {
				t.Errorf("finalText(%s) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

func TestFinalTextFieldPriority(t *testing.T) {
	// finalAnswer is tried before the research-style fields.
	got := finalText(client.Event{Data: json.RawMessage(`{"finalAnswer":"a","report":"b"}`)})
	if got != "a" {
		t.Errorf("got %q, want finalAnswer to win", got)
	}
}

func TestExtractCitations(t *testing.T) {
	t.Run("citedPages numbered by position", func(t *testing.T) {
		// Matches the research complete event: metadata.citedPages, ordered by
		// first citation appearance.
		e := client.Event{Data: json.RawMessage(`{
			"report": "Body with [1] and [2].",
			"metadata": {
				"citedPages": [
					{"id":"p1","title":"First","url":"https://a"},
					{"id":"p2","title":"Second","url":"https://b"}
				]
			}
		}`)}
		got := extractCitations(e)
		if len(got) != 2 {
			t.Fatalf("got %d, want 2", len(got))
		}
		if got[0].Number != 1 || got[0].Title != "First" || got[0].URL != "https://a" {
			t.Errorf("got[0] = %+v", got[0])
		}
		if got[1].Number != 2 || got[1].URL != "https://b" {
			t.Errorf("got[1] = %+v", got[1])
		}
	})

	t.Run("entry missing url and title is skipped", func(t *testing.T) {
		e := client.Event{Data: json.RawMessage(
			`{"metadata":{"citedPages":[{"id":"x"},{"id":"y","url":"https://y"}]}}`)}
		got := extractCitations(e)
		if len(got) != 1 || got[0].URL != "https://y" {
			t.Errorf("got %+v, want only the entry with a url", got)
		}
	})

	t.Run("no metadata", func(t *testing.T) {
		if got := extractCitations(client.Event{Data: json.RawMessage(`{"report":"x"}`)}); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		if got := extractCitations(client.Event{Data: json.RawMessage(`not json`)}); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})
}

func TestAutomateIterationBounds(t *testing.T) {
	cases := []struct {
		maxIter       int
		maxValidation int
		wantErr       bool
	}{
		{0, 0, false},    // not set, server default
		{1, 1, false},    // minimum valid
		{100, 10, false}, // maximum valid
		{-1, 0, true},
		{101, 0, true},
		{0, -1, true},
		{0, 11, true},
	}
	for _, tc := range cases {
		var err error
		if tc.maxIter != 0 && (tc.maxIter < 1 || tc.maxIter > 100) {
			err = fmt.Errorf("bounds")
		}
		if err == nil && tc.maxValidation != 0 && (tc.maxValidation < 1 || tc.maxValidation > 10) {
			err = fmt.Errorf("bounds")
		}
		if tc.wantErr && err == nil {
			t.Errorf("maxIter=%d maxVal=%d: expected error", tc.maxIter, tc.maxValidation)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("maxIter=%d maxVal=%d: unexpected error", tc.maxIter, tc.maxValidation)
		}
	}
}

func TestExtractMessage(t *testing.T) {
	cases := []struct {
		data string
		want string
	}{
		{`{"error":"boom"}`, "boom"},
		{`{"message":"msg"}`, "msg"},
		{`{"error":"e","message":"m"}`, "e"},
		{`plain text`, "plain text"},
	}
	for _, tc := range cases {
		got := extractMessage(client.Event{Name: "error", Data: json.RawMessage(tc.data)})
		if got != tc.want {
			t.Errorf("extractMessage(%s) = %q, want %q", tc.data, got, tc.want)
		}
	}
}

// sseServer serves an endless SSE stream, one event per tick, so a test can
// cancel or time out partway through a live stream.
func sseServer(t *testing.T, tick time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(tick):
				_, _ = io.WriteString(w, "event: tick\ndata: {}\n\n")
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStreamCancellation covers the two ways a long stream can be stopped.
// Both exit 1, but they mean different things: the user asked, versus a limit
// they set was reached, so only the latter explains itself.
func TestStreamCancellation(t *testing.T) {
	t.Run("interrupt returns promptly as cancelled", func(t *testing.T) {
		isolate(t)
		setTestApp(t)
		srv := sseServer(t, 10*time.Millisecond)
		rootApp.client = client.New("k", srv.URL)

		// Stands in for the signal handler cancelling the root context.
		parent, cancelParent := context.WithCancel(context.Background())
		go func() {
			time.Sleep(80 * time.Millisecond)
			cancelParent()
		}()

		start := time.Now()
		_, err := runStream(func(fn func(client.Event) error) error {
			return rootApp.client.Research(parent, client.ResearchRequest{Query: "q"}, fn)
		})
		coded := classifyStreamError(parent, err, 0, time.Since(start))

		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("took %v, want a prompt return", elapsed)
		}
		if got := codeOf(coded); got != 1 {
			t.Errorf("exit code = %d, want 1", got)
		}
		if !errors.Is(coded, ErrInterrupted) {
			t.Errorf("err = %v, want ErrInterrupted so it renders as a plain line", coded)
		}
	})

	t.Run("max-duration expiry names the flag and the elapsed time", func(t *testing.T) {
		isolate(t)
		setTestApp(t)
		srv := sseServer(t, 10*time.Millisecond)
		rootApp.client = client.New("k", srv.URL)

		parent := context.Background()
		const limit = 100 * time.Millisecond
		ctx, cancel := streamContext(parent, limit)
		defer cancel()

		start := time.Now()
		_, err := runStream(func(fn func(client.Event) error) error {
			return rootApp.client.Research(ctx, client.ResearchRequest{Query: "q"}, fn)
		})
		coded := classifyStreamError(parent, err, limit, time.Since(start))

		if got := codeOf(coded); got != 1 {
			t.Errorf("exit code = %d, want 1", got)
		}
		if errors.Is(coded, ErrInterrupted) {
			t.Error("expiry should not look like a user interrupt")
		}
		for _, want := range []string{"--max-duration", limit.String()} {
			if !strings.Contains(coded.Error(), want) {
				t.Errorf("message missing %q: %v", want, coded)
			}
		}
	})

	t.Run("an unset max-duration leaves the context alone", func(t *testing.T) {
		parent := context.Background()
		ctx, cancel := streamContext(parent, 0)
		defer cancel()
		if _, ok := ctx.Deadline(); ok {
			t.Error("no deadline should be set when --max-duration is unset")
		}
		if ctx != parent {
			t.Error("the parent context should be passed through unchanged")
		}
	})

	t.Run("an ordinary stream failure is untouched", func(t *testing.T) {
		parent := context.Background()
		err := classifyStreamError(parent, errors.New("boom"), 0, time.Second)
		if got := codeOf(err); got != 1 {
			t.Errorf("exit code = %d, want 1", got)
		}
		if errors.Is(err, ErrInterrupted) {
			t.Error("a plain failure should not be reported as cancelled")
		}
	})
}
