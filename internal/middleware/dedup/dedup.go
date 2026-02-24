package dedup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
)

// CompiledDedup is a compiled per-route dedup handler created once during route setup.
type CompiledDedup struct {
	ttl            time.Duration
	includeHeaders []string
	includeBody    bool
	maxBodySize    int64
	store          Store
	metrics        *DedupMetrics
	routeID        string
	mode           string

	// In-flight deduplication
	mu       sync.Mutex
	inflight map[string]*inflightEntry
}

type inflightEntry struct {
	done chan struct{}
	resp *StoredResponse
}

// DedupMetrics tracks dedup statistics.
type DedupMetrics struct {
	TotalRequests   atomic.Int64
	DedupHits       atomic.Int64
	DedupMisses     atomic.Int64
	InFlightWaits   atomic.Int64
	StoreErrors     atomic.Int64
	ResponsesStored atomic.Int64
}

// DedupStatus is the admin API representation.
type DedupStatus struct {
	TTL             string   `json:"ttl"`
	IncludeHeaders  []string `json:"include_headers"`
	IncludeBody     bool     `json:"include_body"`
	MaxBodySize     int64    `json:"max_body_size"`
	Mode            string   `json:"mode"`
	TotalRequests   int64    `json:"total_requests"`
	DedupHits       int64    `json:"dedup_hits"`
	DedupMisses     int64    `json:"dedup_misses"`
	InFlightWaits   int64    `json:"inflight_waits"`
	StoreErrors     int64    `json:"store_errors"`
	ResponsesStored int64    `json:"responses_stored"`
}

// New creates a new CompiledDedup from config.
func New(routeID string, cfg config.RequestDedupConfig, redisClient *redis.Client) (*CompiledDedup, error) {
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = 60 * time.Second
	}

	includeBody := true
	if cfg.IncludeBody != nil {
		includeBody = *cfg.IncludeBody
	}

	maxBodySize := cfg.MaxBodySize
	if maxBodySize == 0 {
		maxBodySize = 1 << 20 // 1MB
	}

	mode := cfg.Mode
	if mode == "" {
		mode = "local"
	}

	var store Store
	if mode == "distributed" && redisClient != nil {
		store = NewRedisStore(redisClient, "gw:dedup:"+routeID+":")
	} else {
		store = NewMemoryStore(ttl)
	}

	// Normalize header names to lowercase for consistent fingerprinting
	headers := make([]string, len(cfg.IncludeHeaders))
	for i, h := range cfg.IncludeHeaders {
		headers[i] = strings.ToLower(h)
	}

	return &CompiledDedup{
		ttl:            ttl,
		includeHeaders: headers,
		includeBody:    includeBody,
		maxBodySize:    maxBodySize,
		store:          store,
		metrics:        &DedupMetrics{},
		routeID:        routeID,
		mode:           mode,
		inflight:       make(map[string]*inflightEntry),
	}, nil
}

// Fingerprint computes a SHA-256 fingerprint of the request.
func (cd *CompiledDedup) Fingerprint(r *http.Request) (string, error) {
	h := sha256.New()

	// Method + path + query
	h.Write([]byte(r.Method))
	h.Write([]byte(r.URL.Path))
	h.Write([]byte(r.URL.RawQuery))

	// Sorted configured header values
	if len(cd.includeHeaders) > 0 {
		sorted := make([]string, len(cd.includeHeaders))
		copy(sorted, cd.includeHeaders)
		sort.Strings(sorted)
		for _, name := range sorted {
			vals := r.Header.Values(name)
			for _, v := range vals {
				h.Write([]byte(name))
				h.Write([]byte(v))
			}
		}
	}

	// Body
	if cd.includeBody && r.Body != nil && r.ContentLength != 0 {
		limit := cd.maxBodySize
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, limit))
		if err != nil {
			return "", fmt.Errorf("dedup: failed to read body: %w", err)
		}
		// Restore the body
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		h.Write(bodyBytes)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// Middleware returns a middleware that deduplicates requests by content hash.
func (cd *CompiledDedup) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cd.metrics.TotalRequests.Add(1)

			fp, err := cd.Fingerprint(r)
			if err != nil {
				// Can't fingerprint — pass through
				next.ServeHTTP(w, r)
				return
			}

			// Check store for existing response
			stored, err := cd.store.Get(r.Context(), fp)
			if err != nil {
				cd.metrics.StoreErrors.Add(1)
				// Fail-open: let request proceed
			}
			if stored != nil {
				cd.metrics.DedupHits.Add(1)
				replayResponse(w, stored)
				return
			}

			// Check in-flight map
			cd.mu.Lock()
			if entry, ok := cd.inflight[fp]; ok {
				cd.mu.Unlock()
				cd.metrics.InFlightWaits.Add(1)

				select {
				case <-entry.done:
					if entry.resp != nil {
						replayResponse(w, entry.resp)
						return
					}
					// In-flight cancelled — let through
					next.ServeHTTP(w, r)
					return
				case <-r.Context().Done():
					http.Error(w, "request cancelled", http.StatusGatewayTimeout)
					return
				}
			}

			// Register as in-flight
			entry := &inflightEntry{done: make(chan struct{})}
			cd.inflight[fp] = entry
			cd.mu.Unlock()

			cd.metrics.DedupMisses.Add(1)

			// Capture response
			cw := newCapturingWriter(w)
			next.ServeHTTP(cw, r)

			resp := cw.toStoredResponse()

			// Store response
			if storeErr := cd.store.Set(context.Background(), fp, resp, cd.ttl); storeErr != nil {
				cd.metrics.StoreErrors.Add(1)
			} else {
				cd.metrics.ResponsesStored.Add(1)
			}

			// Notify waiting goroutines
			cd.mu.Lock()
			entry.resp = resp
			close(entry.done)
			delete(cd.inflight, fp)
			cd.mu.Unlock()
		})
	}
}

