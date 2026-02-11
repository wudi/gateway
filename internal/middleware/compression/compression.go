package compression

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/wudi/gateway/internal/config"
)

// encodingWriter is an io.Writer that can be closed.
type encodingWriter interface {
	io.Writer
	Close() error
}

// optionalFlusher is implemented by writers that support flushing.
type optionalFlusher interface {
	Flush() error
}

// countWriter wraps an io.Writer and counts bytes written.
type countWriter struct {
	w io.Writer
	n int64
}

func (cw *countWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
}

// pooledZstdWriter wraps a *zstd.Encoder and returns it to a pool on Close.
type pooledZstdWriter struct {
	enc  *zstd.Encoder
	pool *sync.Pool
}

func (pw *pooledZstdWriter) Write(p []byte) (int, error) {
	return pw.enc.Write(p)
}

func (pw *pooledZstdWriter) Close() error {
	err := pw.enc.Close()
	pw.pool.Put(pw.enc)
	return err
}

// AlgorithmMetrics tracks compression metrics for one algorithm.
type AlgorithmMetrics struct {
	BytesIn  atomic.Int64
	BytesOut atomic.Int64
	Count    atomic.Int64
}

// AlgorithmSnapshot is the JSON-serializable form of AlgorithmMetrics.
type AlgorithmSnapshot struct {
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
	Count    int64 `json:"count"`
}

// CompressionSnapshot is per-route compression stats.
type CompressionSnapshot struct {
	Algorithms map[string]AlgorithmSnapshot `json:"algorithms"`
}

// encodingPref represents a parsed Accept-Encoding entry.
type encodingPref struct {
	encoding string
	quality  float64
}

// defaultAlgoOrder is the server-preferred algorithm order.
var defaultAlgoOrder = []string{"br", "zstd", "gzip"}

// Compressor manages response compression for a route.
type Compressor struct {
	enabled      bool
	level        int
	minSize      int
	contentTypes map[string]bool
	algorithms   map[string]bool
	algoOrder    []string
	metrics      map[string]*AlgorithmMetrics
	zstdPool     sync.Pool
}

// New creates a new Compressor from config.
func New(cfg config.CompressionConfig) *Compressor {
	c := &Compressor{
		enabled:      cfg.Enabled,
		level:        cfg.Level,
		minSize:      cfg.MinSize,
		contentTypes: make(map[string]bool),
		algorithms:   make(map[string]bool),
		metrics:      make(map[string]*AlgorithmMetrics),
	}

	if c.level <= 0 || c.level > 11 {
		c.level = 6
	}
	if c.minSize <= 0 {
		c.minSize = 1024
	}

	// Set up algorithms
	if len(cfg.Algorithms) > 0 {
		for _, algo := range cfg.Algorithms {
			c.algorithms[algo] = true
		}
	} else {
		// Default: all three
		c.algorithms["gzip"] = true
		c.algorithms["br"] = true
		c.algorithms["zstd"] = true
	}

	// Build server preference order (only enabled algos)
	for _, algo := range defaultAlgoOrder {
		if c.algorithms[algo] {
			c.algoOrder = append(c.algoOrder, algo)
		}
	}

	// Initialize metrics for each enabled algorithm
	for algo := range c.algorithms {
		c.metrics[algo] = &AlgorithmMetrics{}
	}

	// Content types
	if len(cfg.ContentTypes) > 0 {
		for _, ct := range cfg.ContentTypes {
			c.contentTypes[ct] = true
		}
	} else {
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

	// Initialize zstd pool
	zstdLevel := zstd.SpeedDefault
	if c.level > 0 {
		zstdLevel = zstd.EncoderLevelFromZstd(c.level)
	}
	c.zstdPool = sync.Pool{
		New: func() any {
			enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstdLevel))
			return enc
		},
	}

	return c
}

// IsEnabled returns whether compression is enabled.
func (c *Compressor) IsEnabled() bool {
	return c.enabled
}

