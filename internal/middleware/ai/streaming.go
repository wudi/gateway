package ai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// streamResponse reads SSE events from the provider, translates them to
// OpenAI-compatible format, and flushes each event individually.
func streamResponse(w http.ResponseWriter, providerResp *http.Response, provider Provider, idleTimeout time.Duration) (usage *Usage, err error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("response writer does not support flushing")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	scanner := bufio.NewScanner(providerResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024) // up to 256KB per line

	var eventType string
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()

	// Channel-based scanner for idle timeout
	type scanResult struct {
		line string
		ok   bool
	}
	lines := make(chan scanResult, 1)

	go func() {
		for scanner.Scan() {
			lines <- scanResult{line: scanner.Text(), ok: true}
		}
		lines <- scanResult{ok: false}
	}()

	for {
		timer.Reset(idleTimeout)
		select {
		case <-timer.C:
			// Write error event and close
			fmt.Fprintf(w, "data: {\"error\":{\"type\":\"idle_timeout\",\"message\":\"no event received within %s\"}}\n\n", idleTimeout)
			flusher.Flush()
			return usage, fmt.Errorf("stream idle timeout after %s", idleTimeout)

		case result := <-lines:
			if !result.ok {
				// Scanner done — end of stream
				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
				return usage, scanner.Err()
			}

			line := result.line

			// Track event type lines (Anthropic uses these)
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}

			if !strings.HasPrefix(line, "data:") {
				// Empty lines or comments — skip
				if line == "" {
					eventType = "" // reset after event boundary
				}
				continue
			}

			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}

			evt, parseErr := provider.ParseStreamEvent(eventType, []byte(data))
			if parseErr == io.EOF {
				// Stream done
				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
				return usage, nil
			}
			if parseErr != nil {
				// Write error event
				errJSON, _ := json.Marshal(map[string]any{
					"error": map[string]string{
						"type":    "provider_parse_error",
						"message": parseErr.Error(),
					},
				})
				fmt.Fprintf(w, "data: %s\n\n", errJSON)
				flusher.Flush()
				return usage, parseErr
			}
			if evt == nil {
				// Skip signal (provider metadata events)
				continue
			}

			// Track usage from the final event
			if evt.Usage != nil {
				usage = evt.Usage
			}

			// Marshal to OpenAI-compatible SSE
			outJSON, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", outJSON)
			flusher.Flush()
		}
	}
}
