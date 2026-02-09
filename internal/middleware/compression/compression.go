package compression

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"

	"github.com/wudi/gateway/internal/config"
)

// Compressor manages response compression for a route
type Compressor struct {
	enabled      bool
	level        int
	minSize      int
	contentTypes map[string]bool
}

// New creates a new Compressor from config
func New(cfg config.CompressionConfig) *Compressor {
	c := &Compressor{
		enabled:      cfg.Enabled,
		level:        cfg.Level,
		minSize:      cfg.MinSize,
		contentTypes: make(map[string]bool),
	}

	if c.level <= 0 || c.level > 9 {
		c.level = 6
	}
	if c.minSize <= 0 {
		c.minSize = 1024
	}

	if len(cfg.ContentTypes) > 0 {
		for _, ct := range cfg.ContentTypes {
			c.contentTypes[ct] = true
		}
	} else {
		// Default compressible types
		c.contentTypes["text/html"] = true
		c.contentTypes["text/css"] = true
		c.contentTypes["text/plain"] = true
		c.contentTypes["text/javascript"] = true
		c.contentTypes["application/javascript"] = true
		c.contentTypes["application/json"] = true
		c.contentTypes["application/xml"] = true
		c.contentTypes["text/xml"] = true
		c.contentTypes["image/svg+xml"] = true
	}

	return c
}

// IsEnabled returns whether compression is enabled
func (c *Compressor) IsEnabled() bool {
	return c.enabled
}

// ShouldCompress checks if the request accepts gzip encoding
func (c *Compressor) ShouldCompress(r *http.Request) bool {
	if !c.enabled {
		return false
	}
	ae := r.Header.Get("Accept-Encoding")
	return strings.Contains(ae, "gzip")
}

// isCompressibleType checks if the content type should be compressed
func (c *Compressor) isCompressibleType(contentType string) bool {
	if len(c.contentTypes) == 0 {
		return true
	}
	// Strip parameters (charset, etc)
	ct := contentType
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}
	return c.contentTypes[ct]
}

// CompressingResponseWriter wraps a ResponseWriter to gzip the response
type CompressingResponseWriter struct {
	http.ResponseWriter
	compressor    *Compressor
	gzWriter      *gzip.Writer
	headerWritten bool
	statusCode    int
	buf           []byte // buffer to check min size
	decided       bool   // whether we decided to compress or not
	compressing   bool
}

// NewCompressingResponseWriter creates a new compressing writer
func NewCompressingResponseWriter(w http.ResponseWriter, c *Compressor) *CompressingResponseWriter {
	return &CompressingResponseWriter{
		ResponseWriter: w,
		compressor:     c,
		statusCode:     200,
	}
}

// WriteHeader captures status code
func (w *CompressingResponseWriter) WriteHeader(code int) {
	if w.headerWritten {
		return
	}
	w.statusCode = code

	// If we've already decided, write through
	if w.decided {
		w.headerWritten = true
		if w.compressing {
			w.ResponseWriter.Header().Del("Content-Length")
			w.ResponseWriter.Header().Set("Content-Encoding", "gzip")
			w.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
		}
		w.ResponseWriter.WriteHeader(code)
		return
	}

	// Check content type — if not compressible, skip
	ct := w.ResponseWriter.Header().Get("Content-Type")
	if ct != "" && !w.compressor.isCompressibleType(ct) {
		w.decided = true
		w.compressing = false
		w.headerWritten = true
		w.ResponseWriter.WriteHeader(code)
		return
	}

	// Don't write header yet — wait for Write to check size
}

func (w *CompressingResponseWriter) Write(b []byte) (int, error) {
	if !w.decided {
		w.buf = append(w.buf, b...)

		// Check content type from header
		ct := w.ResponseWriter.Header().Get("Content-Type")
		if ct != "" && !w.compressor.isCompressibleType(ct) {
			w.decided = true
			w.compressing = false
			w.flushBuffer()
			return len(b), nil
		}

		if len(w.buf) >= w.compressor.minSize {
			// Enough data, start compressing
			w.decided = true
			w.compressing = true
			w.flushBuffer()
			return len(b), nil
		}
		return len(b), nil
	}

	if w.compressing && w.gzWriter != nil {
		return w.gzWriter.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *CompressingResponseWriter) flushBuffer() {
	if !w.headerWritten {
		w.headerWritten = true
		if w.compressing {
			w.ResponseWriter.Header().Del("Content-Length")
			w.ResponseWriter.Header().Set("Content-Encoding", "gzip")
			w.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
			gz, _ := gzip.NewWriterLevel(w.ResponseWriter, w.compressor.level)
			w.gzWriter = gz
		}
		w.ResponseWriter.WriteHeader(w.statusCode)
	}

	if len(w.buf) > 0 {
		if w.compressing && w.gzWriter != nil {
			w.gzWriter.Write(w.buf)
		} else {
			w.ResponseWriter.Write(w.buf)
		}
		w.buf = nil
	}
}

// Close finishes compression — must be called after request completes
func (w *CompressingResponseWriter) Close() {
	if !w.decided {
		// Data was less than minSize, write uncompressed
		w.decided = true
		w.compressing = false
		w.flushBuffer()
		return
	}
	if w.compressing && w.gzWriter != nil {
		w.gzWriter.Close()
	}
}

// Flush implements http.Flusher
func (w *CompressingResponseWriter) Flush() {
	if !w.decided {
		w.decided = true
		w.compressing = len(w.buf) >= w.compressor.minSize
		w.flushBuffer()
	}
	if w.compressing && w.gzWriter != nil {
		w.gzWriter.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// StatusCode returns the recorded status code
func (w *CompressingResponseWriter) StatusCode() int {
	return w.statusCode
}

// Unwrap returns the underlying ResponseWriter
func (w *CompressingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// CompressingResponseWriter implements a Close method for flushing the gzip stream

// CompressorByRoute manages compressors per route
type CompressorByRoute struct {
	compressors map[string]*Compressor
	mu          sync.RWMutex
}

// NewCompressorByRoute creates a new per-route compressor manager
func NewCompressorByRoute() *CompressorByRoute {
	return &CompressorByRoute{
		compressors: make(map[string]*Compressor),
	}
}

// AddRoute adds a compressor for a route
func (m *CompressorByRoute) AddRoute(routeID string, cfg config.CompressionConfig) {
	m.mu.Lock()
	m.compressors[routeID] = New(cfg)
	m.mu.Unlock()
}

// GetCompressor returns the compressor for a route
func (m *CompressorByRoute) GetCompressor(routeID string) *Compressor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.compressors[routeID]
}

// RouteIDs returns all route IDs with compressors.
func (m *CompressorByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.compressors))
	for id := range m.compressors {
		ids = append(ids, id)
	}
	return ids
}
