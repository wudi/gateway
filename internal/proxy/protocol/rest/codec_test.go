package rest

import (
	"bytes"
	"io"
	"testing"
)

func TestDecodeEncodeGRPCFrame(t *testing.T) {
	original := []byte("hello world")

	var buf bytes.Buffer
	if err := encodeGRPCFrame(&buf, original, false); err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, compressed, err := decodeGRPCFrame(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if compressed {
		t.Error("expected uncompressed frame")
	}
	if !bytes.Equal(decoded, original) {
		t.Errorf("decoded %q, want %q", decoded, original)
	}
}

func TestDecodeGRPCFrameCompressed(t *testing.T) {
	original := []byte("compressed data")

	var buf bytes.Buffer
	if err := encodeGRPCFrame(&buf, original, true); err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, compressed, err := decodeGRPCFrame(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !compressed {
		t.Error("expected compressed flag")
	}
	if !bytes.Equal(decoded, original) {
		t.Errorf("decoded %q, want %q", decoded, original)
	}
}

func TestDecodeGRPCFrameEmpty(t *testing.T) {
	var buf bytes.Buffer
	_, _, err := decodeGRPCFrame(&buf)
	if err != io.EOF {
		t.Errorf("expected EOF for empty reader, got %v", err)
	}
}

func TestDecodeGRPCFrameEmptyBody(t *testing.T) {
	var buf bytes.Buffer
	if err := encodeGRPCFrame(&buf, []byte{}, false); err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, _, err := decodeGRPCFrame(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("expected empty body, got %d bytes", len(decoded))
	}
}

func TestDecodeGRPCFrameTooLarge(t *testing.T) {
	// Craft a header claiming 100MB
	header := []byte{0, 0x06, 0x40, 0x00, 0x00} // ~100MB
	buf := bytes.NewReader(header)

	_, _, err := decodeGRPCFrame(buf)
	if err == nil {
		t.Error("expected error for oversized frame")
	}
}
