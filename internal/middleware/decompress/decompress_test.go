package decompress

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/wudi/gateway/config"
)

func compressGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func compressDeflate(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func compressBrotli(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func compressZstd(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecompressGzip(t *testing.T) {
	original := []byte(`{"message":"hello world"}`)
	compressed := compressGzip(t, original)

	d := New(config.RequestDecompressionConfig{Enabled: true})

	r := httptest.NewRequest("POST", "/api", bytes.NewReader(compressed))
	r.Header.Set("Content-Encoding", "gzip")
	r.Header.Set("Content-Length", "999")

	algo, ok := d.ShouldDecompress(r)
	if !ok {
		t.Fatal("expected ShouldDecompress to return true")
	}
	if algo != "gzip" {
		t.Fatalf("expected algo gzip, got %s", algo)
	}

	if err := d.Decompress(r, algo); err != nil {
		t.Fatal(err)
	}

	// Content-Encoding should be removed
	if r.Header.Get("Content-Encoding") != "" {
		t.Error("Content-Encoding header should be removed")
	}
	if r.Header.Get("Content-Length") != "" {
		t.Error("Content-Length header should be removed")
	}
	if r.ContentLength != -1 {
		t.Errorf("ContentLength should be -1, got %d", r.ContentLength)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()

	if !bytes.Equal(body, original) {
		t.Errorf("decompressed body mismatch: got %q, want %q", body, original)
	}

	// Check metrics
	stats := d.Stats()
	if stats.TotalRequests != 1 {
		t.Errorf("expected 1 total request, got %d", stats.TotalRequests)
	}
	if stats.Decompressed != 1 {
		t.Errorf("expected 1 decompressed, got %d", stats.Decompressed)
	}
	if stats.AlgorithmCount["gzip"] != 1 {
		t.Errorf("expected gzip count 1, got %d", stats.AlgorithmCount["gzip"])
	}
}

func TestDecompressDeflate(t *testing.T) {
	original := []byte(`deflate test data`)
	compressed := compressDeflate(t, original)

	d := New(config.RequestDecompressionConfig{Enabled: true})

	r := httptest.NewRequest("POST", "/api", bytes.NewReader(compressed))
	r.Header.Set("Content-Encoding", "deflate")

	algo, ok := d.ShouldDecompress(r)
	if !ok || algo != "deflate" {
		t.Fatal("expected deflate")
	}

	if err := d.Decompress(r, algo); err != nil {
		t.Fatal(err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()

	if !bytes.Equal(body, original) {
		t.Errorf("mismatch: got %q, want %q", body, original)
	}
}

func TestDecompressBrotli(t *testing.T) {
	original := []byte(`brotli test data`)
	compressed := compressBrotli(t, original)

	d := New(config.RequestDecompressionConfig{Enabled: true})

	r := httptest.NewRequest("POST", "/api", bytes.NewReader(compressed))
	r.Header.Set("Content-Encoding", "br")

	algo, ok := d.ShouldDecompress(r)
	if !ok || algo != "br" {
		t.Fatal("expected br")
	}

	if err := d.Decompress(r, algo); err != nil {
		t.Fatal(err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()

	if !bytes.Equal(body, original) {
		t.Errorf("mismatch: got %q, want %q", body, original)
	}
}

func TestDecompressZstd(t *testing.T) {
	original := []byte(`zstd test data`)
	compressed := compressZstd(t, original)

	d := New(config.RequestDecompressionConfig{Enabled: true})

	r := httptest.NewRequest("POST", "/api", bytes.NewReader(compressed))
	r.Header.Set("Content-Encoding", "zstd")

	algo, ok := d.ShouldDecompress(r)
	if !ok || algo != "zstd" {
		t.Fatal("expected zstd")
	}

	if err := d.Decompress(r, algo); err != nil {
		t.Fatal(err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()

	if !bytes.Equal(body, original) {
		t.Errorf("mismatch: got %q, want %q", body, original)
	}
}

func TestNoContentEncoding(t *testing.T) {
	d := New(config.RequestDecompressionConfig{Enabled: true})

	r := httptest.NewRequest("POST", "/api", strings.NewReader("plain body"))

	_, ok := d.ShouldDecompress(r)
	if ok {
		t.Error("should not decompress without Content-Encoding")
	}
}

func TestUnsupportedEncoding(t *testing.T) {
	d := New(config.RequestDecompressionConfig{
		Enabled:    true,
		Algorithms: []string{"gzip"},
	})

	r := httptest.NewRequest("POST", "/api", strings.NewReader("data"))
	r.Header.Set("Content-Encoding", "br")

	_, ok := d.ShouldDecompress(r)
	if ok {
		t.Error("should not decompress unsupported algorithm")
	}
}

func TestMaxDecompressedSize(t *testing.T) {
	// Create data larger than the limit
	original := bytes.Repeat([]byte("A"), 2000)
	compressed := compressGzip(t, original)

	d := New(config.RequestDecompressionConfig{
		Enabled:             true,
		MaxDecompressedSize: 1000, // 1KB limit
	})

	r := httptest.NewRequest("POST", "/api", bytes.NewReader(compressed))
	r.Header.Set("Content-Encoding", "gzip")

	algo, _ := d.ShouldDecompress(r)
	if err := d.Decompress(r, algo); err != nil {
		t.Fatal(err) // decompress itself succeeds; reading will fail
	}

	_, err := io.ReadAll(r.Body)
	if err == nil {
		t.Error("expected error reading oversized decompressed body")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMiddlewareIntegration(t *testing.T) {
	original := []byte(`{"key":"value"}`)
	compressed := compressGzip(t, original)

	d := New(config.RequestDecompressionConfig{Enabled: true})

	var receivedBody []byte
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("inner handler read error: %v", err)
		}
		w.WriteHeader(200)
	})

	// Simulate the middleware
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if algo, ok := d.ShouldDecompress(r); ok {
			if err := d.Decompress(r, algo); err != nil {
				http.Error(w, "decompression failed", http.StatusBadRequest)
				return
			}
		}
		inner.ServeHTTP(w, r)
	})

	r := httptest.NewRequest("POST", "/api", bytes.NewReader(compressed))
	r.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, r)

	if rr.Code != 200 {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !bytes.Equal(receivedBody, original) {
		t.Errorf("inner handler got %q, want %q", receivedBody, original)
	}
}

func TestDecompressorByRoute(t *testing.T) {
	mgr := NewDecompressorByRoute()

	mgr.AddRoute("api", config.RequestDecompressionConfig{
		Enabled:    true,
		Algorithms: []string{"gzip", "br"},
	})
	mgr.AddRoute("upload", config.RequestDecompressionConfig{
		Enabled:             true,
		MaxDecompressedSize: 100 << 20, // 100MB
	})

	if d := mgr.GetDecompressor("api"); d == nil {
		t.Error("expected non-nil decompressor for 'api'")
	}
	if d := mgr.GetDecompressor("missing"); d != nil {
		t.Error("expected nil for missing route")
	}

	ids := mgr.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := mgr.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats entries, got %d", len(stats))
	}
}

func TestMergeDecompressionConfig(t *testing.T) {
	global := config.RequestDecompressionConfig{
		Enabled:             true,
		Algorithms:          []string{"gzip", "br"},
		MaxDecompressedSize: 50 << 20,
	}

	// Per-route overrides algorithms
	perRoute := config.RequestDecompressionConfig{
		Enabled:    true,
		Algorithms: []string{"zstd"},
	}

	merged := MergeDecompressionConfig(perRoute, global)
	if len(merged.Algorithms) != 1 || merged.Algorithms[0] != "zstd" {
		t.Errorf("expected algorithms [zstd], got %v", merged.Algorithms)
	}
	if merged.MaxDecompressedSize != 50<<20 {
		t.Errorf("expected max size from global, got %d", merged.MaxDecompressedSize)
	}

	// Per-route overrides max size
	perRoute2 := config.RequestDecompressionConfig{
		Enabled:             true,
		MaxDecompressedSize: 100 << 20,
	}
	merged2 := MergeDecompressionConfig(perRoute2, global)
	if merged2.MaxDecompressedSize != 100<<20 {
		t.Errorf("expected max size 100MB, got %d", merged2.MaxDecompressedSize)
	}
}

func TestDefaultAlgorithms(t *testing.T) {
	d := New(config.RequestDecompressionConfig{Enabled: true})

	for _, algo := range []string{"gzip", "deflate", "br", "zstd"} {
		if !d.algorithms[algo] {
			t.Errorf("expected default algorithm %s to be enabled", algo)
		}
	}
}

func TestCaseInsensitiveEncoding(t *testing.T) {
	d := New(config.RequestDecompressionConfig{Enabled: true})

	original := []byte("test")
	compressed := compressGzip(t, original)

	r := httptest.NewRequest("POST", "/api", bytes.NewReader(compressed))
	r.Header.Set("Content-Encoding", "GZIP")

	algo, ok := d.ShouldDecompress(r)
	if !ok {
		t.Error("should handle case-insensitive Content-Encoding")
	}
	if algo != "gzip" {
		t.Errorf("expected normalized algo 'gzip', got %q", algo)
	}
}
