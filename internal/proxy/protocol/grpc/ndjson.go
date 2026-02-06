package grpc

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ndjsonWriter streams JSON objects with newline delimiters to an HTTP response.
// It flushes after each message to enable real-time streaming.
type ndjsonWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex

	marshalOpts protojson.MarshalOptions
}

// newNDJSONWriter creates a new NDJSON writer.
// Returns an error if the ResponseWriter doesn't support flushing.
func newNDJSONWriter(w http.ResponseWriter) (*ndjsonWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("response writer does not support flushing")
	}

	return &ndjsonWriter{
		w:       w,
		flusher: flusher,
		marshalOpts: protojson.MarshalOptions{
			EmitUnpopulated: false,
			UseProtoNames:   true,
		},
	}, nil
}

// WriteMessage marshals a proto message to JSON and writes it as a single NDJSON line.
func (nw *ndjsonWriter) WriteMessage(msg proto.Message) error {
	nw.mu.Lock()
	defer nw.mu.Unlock()

	jsonBytes, err := nw.marshalOpts.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message to JSON: %w", err)
	}

	// Write JSON followed by newline
	if _, err := nw.w.Write(jsonBytes); err != nil {
		return err
	}
	if _, err := nw.w.Write([]byte("\n")); err != nil {
		return err
	}

	nw.flusher.Flush()
	return nil
}

// WriteError writes a JSON error as the final NDJSON line.
// This is used when an error occurs mid-stream.
func (nw *ndjsonWriter) WriteError(code codes.Code, message string) error {
	nw.mu.Lock()
	defer nw.mu.Unlock()

	errJSON := fmt.Sprintf(`{"error":{"code":%d,"message":%q}}`, code, message)
	if _, err := nw.w.Write([]byte(errJSON)); err != nil {
		return err
	}
	if _, err := nw.w.Write([]byte("\n")); err != nil {
		return err
	}

	nw.flusher.Flush()
	return nil
}

// ndjsonReader reads newline-delimited JSON from an io.Reader.
type ndjsonReader struct {
	scanner       *bufio.Scanner
	unmarshalOpts protojson.UnmarshalOptions
}

// newNDJSONReader creates a new NDJSON reader.
func newNDJSONReader(r io.Reader) *ndjsonReader {
	scanner := bufio.NewScanner(r)
	// Set a reasonable max line size (1MB)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	return &ndjsonReader{
		scanner: scanner,
		unmarshalOpts: protojson.UnmarshalOptions{
			DiscardUnknown: true,
		},
	}
}

// ReadMessage reads the next JSON line and unmarshals it into the proto message.
// Returns io.EOF when there are no more lines.
func (nr *ndjsonReader) ReadMessage(msg proto.Message) error {
	if !nr.scanner.Scan() {
		if err := nr.scanner.Err(); err != nil {
			return err
		}
		return io.EOF
	}

	line := nr.scanner.Bytes()
	if len(line) == 0 {
		// Skip empty lines and try next
		return nr.ReadMessage(msg)
	}

	if err := nr.unmarshalOpts.Unmarshal(line, msg); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	return nil
}

// Err returns any error that occurred during scanning.
func (nr *ndjsonReader) Err() error {
	return nr.scanner.Err()
}
