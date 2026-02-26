package decompress

import (
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// defaultMaxDecompressedSize is the default zip bomb protection limit (50 MB).
const defaultMaxDecompressedSize int64 = 50 << 20

// validAlgorithms is the set of supported Content-Encoding values.
var validAlgorithms = map[string]bool{
	"gzip":    true,
	"deflate": true,
	"br":      true,
	"zstd":    true,
}

// Metrics tracks per-algorithm decompression metrics.
type Metrics struct {
	TotalRequests  atomic.Int64
	Decompressed   atomic.Int64
	Errors         atomic.Int64
	AlgorithmCount map[string]*atomic.Int64
}

// Snapshot is the JSON-serializable metrics form.
type Snapshot struct {
	TotalRequests  int64            `json:"total_requests"`
	Decompressed   int64            `json:"decompressed"`
	Errors         int64            `json:"errors"`
	AlgorithmCount map[string]int64 `json:"algorithm_count"`
}

// Decompressor handles request body decompression for a route.
type Decompressor struct {
	algorithms          map[string]bool
	maxDecompressedSize int64
	metrics             Metrics
	zstdPool            sync.Pool
}

// New creates a Decompressor from config.
func New(cfg config.RequestDecompressionConfig) *Decompressor {
	d := &Decompressor{
		algorithms:          make(map[string]bool),
		maxDecompressedSize: cfg.MaxDecompressedSize,
	}

	if d.maxDecompressedSize <= 0 {
		d.maxDecompressedSize = defaultMaxDecompressedSize
	}

	// Set up algorithms
	if len(cfg.Algorithms) > 0 {
		for _, algo := range cfg.Algorithms {
			if validAlgorithms[algo] {
				d.algorithms[algo] = true
			}
		}
	} else {
		// Default: all four
		for algo := range validAlgorithms {
			d.algorithms[algo] = true
		}
	}

	// Initialize per-algorithm counters
	d.metrics.AlgorithmCount = make(map[string]*atomic.Int64, len(d.algorithms))
	for algo := range d.algorithms {
		d.metrics.AlgorithmCount[algo] = &atomic.Int64{}
	}

	// zstd decoder pool
	d.zstdPool = sync.Pool{
		New: func() any {
			dec, _ := zstd.NewReader(nil)
			return dec
		},
	}

	return d
}

// IsEnabled returns whether decompression is enabled.
func (d *Decompressor) IsEnabled() bool {
	return len(d.algorithms) > 0
}

// ShouldDecompress checks if the request has a supported Content-Encoding.
func (d *Decompressor) ShouldDecompress(r *http.Request) (string, bool) {
	ce := r.Header.Get("Content-Encoding")
	if ce == "" {
		return "", false
	}
	// Normalize
	ce = strings.TrimSpace(strings.ToLower(ce))
	if d.algorithms[ce] {
		return ce, true
	}
	return "", false
}

// Decompress wraps the request body with the appropriate decompressor,
// removes Content-Encoding, and updates Content-Length.
func (d *Decompressor) Decompress(r *http.Request, algo string) error {
	d.metrics.TotalRequests.Add(1)

	reader, err := d.newReader(r.Body, algo)
	if err != nil {
		d.metrics.Errors.Add(1)
		return fmt.Errorf("decompress: %w", err)
	}

	// Wrap with size limit for zip bomb protection
	limited := &limitedReader{
		r:   reader,
		n:   d.maxDecompressedSize,
		err: fmt.Errorf("decompressed body exceeds maximum size of %d bytes", d.maxDecompressedSize),
	}

	r.Body = &decompressedBody{
		Reader: limited,
		closer: r.Body,
		pool:   d.getPoolReturn(reader, algo),
	}

	// Remove Content-Encoding since body is now uncompressed
	r.Header.Del("Content-Encoding")
	// Content-Length is no longer accurate
	r.Header.Del("Content-Length")
	r.ContentLength = -1

	d.metrics.Decompressed.Add(1)
	if counter, ok := d.metrics.AlgorithmCount[algo]; ok {
		counter.Add(1)
	}

	return nil
}

// Stats returns a snapshot of decompression metrics.
func (d *Decompressor) Stats() Snapshot {
	snap := Snapshot{
		TotalRequests:  d.metrics.TotalRequests.Load(),
		Decompressed:   d.metrics.Decompressed.Load(),
		Errors:         d.metrics.Errors.Load(),
		AlgorithmCount: make(map[string]int64, len(d.metrics.AlgorithmCount)),
	}
	for algo, counter := range d.metrics.AlgorithmCount {
		snap.AlgorithmCount[algo] = counter.Load()
	}
	return snap
}

// newReader creates a decompression reader for the given algorithm.
func (d *Decompressor) newReader(r io.Reader, algo string) (io.Reader, error) {
	switch algo {
	case "gzip":
		return gzip.NewReader(r)
	case "deflate":
		return flate.NewReader(r), nil
	case "br":
		return brotli.NewReader(r), nil
	case "zstd":
		dec := d.zstdPool.Get().(*zstd.Decoder)
		dec.Reset(r)
		return dec, nil
	default:
		return nil, fmt.Errorf("unsupported encoding: %s", algo)
	}
}

// getPoolReturn returns a function that returns pooled resources, or nil.
func (d *Decompressor) getPoolReturn(reader io.Reader, algo string) func() {
	if algo == "zstd" {
		if dec, ok := reader.(*zstd.Decoder); ok {
			return func() { d.zstdPool.Put(dec) }
		}
	}
	return nil
}

// limitedReader wraps a reader and returns an error if the limit is exceeded.
type limitedReader struct {
	r   io.Reader
	n   int64
	err error
}

func (lr *limitedReader) Read(p []byte) (int, error) {
	n, err := lr.r.Read(p)
	lr.n -= int64(n)
	if lr.n < 0 {
		return 0, lr.err
	}
	return n, err
}

// decompressedBody wraps the decompressed reader with proper cleanup.
type decompressedBody struct {
	io.Reader
	closer io.Closer
	pool   func()
}

func (db *decompressedBody) Close() error {
	if db.pool != nil {
		db.pool()
	}
	return db.closer.Close()
}

// MergeDecompressionConfig merges per-route config with global config.
func MergeDecompressionConfig(perRoute, global config.RequestDecompressionConfig) config.RequestDecompressionConfig {
	return config.MergeNonZero(global, perRoute)
}

// DecompressorByRoute manages decompressors per route.
type DecompressorByRoute struct {
	byroute.Manager[*Decompressor]
}

// NewDecompressorByRoute creates a new per-route decompressor manager.
func NewDecompressorByRoute() *DecompressorByRoute {
	return &DecompressorByRoute{}
}

// AddRoute adds a decompressor for a route.
func (m *DecompressorByRoute) AddRoute(routeID string, cfg config.RequestDecompressionConfig) {
	m.Add(routeID, New(cfg))
}

// GetDecompressor returns the decompressor for a route.
func (m *DecompressorByRoute) GetDecompressor(routeID string) *Decompressor {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route decompression statistics.
func (m *DecompressorByRoute) Stats() map[string]Snapshot {
	return byroute.CollectStats(&m.Manager, func(d *Decompressor) Snapshot { return d.Stats() })
}

// Middleware returns a middleware that decompresses request bodies with Content-Encoding.
func (d *Decompressor) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if algo, ok := d.ShouldDecompress(r); ok {
				if err := d.Decompress(r, algo); err != nil {
					http.Error(w, `{"error":"request decompression failed"}`, http.StatusBadRequest)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
