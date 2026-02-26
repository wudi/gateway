package grpc

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestNDJSONWriterWriteMessage(t *testing.T) {
	tests := []struct {
		name     string
		fields   map[string]interface{}
		contains []string
	}{
		{
			name:     "string field",
			fields:   map[string]interface{}{"key": "value"},
			contains: []string{`"key":"value"`},
		},
		{
			name:     "numeric field",
			fields:   map[string]interface{}{"count": float64(42)},
			contains: []string{`"count":42`},
		},
		{
			name:     "boolean field",
			fields:   map[string]interface{}{"ok": true},
			contains: []string{`"ok":true`},
		},
		{
			name:     "multiple fields",
			fields:   map[string]interface{}{"a": "x", "b": float64(1)},
			contains: []string{`"a":"x"`, `"b":1`},
		},
		{
			name:     "nested struct",
			fields:   map[string]interface{}{"outer": map[string]interface{}{"inner": "deep"}},
			contains: []string{`"outer"`, `"inner":"deep"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			w, err := newNDJSONWriter(rec)
			if err != nil {
				t.Fatalf("newNDJSONWriter: %v", err)
			}

			msg, err := structpb.NewStruct(tt.fields)
			if err != nil {
				t.Fatalf("NewStruct: %v", err)
			}

			if err := w.WriteMessage(msg); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			body := rec.Body.String()
			if !strings.HasSuffix(body, "\n") {
				t.Errorf("body should end with newline, got %q", body)
			}
			for _, want := range tt.contains {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q; got %q", want, body)
				}
			}
		})
	}
}

func TestNDJSONWriterWriteError(t *testing.T) {
	tests := []struct {
		name        string
		code        codes.Code
		message     string
		wantCode    string
		wantMessage string
	}{
		{
			name:        "not found",
			code:        codes.NotFound,
			message:     "resource missing",
			wantCode:    `"code":5`,
			wantMessage: `"message":"resource missing"`,
		},
		{
			name:        "internal",
			code:        codes.Internal,
			message:     "server error",
			wantCode:    `"code":13`,
			wantMessage: `"message":"server error"`,
		},
		{
			name:        "ok code",
			code:        codes.OK,
			message:     "",
			wantCode:    `"code":0`,
			wantMessage: `"message":""`,
		},
		{
			name:        "invalid argument with special chars",
			code:        codes.InvalidArgument,
			message:     `field "name" is required`,
			wantCode:    `"code":3`,
			wantMessage: `"message":"field \"name\" is required"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			w, err := newNDJSONWriter(rec)
			if err != nil {
				t.Fatalf("newNDJSONWriter: %v", err)
			}

			if err := w.WriteError(tt.code, tt.message); err != nil {
				t.Fatalf("WriteError: %v", err)
			}

			body := rec.Body.String()
			if !strings.HasSuffix(body, "\n") {
				t.Errorf("body should end with newline, got %q", body)
			}
			if !strings.Contains(body, tt.wantCode) {
				t.Errorf("body missing code %q; got %q", tt.wantCode, body)
			}
			if !strings.Contains(body, tt.wantMessage) {
				t.Errorf("body missing message %q; got %q", tt.wantMessage, body)
			}
			if !strings.Contains(body, `"error"`) {
				t.Errorf("body missing error wrapper; got %q", body)
			}
		})
	}
}

func TestNDJSONWriterNonFlusher(t *testing.T) {
	w := &nonFlushingWriter{}
	_, err := newNDJSONWriter(w)
	if err == nil {
		t.Fatal("expected error for non-flushing writer, got nil")
	}
	if !strings.Contains(err.Error(), "flushing") {
		t.Errorf("error should mention flushing, got %q", err.Error())
	}
}

func TestNDJSONReaderReadMessage(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantKey   string
		wantValue string
	}{
		{
			name:      "simple object",
			input:     `{"key":"value"}` + "\n",
			wantKey:   "key",
			wantValue: "value",
		},
		{
			name:      "object without trailing newline",
			input:     `{"key":"value"}`,
			wantKey:   "key",
			wantValue: "value",
		},
		{
			name:      "empty lines before data",
			input:     "\n\n" + `{"key":"value"}` + "\n",
			wantKey:   "key",
			wantValue: "value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newNDJSONReader(strings.NewReader(tt.input))
			msg := &structpb.Struct{}
			if err := r.ReadMessage(msg); err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			v, ok := msg.Fields[tt.wantKey]
			if !ok {
				t.Fatalf("missing field %q in %v", tt.wantKey, msg.Fields)
			}
			if got := v.GetStringValue(); got != tt.wantValue {
				t.Errorf("field %q = %q, want %q", tt.wantKey, got, tt.wantValue)
			}
		})
	}
}

func TestNDJSONReaderEmptyInput(t *testing.T) {
	r := newNDJSONReader(strings.NewReader(""))
	msg := &structpb.Struct{}
	err := r.ReadMessage(msg)
	if err != io.EOF {
		t.Errorf("expected io.EOF on empty input, got %v", err)
	}
}

func TestNDJSONReaderSkipsEmptyLines(t *testing.T) {
	input := "\n\n" + `{"a":"1"}` + "\n\n\n" + `{"a":"2"}` + "\n\n"
	r := newNDJSONReader(strings.NewReader(input))

	var values []string
	for {
		msg := &structpb.Struct{}
		err := r.ReadMessage(msg)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if v, ok := msg.Fields["a"]; ok {
			values = append(values, v.GetStringValue())
		}
	}

	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d: %v", len(values), values)
	}
	if values[0] != "1" || values[1] != "2" {
		t.Errorf("values = %v, want [1 2]", values)
	}
}

func TestNDJSONReaderErrNilOnCleanRead(t *testing.T) {
	input := `{"x":"y"}` + "\n"
	r := newNDJSONReader(strings.NewReader(input))

	msg := &structpb.Struct{}
	if err := r.ReadMessage(msg); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	// Read until EOF
	_ = r.ReadMessage(&structpb.Struct{})

	if err := r.Err(); err != nil {
		t.Errorf("Err() = %v after clean read, want nil", err)
	}
}

func TestNDJSONReaderInvalidJSON(t *testing.T) {
	input := `{this is not valid json}` + "\n"
	r := newNDJSONReader(strings.NewReader(input))
	msg := &structpb.Struct{}
	err := r.ReadMessage(msg)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse JSON") {
		t.Errorf("error should mention JSON parsing, got %q", err.Error())
	}
}

func TestNDJSONReaderOnlyEmptyLines(t *testing.T) {
	input := "\n\n\n"
	r := newNDJSONReader(strings.NewReader(input))
	msg := &structpb.Struct{}
	err := r.ReadMessage(msg)
	if err != io.EOF {
		t.Errorf("expected io.EOF for only-empty-lines input, got %v", err)
	}
}
