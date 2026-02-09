package compression

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestCompressorShouldCompress(t *testing.T) {
	c := New(config.CompressionConfig{
		Enabled: true,
	})

	tests := []struct {
		name     string
		encoding string
		want     bool
	}{
		{"gzip accepted", "gzip, deflate", true},
		{"no gzip", "deflate", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tt.encoding != "" {
				r.Header.Set("Accept-Encoding", tt.encoding)
			}

			if got := c.ShouldCompress(r); got != tt.want {
				t.Errorf("ShouldCompress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompressorDisabled(t *testing.T) {
	c := New(config.CompressionConfig{
		Enabled: false,
	})

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")

	if c.ShouldCompress(r) {
		t.Error("disabled compressor should not compress")
	}
}

func TestCompressingResponseWriter(t *testing.T) {
	c := New(config.CompressionConfig{
		Enabled: true,
		Level:   6,
		MinSize: 10, // Low threshold for testing
	})

	w := httptest.NewRecorder()
	cw := NewCompressingResponseWriter(w, c)

	cw.ResponseWriter.Header().Set("Content-Type", "application/json")
	cw.WriteHeader(http.StatusOK)

	// Write enough data to trigger compression
	data := strings.Repeat(`{"key":"value"}`, 100)
	cw.Write([]byte(data))
	cw.Close()

	// Check that gzip encoding was applied
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Error("expected gzip Content-Encoding")
	}

	// Verify the body is valid gzip
	gzReader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}

	if string(decompressed) != data {
		t.Errorf("decompressed data doesn't match original")
	}
}

func TestCompressingResponseWriterSmallBody(t *testing.T) {
	c := New(config.CompressionConfig{
		Enabled: true,
		MinSize: 1024, // Default threshold
	})

	w := httptest.NewRecorder()
	cw := NewCompressingResponseWriter(w, c)

	cw.ResponseWriter.Header().Set("Content-Type", "application/json")
	cw.WriteHeader(http.StatusOK)

	// Write small data that shouldn't be compressed
	cw.Write([]byte(`{"ok":true}`))
	cw.Close()

	if w.Header().Get("Content-Encoding") == "gzip" {
		t.Error("small body should not be compressed")
	}
}

func TestCompressorByRoute(t *testing.T) {
	m := NewCompressorByRoute()
	m.AddRoute("route1", config.CompressionConfig{
		Enabled: true,
		Level:   6,
	})

	c := m.GetCompressor("route1")
	if c == nil || !c.IsEnabled() {
		t.Fatal("expected compressor for route1")
	}

	if m.GetCompressor("unknown") != nil {
		t.Error("expected nil for unknown route")
	}
}
