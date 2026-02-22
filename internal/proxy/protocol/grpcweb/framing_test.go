package grpcweb

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

func TestEncodeDecodeDataFrame(t *testing.T) {
	payload := []byte("hello world")
	frame := encodeDataFrame(payload)

	decoded, err := decodeGRPCWebFrame(bytes.NewReader(frame), 0)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.Flag != flagData {
		t.Errorf("expected flag 0x00, got 0x%02x", decoded.Flag)
	}
	if !bytes.Equal(decoded.Payload, payload) {
		t.Errorf("payload mismatch: got %q, want %q", decoded.Payload, payload)
	}
	if decoded.isTrailer() {
		t.Error("data frame should not be trailer")
	}
}

func TestEncodeDecodeTrailerFrame(t *testing.T) {
	trailers := map[string]string{
		"grpc-status":  "0",
		"grpc-message": "OK",
	}
	frame := encodeTrailerFrame(trailers)

	decoded, err := decodeGRPCWebFrame(bytes.NewReader(frame), 0)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.Flag != flagTrailer {
		t.Errorf("expected flag 0x80, got 0x%02x", decoded.Flag)
	}
	if !decoded.isTrailer() {
		t.Error("trailer frame should be trailer")
	}

	parsed := parseTrailerFrame(decoded.Payload)
	if parsed["grpc-status"] != "0" {
		t.Errorf("grpc-status: got %q, want %q", parsed["grpc-status"], "0")
	}
	if parsed["grpc-message"] != "OK" {
		t.Errorf("grpc-message: got %q, want %q", parsed["grpc-message"], "OK")
	}
}

func TestEncodeDecodeEmptyDataFrame(t *testing.T) {
	frame := encodeDataFrame(nil)
	decoded, err := decodeGRPCWebFrame(bytes.NewReader(frame), 0)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.Flag != flagData {
		t.Errorf("expected flag 0x00, got 0x%02x", decoded.Flag)
	}
	if len(decoded.Payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(decoded.Payload))
	}
}

func TestDecodeMaxMessageSize(t *testing.T) {
	// Create a frame claiming a large payload.
	payload := make([]byte, 1024)
	frame := encodeDataFrame(payload)

	// Decode with a small max size should fail.
	_, err := decodeGRPCWebFrame(bytes.NewReader(frame), 512)
	if err == nil {
		t.Error("expected error for oversized message")
	}

	// Decode with sufficient max size should succeed.
	decoded, err := decodeGRPCWebFrame(bytes.NewReader(frame), 2048)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(decoded.Payload) != 1024 {
		t.Errorf("expected 1024 bytes, got %d", len(decoded.Payload))
	}
}

func TestDecodeFrameTruncated(t *testing.T) {
	// Only 3 bytes (less than header size).
	_, err := decodeGRPCWebFrame(bytes.NewReader([]byte{0, 0, 0}), 0)
	if err == nil {
		t.Error("expected error for truncated header")
	}

	// Valid header but truncated payload.
	frame := encodeDataFrame([]byte("hello"))
	_, err = decodeGRPCWebFrame(bytes.NewReader(frame[:frameHeaderSize+2]), 0)
	if err == nil {
		t.Error("expected error for truncated payload")
	}
}

func TestDecodeEmptyReader(t *testing.T) {
	_, err := decodeGRPCWebFrame(bytes.NewReader(nil), 0)
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestIsGRPCWebContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"application/grpc-web", true},
		{"application/grpc-web+proto", true},
		{"application/grpc-web-text", true},
		{"application/grpc-web-text+proto", true},
		{"application/grpc-web; charset=utf-8", true},
		{"application/grpc", false},
		{"application/json", false},
		{"text/plain", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isGRPCWebContentType(tt.ct); got != tt.want {
			t.Errorf("isGRPCWebContentType(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}

func TestIsTextMode(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"application/grpc-web-text", true},
		{"application/grpc-web-text+proto", true},
		{"application/grpc-web-text; charset=utf-8", true},
		{"application/grpc-web", false},
		{"application/grpc-web+proto", false},
	}
	for _, tt := range tests {
		if got := isTextMode(tt.ct); got != tt.want {
			t.Errorf("isTextMode(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}

func TestIsGRPCWebRequest(t *testing.T) {
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	if !isGRPCWebRequest(req) {
		t.Error("expected grpc-web request to be detected")
	}

	req.Header.Set("Content-Type", "application/json")
	if isGRPCWebRequest(req) {
		t.Error("json request should not be grpc-web")
	}
}

func TestBase64RoundTrip(t *testing.T) {
	original := []byte("hello grpc-web text mode")
	encoded := base64Encode(original)
	decoded, err := base64Decode(encoded)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if !bytes.Equal(decoded, original) {
		t.Errorf("round-trip mismatch: got %q, want %q", decoded, original)
	}
}

func TestBase64DecodeInvalid(t *testing.T) {
	_, err := base64Decode([]byte("!!!not-base64!!!"))
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestParseTrailerFrameEmpty(t *testing.T) {
	trailers := parseTrailerFrame([]byte(""))
	if len(trailers) != 0 {
		t.Errorf("expected empty trailers, got %v", trailers)
	}
}

func TestMultipleFrames(t *testing.T) {
	// Write two data frames followed by a trailer frame into a buffer.
	var buf bytes.Buffer
	buf.Write(encodeDataFrame([]byte("msg1")))
	buf.Write(encodeDataFrame([]byte("msg2")))
	buf.Write(encodeTrailerFrame(map[string]string{"grpc-status": "0"}))

	// Read all three frames.
	f1, err := decodeGRPCWebFrame(&buf, 0)
	if err != nil {
		t.Fatalf("frame 1: %v", err)
	}
	if string(f1.Payload) != "msg1" {
		t.Errorf("frame 1 payload: got %q, want %q", f1.Payload, "msg1")
	}

	f2, err := decodeGRPCWebFrame(&buf, 0)
	if err != nil {
		t.Fatalf("frame 2: %v", err)
	}
	if string(f2.Payload) != "msg2" {
		t.Errorf("frame 2 payload: got %q, want %q", f2.Payload, "msg2")
	}

	f3, err := decodeGRPCWebFrame(&buf, 0)
	if err != nil {
		t.Fatalf("frame 3: %v", err)
	}
	if !f3.isTrailer() {
		t.Error("frame 3 should be trailer")
	}
}

func TestLargePayload(t *testing.T) {
	payload := make([]byte, 100000)
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	frame := encodeDataFrame(payload)
	decoded, err := decodeGRPCWebFrame(bytes.NewReader(frame), 0)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !bytes.Equal(decoded.Payload, payload) {
		t.Error("large payload round-trip mismatch")
	}
}