// Status returns the admin status snapshot.
func (cd *CompiledDedup) Status() DedupStatus {
	return DedupStatus{
		TTL:             cd.ttl.String(),
		IncludeHeaders:  cd.includeHeaders,
		IncludeBody:     cd.includeBody,
		MaxBodySize:     cd.maxBodySize,
		Mode:            cd.mode,
		TotalRequests:   cd.metrics.TotalRequests.Load(),
		DedupHits:       cd.metrics.DedupHits.Load(),
		DedupMisses:     cd.metrics.DedupMisses.Load(),
		InFlightWaits:   cd.metrics.InFlightWaits.Load(),
		StoreErrors:     cd.metrics.StoreErrors.Load(),
		ResponsesStored: cd.metrics.ResponsesStored.Load(),
	}
}

// Close releases store resources.
func (cd *CompiledDedup) Close() {
	cd.store.Close()
}

// replayResponse writes a stored response to the client with X-Dedup-Replayed header.
func replayResponse(w http.ResponseWriter, resp *StoredResponse) {
	for k, vv := range resp.Headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Dedup-Replayed", "true")
	w.WriteHeader(resp.StatusCode)
	w.Write(resp.Body)
}

// capturingWriter wraps http.ResponseWriter to capture the response.
type capturingWriter struct {
	http.ResponseWriter
	statusCode  int
	body        bytes.Buffer
	wroteHeader bool
}

func newCapturingWriter(w http.ResponseWriter) *capturingWriter {
	return &capturingWriter{ResponseWriter: w, statusCode: 200}
}

func (w *capturingWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *capturingWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(200)
	}
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *capturingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *capturingWriter) toStoredResponse() *StoredResponse {
	return &StoredResponse{
		StatusCode: w.statusCode,
		Headers:    w.ResponseWriter.Header().Clone(),
		Body:       w.body.Bytes(),
	}
}

// DedupByRoute manages per-route dedup handlers.
type DedupByRoute struct {
	byroute.Manager[*CompiledDedup]
}

// NewDedupByRoute creates a new DedupByRoute manager.
func NewDedupByRoute() *DedupByRoute {
	return &DedupByRoute{}
}

// AddRoute creates and registers a dedup handler for the given route.
func (m *DedupByRoute) AddRoute(routeID string, cfg config.RequestDedupConfig, redisClient *redis.Client) error {
	if !cfg.Enabled {
		return nil
	}

	cd, err := New(routeID, cfg, redisClient)
	if err != nil {
		return err
	}

	m.Add(routeID, cd)
	return nil
}

// GetHandler returns the dedup handler for a route, or nil.
func (m *DedupByRoute) GetHandler(routeID string) *CompiledDedup {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns admin status for all routes.
func (m *DedupByRoute) Stats() map[string]DedupStatus {
	return byroute.CollectStats(&m.Manager, func(cd *CompiledDedup) DedupStatus { return cd.Status() })
}

// CloseAll closes all dedup handlers.
func (m *DedupByRoute) CloseAll() {
	m.Range(func(_ string, cd *CompiledDedup) bool {
		cd.Close()
		return true
	})
}
