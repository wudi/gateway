package compression

import (
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

func TestNegotiateEncoding(t *testing.T) {
	tests := []struct {
		name       string
		algos      []string
		encoding   string
		wantAlgo   string
	}{
		{"gzip only config, gzip accepted", []string{"gzip"}, "gzip", "gzip"},
		{"br preferred by server", nil, "gzip, br", "br"},
		{"quality factors br higher", nil, "gzip;q=0.5, br;q=1.0", "br"},
		{"quality factors gzip higher", nil, "gzip;q=1.0, br;q=0.5", "gzip"},
		{"wildcard matches server pref", nil, "*", "br"},
		{"q=0 rejects gzip", []string{"gzip"}, "gzip;q=0", ""},
		{"restricted to zstd only", []string{"zstd"}, "br, zstd, gzip", "zstd"},
		{"empty header", nil, "", ""},
		{"no matching algo", []string{"br"}, "gzip", ""},
		{"wildcard with explicit reject", nil, "*, br;q=0", "zstd"},
		{"zstd preferred over gzip", nil, "zstd, gzip", "zstd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.CompressionConfig{
				Enabled:    true,
				Algorithms: tt.algos,
			}
			c := New(cfg)

			r := httptest.NewRequest("GET", "/", nil)
			if tt.encoding != "" {
				r.Header.Set("Accept-Encoding", tt.encoding)
			}

			got := c.NegotiateEncoding(r)
			if got != tt.wantAlgo {
				t.Errorf("NegotiateEncoding() = %q, want %q", got, tt.wantAlgo)
			}
		})
	}
}

func TestNegotiateEncoding_Disabled(t *testing.T) {
	c := New(config.CompressionConfig{Enabled: false})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	if got := c.NegotiateEncoding(r); got != "" {
		t.Errorf("disabled compressor should return empty, got %q", got)
	}
}

func TestParseAcceptEncoding(t *testing.T) {
	tests := []struct {
		header string
		want   []encodingPref
	}{
		{"gzip", []encodingPref{{"gzip", 1.0}}},
		{"gzip;q=0.5, br", []encodingPref{{"gzip", 0.5}, {"br", 1.0}}},
		{"", nil},
		{"*;q=0", []encodingPref{{"*", 0}}},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			got := parseAcceptEncoding(tt.header)
			if len(got) != len(tt.want) {
				t.Fatalf("parseAcceptEncoding(%q) = %d entries, want %d", tt.header, len(got), len(tt.want))
			}
			for i, g := range got {
				if g.encoding != tt.want[i].encoding || g.quality != tt.want[i].quality {
					t.Errorf("entry %d: got {%s %.1f}, want {%s %.1f}",
						i, g.encoding, g.quality, tt.want[i].encoding, tt.want[i].quality)
				}
			}
		})
	}
}

func TestCompressingResponseWriter_Gzip(t *testing.T) {
	c := New(config.CompressionConfig{
		Enabled: true,
		Level:   6,
		MinSize: 10,
	})

	w := httptest.NewRecorder()
	cw := NewCompressingResponseWriter(w, c, "gzip")

	cw.ResponseWriter.Header().Set("Content-Type", "application/json")
	cw.WriteHeader(http.StatusOK)

	data := strings.Repeat(`{"key":"value"}`, 100)
	cw.Write([]byte(data))
	cw.Close()

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Error("expected gzip Content-Encoding")
	}

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
		t.Error("decompressed data doesn't match original")
	}
}

func TestCompressingResponseWriter_Brotli(t *testing.T) {
	c := New(config.CompressionConfig{
		Enabled: true,
		Level:   6,
		MinSize: 10,
	})

	w := httptest.NewRecorder()
	cw := NewCompressingResponseWriter(w, c, "br")

	cw.ResponseWriter.Header().Set("Content-Type", "application/json")
	cw.WriteHeader(http.StatusOK)

	data := strings.Repeat(`{"key":"value"}`, 100)
	cw.Write([]byte(data))
	cw.Close()

	if w.Header().Get("Content-Encoding") != "br" {
		t.Errorf("expected br Content-Encoding, got %q", w.Header().Get("Content-Encoding"))
	}

	decompressed, err := io.ReadAll(brotli.NewReader(w.Body))
	if err != nil {
		t.Fatalf("failed to decompress brotli: %v", err)
	}
	if string(decompressed) != data {
		t.Error("decompressed data doesn't match original")
	}
}