// parseAcceptEncoding parses the Accept-Encoding header per RFC 7231 §5.3.4.
func parseAcceptEncoding(header string) []encodingPref {
	if header == "" {
		return nil
	}
	parts := strings.Split(header, ",")
	prefs := make([]encodingPref, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		enc := part
		q := 1.0
		if idx := strings.Index(part, ";"); idx != -1 {
			enc = strings.TrimSpace(part[:idx])
			params := strings.TrimSpace(part[idx+1:])
			if strings.HasPrefix(params, "q=") {
				if v, err := strconv.ParseFloat(params[2:], 64); err == nil {
					q = v
				}
			}
		}
		prefs = append(prefs, encodingPref{encoding: enc, quality: q})
	}
	return prefs
}

// NegotiateEncoding selects the best compression algorithm based on Accept-Encoding.
// Returns "" if no suitable algorithm is found or compression is disabled.
func (c *Compressor) NegotiateEncoding(r *http.Request) string {
	if !c.enabled {
		return ""
	}
	ae := r.Header.Get("Accept-Encoding")
	if ae == "" {
		return ""
	}

	prefs := parseAcceptEncoding(ae)
	if len(prefs) == 0 {
		return ""
	}

	// Build a map of encoding → quality from client preferences.
	clientPrefs := make(map[string]float64, len(prefs))
	hasWildcard := false
	wildcardQ := 0.0
	for _, p := range prefs {
		if p.encoding == "*" {
			hasWildcard = true
			wildcardQ = p.quality
		} else {
			clientPrefs[p.encoding] = p.quality
		}
	}

	// Walk server preference order; pick best match.
	bestAlgo := ""
	bestQ := -1.0
	for _, algo := range c.algoOrder {
		q, explicit := clientPrefs[algo]
		if !explicit {
			if hasWildcard {
				q = wildcardQ
			} else {
				continue
			}
		}
		if q <= 0 {
			continue // q=0 means rejected
		}
		// Higher quality wins; on tie, server preference (earlier in algoOrder) wins.
		if q > bestQ {
			bestQ = q
			bestAlgo = algo
		}
	}
	return bestAlgo
}

// newEncodingWriter creates a writer for the specified algorithm.
func (c *Compressor) newEncodingWriter(w io.Writer, algo string) encodingWriter {
	switch algo {
	case "gzip":
		level := c.level
		if level > 9 {
			level = 9
		}
		gz, _ := gzip.NewWriterLevel(w, level)
		return gz
	case "br":
		return brotli.NewWriterLevel(w, c.level)
	case "zstd":
		enc := c.zstdPool.Get().(*zstd.Encoder)
		enc.Reset(w)
		return &pooledZstdWriter{enc: enc, pool: &c.zstdPool}
	default:
		// Fallback to gzip
		gz, _ := gzip.NewWriterLevel(w, c.level)
		return gz
	}
}

// Stats returns per-algorithm compression metrics.
func (c *Compressor) Stats() CompressionSnapshot {
	snap := CompressionSnapshot{
		Algorithms: make(map[string]AlgorithmSnapshot, len(c.metrics)),
	}
	for algo, m := range c.metrics {
		snap.Algorithms[algo] = AlgorithmSnapshot{
			BytesIn:  m.BytesIn.Load(),
			BytesOut: m.BytesOut.Load(),
			Count:    m.Count.Load(),
		}
	}
	return snap
}

// isCompressibleType checks if the content type should be compressed.
func (c *Compressor) isCompressibleType(contentType string) bool {
	if len(c.contentTypes) == 0 {
		return true
	}
	ct := contentType
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}
	return c.contentTypes[ct]
}

// CompressingResponseWriter wraps a ResponseWriter to compress the response.
type CompressingResponseWriter struct {
	http.ResponseWriter
	compressor    *Compressor
	algorithm     string
	encWriter     encodingWriter
	countWriter   *countWriter
	headerWritten bool
	statusCode    int
	buf           []byte
	decided       bool
	compressing   bool
	bytesIn       int64
}

// NewCompressingResponseWriter creates a new compressing writer.
func NewCompressingResponseWriter(w http.ResponseWriter, c *Compressor, algo string) *CompressingResponseWriter {
	return &CompressingResponseWriter{
		ResponseWriter: w,
		compressor:     c,
		algorithm:      algo,
		statusCode:     200,
	}
}

