package cmd

import (
	"encoding/json"
	"testing"

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
