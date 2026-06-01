package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// Event is a single Server-Sent Event. The Tabstack streaming endpoints emit
// frames shaped like:
//
//	event: agent:processing
//	data: {"operation": "Creating task plan"}
//
// We capture the event name and the raw data payload. Data is kept as
// json.RawMessage because the shape varies per event type, and for the
// automate endpoint some fields are themselves JSON encoded as strings.
type Event struct {
	Name string
	Data json.RawMessage
}

// DataString returns the data payload as a plain string. Handy when the data
// is not JSON, or when you want the raw text before decoding.
func (e Event) DataString() string {
	return string(e.Data)
}

// Decode unmarshals the event data into v.
func (e Event) Decode(v any) error {
	return json.Unmarshal(e.Data, v)
}

// ParseSSE reads an event-stream from r and calls fn for every complete event.
// It returns when the stream ends (io.EOF) or fn returns a non-nil error.
//
// SSE frames are separated by a blank line. Within a frame, lines beginning
// with "event:" set the event name and lines beginning with "data:" append to
// the payload. Multiple data lines are joined with newlines per the spec.
// Lines starting with ":" are comments and ignored.
func ParseSSE(r io.Reader, fn func(Event) error) error {
	scanner := bufio.NewScanner(r)
	// SSE data lines (especially extracted page content) can be large, so give
	// the scanner room well beyond the default 64KB token limit.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var (
		name string
		data bytes.Buffer
	)

	flush := func() error {
		// Skip empty separators that do not delimit a real event.
		if name == "" && data.Len() == 0 {
			return nil
		}
		// Copy the payload: data.Bytes() points at the buffer's backing array,
		// which we reset and reuse for the next event. Without the copy, a
		// caller that retains Event.Data (as the SSE consumers do) would see it
		// overwritten by the following frame.
		trimmed := bytes.TrimSpace(data.Bytes())
		payload := make([]byte, len(trimmed))
		copy(payload, trimmed)

		evt := Event{
			Name: name,
			Data: json.RawMessage(payload),
		}
		name = ""
		data.Reset()
		return fn(evt)
	}

	for scanner.Scan() {
		line := scanner.Text()

		// Blank line marks the end of an event.
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}

		// Comment line.
		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, ok := strings.Cut(line, ":")
		if !ok {
			// A line with no colon is a field name with an empty value.
			field = line
			value = ""
		}
		// Per spec, a single leading space after the colon is stripped.
		value = strings.TrimPrefix(value, " ")

		switch field {
		case "event":
			name = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		case "id", "retry":
			// Not used by Tabstack, ignore.
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	// Flush a trailing event that was not terminated by a blank line.
	return flush()
}