// WriteHeader captures status code.
func (w *CompressingResponseWriter) WriteHeader(code int) {
	if w.headerWritten {
		return
	}
	w.statusCode = code

	if w.decided {
		w.headerWritten = true
		if w.compressing {
			w.ResponseWriter.Header().Del("Content-Length")
			w.ResponseWriter.Header().Set("Content-Encoding", w.algorithm)
			w.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
		}
		w.ResponseWriter.WriteHeader(code)
		return
	}

	ct := w.ResponseWriter.Header().Get("Content-Type")
	if ct != "" && !w.compressor.isCompressibleType(ct) {
		w.decided = true
		w.compressing = false
		w.headerWritten = true
		w.ResponseWriter.WriteHeader(code)
		return
	}
}

func (w *CompressingResponseWriter) Write(b []byte) (int, error) {
	if !w.decided {
		w.buf = append(w.buf, b...)

		ct := w.ResponseWriter.Header().Get("Content-Type")
		if ct != "" && !w.compressor.isCompressibleType(ct) {
			w.decided = true
			w.compressing = false
			w.flushBuffer()
			return len(b), nil
		}

		if len(w.buf) >= w.compressor.minSize {
			w.decided = true
			w.compressing = true
			w.flushBuffer()
			return len(b), nil
		}
		return len(b), nil
	}

	if w.compressing && w.encWriter != nil {
		w.bytesIn += int64(len(b))
		return w.encWriter.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *CompressingResponseWriter) flushBuffer() {
	if !w.headerWritten {
		w.headerWritten = true
		if w.compressing {
			w.ResponseWriter.Header().Del("Content-Length")
			w.ResponseWriter.Header().Set("Content-Encoding", w.algorithm)
			w.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
			cw := &countWriter{w: w.ResponseWriter}
			w.countWriter = cw
			w.encWriter = w.compressor.newEncodingWriter(cw, w.algorithm)
		}
		w.ResponseWriter.WriteHeader(w.statusCode)
	}

	if len(w.buf) > 0 {
		if w.compressing && w.encWriter != nil {
			w.bytesIn += int64(len(w.buf))
			w.encWriter.Write(w.buf)
		} else {
			w.ResponseWriter.Write(w.buf)
		}
		w.buf = nil
	}
}

// Close finishes compression — must be called after request completes.
func (w *CompressingResponseWriter) Close() {
	if !w.decided {
		w.decided = true
		w.compressing = false
		w.flushBuffer()
		return
	}
	if w.compressing && w.encWriter != nil {
		w.encWriter.Close()
		// Record metrics
		if m, ok := w.compressor.metrics[w.algorithm]; ok {
			m.BytesIn.Add(w.bytesIn)
			if w.countWriter != nil {
				m.BytesOut.Add(w.countWriter.n)
			}
			m.Count.Add(1)
		}
	}
}

// Flush implements http.Flusher.
func (w *CompressingResponseWriter) Flush() {
	if !w.decided {
		w.decided = true
		w.compressing = len(w.buf) >= w.compressor.minSize
		w.flushBuffer()
	}
	if w.compressing && w.encWriter != nil {
		if f, ok := w.encWriter.(optionalFlusher); ok {
			f.Flush()
		}
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// StatusCode returns the recorded status code.
func (w *CompressingResponseWriter) StatusCode() int {
	return w.statusCode
}

// Unwrap returns the underlying ResponseWriter.
func (w *CompressingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// CompressorByRoute manages compressors per route.
type CompressorByRoute struct {
	compressors map[string]*Compressor
	mu          sync.RWMutex
}

// NewCompressorByRoute creates a new per-route compressor manager.
func NewCompressorByRoute() *CompressorByRoute {
	return &CompressorByRoute{
		compressors: make(map[string]*Compressor),
	}
}

// AddRoute adds a compressor for a route.
func (m *CompressorByRoute) AddRoute(routeID string, cfg config.CompressionConfig) {
	m.mu.Lock()
	m.compressors[routeID] = New(cfg)
	m.mu.Unlock()
}

// GetCompressor returns the compressor for a route.
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

// Stats returns per-route compression statistics.
func (m *CompressorByRoute) Stats() map[string]CompressionSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]CompressionSnapshot, len(m.compressors))
	for id, c := range m.compressors {
		result[id] = c.Stats()
	}
	return result
}
