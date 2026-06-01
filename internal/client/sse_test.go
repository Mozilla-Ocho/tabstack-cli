package client

import (
	"errors"
	"strings"
	"testing"
)

// collect runs ParseSSE over s and returns every event it emits.
func collect(t *testing.T, s string) []Event {
	t.Helper()
	var got []Event
	err := ParseSSE(strings.NewReader(s), func(e Event) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatalf("ParseSSE: %v", err)
	}
	return got
}

func TestParseSSEBasic(t *testing.T) {
	stream := "event: agent:processing\ndata: {\"operation\":\"plan\"}\n\n" +
		"event: complete\ndata: {\"success\":true}\n\n"

	got := collect(t, stream)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Name != "agent:processing" {
		t.Errorf("event[0].Name = %q", got[0].Name)
	}
	if string(got[0].Data) != `{"operation":"plan"}` {
		t.Errorf("event[0].Data = %q", got[0].Data)
	}
	if got[1].Name != "complete" {
		t.Errorf("event[1].Name = %q", got[1].Name)
	}
}

func TestParseSSEMultipleDataLines(t *testing.T) {
	// Per spec, multiple data lines join with newlines.
	stream := "event: msg\ndata: line1\ndata: line2\n\n"
	got := collect(t, stream)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if string(got[0].Data) != "line1\nline2" {
		t.Errorf("Data = %q, want joined", got[0].Data)
	}
}

func TestParseSSETrimsPayload(t *testing.T) {
	// The parser strips one leading space per the SSE spec, and flush trims any
	// remaining surrounding whitespace, so "data:  x " yields "x".
	got := collect(t, "data:  x \n\n")
	if string(got[0].Data) != "x" {
		t.Errorf("Data = %q, want trimmed", got[0].Data)
	}
}

func TestParseSSEIgnoresComments(t *testing.T) {
	stream := ": this is a comment\nevent: ping\ndata: 1\n\n"
	got := collect(t, stream)
	if len(got) != 1 || got[0].Name != "ping" {
		t.Fatalf("comment not ignored: %+v", got)
	}
}

func TestParseSSETrailingEventNoBlankLine(t *testing.T) {
	// A final frame not terminated by a blank line must still flush.
	got := collect(t, "event: done\ndata: bye")
	if len(got) != 1 || got[0].Name != "done" {
		t.Fatalf("trailing event lost: %+v", got)
	}
}

func TestParseSSESkipsEmptySeparators(t *testing.T) {
	// Leading/duplicate blank lines should not produce phantom events.
	got := collect(t, "\n\nevent: x\ndata: 1\n\n\n")
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
}

func TestParseSSEFieldWithoutColon(t *testing.T) {
	// A bare "data" line is a field with an empty value.
	got := collect(t, "event: x\ndata\n\n")
	if len(got) != 1 {
		t.Fatalf("got %d events", len(got))
	}
	if string(got[0].Data) != "" {
		t.Errorf("Data = %q, want empty", got[0].Data)
	}
}

func TestParseSSEPropagatesCallbackError(t *testing.T) {
	sentinel := errors.New("stop")
	err := ParseSSE(strings.NewReader("event: a\ndata: 1\n\nevent: b\ndata: 2\n\n"),
		func(e Event) error {
			return sentinel
		})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}

func TestParseSSELargePayload(t *testing.T) {
	// Exceed the default 64KB scanner token limit to exercise the 4MB buffer.
	big := strings.Repeat("a", 200*1024)
	got := collect(t, "event: big\ndata: "+big+"\n\n")
	if len(got) != 1 {
		t.Fatalf("got %d events", len(got))
	}
	if len(got[0].Data) != len(big) {
		t.Errorf("Data len = %d, want %d", len(got[0].Data), len(big))
	}
}

func TestEventDecode(t *testing.T) {
	e := Event{Name: "x", Data: []byte(`{"operation":"go","n":3}`)}
	var d struct {
		Operation string `json:"operation"`
		N         int    `json:"n"`
	}
	if err := e.Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Operation != "go" || d.N != 3 {
		t.Errorf("decoded = %+v", d)
	}
	if e.DataString() != `{"operation":"go","n":3}` {
		t.Errorf("DataString = %q", e.DataString())
	}
}
