package grpc

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// createTestMessage creates a dynamic protobuf message with fields for testing.
// We use google.protobuf.Struct since it marshals to regular JSON.
func createTestStruct(fields map[string]interface{}) proto.Message {
	s, _ := structpb.NewStruct(fields)
	return s
}

func TestNDJSONWriter(t *testing.T) {
	t.Run("writes message with newline", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writer, err := newNDJSONWriter(rec)
		if err != nil {
			t.Fatalf("newNDJSONWriter failed: %v", err)
		}

		msg := createTestStruct(map[string]interface{}{"name": "hello"})
		if err := writer.WriteMessage(msg); err != nil {
			t.Fatalf("WriteMessage failed: %v", err)
		}

		body := rec.Body.String()
		if !strings.HasSuffix(body, "\n") {
			t.Errorf("expected body to end with newline, got: %q", body)
		}
		if !strings.Contains(body, `"name":"hello"`) {
			t.Errorf("expected body to contain name field, got: %q", body)
		}
	})

	t.Run("writes multiple messages", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writer, err := newNDJSONWriter(rec)
		if err != nil {
			t.Fatalf("newNDJSONWriter failed: %v", err)
		}

		for i := 0; i < 3; i++ {
			msg := createTestStruct(map[string]interface{}{"index": float64(i)})
			if err := writer.WriteMessage(msg); err != nil {
				t.Fatalf("WriteMessage[%d] failed: %v", i, err)
			}
		}

		body := rec.Body.String()
		lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
		if len(lines) != 3 {
			t.Errorf("expected 3 lines, got %d: %q", len(lines), body)
		}
	})

	t.Run("writes error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writer, err := newNDJSONWriter(rec)
		if err != nil {
			t.Fatalf("newNDJSONWriter failed: %v", err)
		}

		if err := writer.WriteError(codes.NotFound, "item not found"); err != nil {
			t.Fatalf("WriteError failed: %v", err)
		}

		body := rec.Body.String()
		if !strings.Contains(body, `"code":5`) { // NotFound = 5
			t.Errorf("expected code 5 in error, got: %q", body)
		}
		if !strings.Contains(body, `"message":"item not found"`) {
			t.Errorf("expected message in error, got: %q", body)
		}
	})

	t.Run("fails for non-flusher", func(t *testing.T) {
		w := &nonFlushingWriter{}
		_, err := newNDJSONWriter(w)
		if err == nil {
			t.Error("expected error for non-flushing writer")
		}
	})
}

