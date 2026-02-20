package sse

import (
	"strings"
)

// SSEEvent represents a parsed Server-Sent Event.
type SSEEvent struct {
	ID    string // event id (from "id:" field)
	Event string // event type (from "event:" field)
	Data  string // event data (from "data:" field(s), joined with newlines)
	Retry string // retry interval (from "retry:" field)
	Raw   []byte // the complete raw event bytes including trailing \n\n
}

// parseSSEEvent parses a raw SSE event block (terminated by \n\n) into an SSEEvent.
func parseSSEEvent(raw []byte) SSEEvent {
	evt := SSEEvent{Raw: raw}

	text := string(raw)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")

	var dataParts []string
	for _, line := range lines {
		if line == "" || line[0] == ':' {
			continue // comment or empty line
		}

		var field, value string
		if idx := strings.IndexByte(line, ':'); idx >= 0 {
			field = line[:idx]
			value = line[idx+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:] // strip leading space after colon
			}
		} else {
			field = line
		}

		switch field {
		case "id":
			evt.ID = value
		case "event":
			evt.Event = value
		case "data":
			dataParts = append(dataParts, value)
		case "retry":
			evt.Retry = value
		}
	}

	if len(dataParts) > 0 {
		evt.Data = strings.Join(dataParts, "\n")
	}

	return evt
}