func TestCompressingResponseWriter_Zstd(t *testing.T) {
	c := New(config.CompressionConfig{
		Enabled: true,
		Level:   3,
		MinSize: 10,
	})

	w := httptest.NewRecorder()
	cw := NewCompressingResponseWriter(w, c, "zstd")

	cw.ResponseWriter.Header().Set("Content-Type", "application/json")
	cw.WriteHeader(http.StatusOK)

	data := strings.Repeat(`{"key":"value"}`, 100)
	cw.Write([]byte(data))
	cw.Close()

	if w.Header().Get("Content-Encoding") != "zstd" {
		t.Errorf("expected zstd Content-Encoding, got %q", w.Header().Get("Content-Encoding"))
	}

	reader, err := zstd.NewReader(w.Body)
	if err != nil {
		t.Fatalf("failed to create zstd reader: %v", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to decompress zstd: %v", err)
	}
	if string(decompressed) != data {
		t.Error("decompressed data doesn't match original")
	}
}

func TestSmallBodyNotCompressed(t *testing.T) {
	c := New(config.CompressionConfig{
		Enabled: true,
		MinSize: 1024,
	})

	w := httptest.NewRecorder()
	cw := NewCompressingResponseWriter(w, c, "gzip")

	cw.ResponseWriter.Header().Set("Content-Type", "application/json")
	cw.WriteHeader(http.StatusOK)

	cw.Write([]byte(`{"ok":true}`))
	cw.Close()

	if w.Header().Get("Content-Encoding") != "" {
		t.Error("small body should not be compressed")
	}
}

func TestCompressionMetrics(t *testing.T) {
	c := New(config.CompressionConfig{
		Enabled: true,
		Level:   6,
		MinSize: 10,
	})

	w := httptest.NewRecorder()
	cw := NewCompressingResponseWriter(w, c, "gzip")

	cw.ResponseWriter.Header().Set("Content-Type", "application/json")
	cw.WriteHeader(http.StatusOK)

	data := strings.Repeat(`{"key":"value"}`, 100)
	cw.Write([]byte(data))
	cw.Close()

	snap := c.Stats()
	gzStats := snap.Algorithms["gzip"]
	if gzStats.BytesIn <= 0 {
		t.Errorf("BytesIn should be > 0, got %d", gzStats.BytesIn)
	}
	if gzStats.BytesOut <= 0 {
		t.Errorf("BytesOut should be > 0, got %d", gzStats.BytesOut)
	}
	if gzStats.Count != 1 {
		t.Errorf("Count should be 1, got %d", gzStats.Count)
	}
}

func TestCompressorByRouteStats(t *testing.T) {
	m := NewCompressorByRoute()
	m.AddRoute("route1", config.CompressionConfig{
		Enabled: true,
		Level:   6,
	})

	c := m.GetCompressor("route1")
	if c == nil || !c.IsEnabled() {
		t.Fatal("expected compressor for route1")
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}

	if m.GetCompressor("unknown") != nil {
		t.Error("expected nil for unknown route")
	}
}

func TestAlgorithmsConfig(t *testing.T) {
	c := New(config.CompressionConfig{
		Enabled:    true,
		Algorithms: []string{"gzip"},
	})

	// gzip should work
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip, br, zstd")
	if got := c.NegotiateEncoding(r); got != "gzip" {
		t.Errorf("expected gzip only, got %q", got)
	}

	// br should be rejected since not in algorithms
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("Accept-Encoding", "br")
	if got := c.NegotiateEncoding(r2); got != "" {
		t.Errorf("expected empty for br-only request with gzip-only config, got %q", got)
	}
}