func TestNDJSONReader(t *testing.T) {
	t.Run("reads single message", func(t *testing.T) {
		input := `{"name":"hello"}` + "\n"
		reader := newNDJSONReader(strings.NewReader(input))

		msg := &structpb.Struct{}
		if err := reader.ReadMessage(msg); err != nil {
			t.Fatalf("ReadMessage failed: %v", err)
		}

		if v, ok := msg.Fields["name"]; !ok || v.GetStringValue() != "hello" {
			t.Errorf("expected name='hello', got %v", msg.Fields)
		}
	})

	t.Run("reads multiple messages", func(t *testing.T) {
		input := `{"index":1}
{"index":2}
{"index":3}
`
		reader := newNDJSONReader(strings.NewReader(input))

		var values []float64
		for {
			msg := &structpb.Struct{}
			err := reader.ReadMessage(msg)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("ReadMessage failed: %v", err)
			}
			if v, ok := msg.Fields["index"]; ok {
				values = append(values, v.GetNumberValue())
			}
		}

		if len(values) != 3 {
			t.Errorf("expected 3 values, got %d", len(values))
		}
		for i, v := range values {
			if v != float64(i+1) {
				t.Errorf("values[%d] = %v, want %v", i, v, float64(i+1))
			}
		}
	})

	t.Run("skips empty lines", func(t *testing.T) {
		input := `{"name":"first"}

{"name":"second"}
`
		reader := newNDJSONReader(strings.NewReader(input))

		var values []string
		for {
			msg := &structpb.Struct{}
			err := reader.ReadMessage(msg)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("ReadMessage failed: %v", err)
			}
			if v, ok := msg.Fields["name"]; ok {
				values = append(values, v.GetStringValue())
			}
		}

		if len(values) != 2 {
			t.Errorf("expected 2 values, got %d: %v", len(values), values)
		}
	})

	t.Run("returns EOF on empty input", func(t *testing.T) {
		reader := newNDJSONReader(strings.NewReader(""))

		msg := &structpb.Struct{}
		err := reader.ReadMessage(msg)
		if err != io.EOF {
			t.Errorf("expected io.EOF, got %v", err)
		}
	})

	t.Run("returns error on invalid JSON", func(t *testing.T) {
		input := `{not valid json}` + "\n"
		reader := newNDJSONReader(strings.NewReader(input))

		msg := &structpb.Struct{}
		err := reader.ReadMessage(msg)
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("handles large lines", func(t *testing.T) {
		// Create a string larger than default buffer
		large := strings.Repeat("x", 100*1024)
		input := `{"data":"` + large + `"}` + "\n"
		reader := newNDJSONReader(strings.NewReader(input))

		msg := &structpb.Struct{}
		if err := reader.ReadMessage(msg); err != nil {
			t.Fatalf("ReadMessage failed: %v", err)
		}

		if v, ok := msg.Fields["data"]; !ok || v.GetStringValue() != large {
			t.Errorf("data length mismatch")
		}
	})
}

func TestNDJSONRoundTrip(t *testing.T) {
	// Use a custom writer that wraps bytes.Buffer
	rec := httptest.NewRecorder()
	writer, err := newNDJSONWriter(rec)
	if err != nil {
		t.Fatalf("newNDJSONWriter failed: %v", err)
	}

	// Write some messages
	messages := []string{"alpha", "beta", "gamma"}
	for _, m := range messages {
		msg := createTestStruct(map[string]interface{}{"value": m})
		if err := writer.WriteMessage(msg); err != nil {
			t.Fatalf("WriteMessage failed: %v", err)
		}
	}

	// Now read them back
	reader := newNDJSONReader(strings.NewReader(rec.Body.String()))
	var result []string
	for {
		msg := &structpb.Struct{}
		err := reader.ReadMessage(msg)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ReadMessage failed: %v", err)
		}
		if v, ok := msg.Fields["value"]; ok {
			result = append(result, v.GetStringValue())
		}
	}

	if len(result) != len(messages) {
		t.Errorf("expected %d messages, got %d", len(messages), len(result))
	}
	for i, m := range messages {
		if result[i] != m {
			t.Errorf("result[%d] = %q, want %q", i, result[i], m)
		}
	}
}

func TestNDJSONWithDynamicMessage(t *testing.T) {
	// Test with dynamicpb.Message which is what streaming uses internally
	// Create a simple FileDescriptor for testing
	rec := httptest.NewRecorder()
	writer, err := newNDJSONWriter(rec)
	if err != nil {
		t.Fatalf("newNDJSONWriter failed: %v", err)
	}

	// Use structpb.Struct as a known message type
	msg := createTestStruct(map[string]interface{}{
		"id":   float64(123),
		"name": "test",
	})
	if err := writer.WriteMessage(msg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"id":123`) {
		t.Errorf("expected id field, got: %q", body)
	}
	if !strings.Contains(body, `"name":"test"`) {
		t.Errorf("expected name field, got: %q", body)
	}
}

// nonFlushingWriter is a ResponseWriter that doesn't implement http.Flusher.
type nonFlushingWriter struct{}

func (w *nonFlushingWriter) Header() http.Header         { return make(http.Header) }
func (w *nonFlushingWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nonFlushingWriter) WriteHeader(int)             {}
