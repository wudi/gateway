package idempotency

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/variables"
)

// CompiledIdempotency is a compiled per-route idempotency handler created once during route setup.
type CompiledIdempotency struct {
	headerName   string
	ttl          time.Duration
	methods      map[string]bool
	enforce      bool
	keyScope     string
	maxKeyLength int
	maxBodySize  int64
	store        Store
	metrics      *IdempotencyMetrics
	routeID      string
	mode         string

	// In-flight deduplication
	mu       sync.Mutex
	inflight map[string]*inflightEntry
}

type inflightEntry struct {
	done chan struct{}
	resp *StoredResponse
}

// New creates a new CompiledIdempotency from config.
func New(routeID string, cfg config.IdempotencyConfig, redisClient *redis.Client) (*CompiledIdempotency, error) {
	headerName := cfg.HeaderName
	if headerName == "" {
		headerName = "Idempotency-Key"
	}

	ttl := cfg.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}

	methods := make(map[string]bool)
	if len(cfg.Methods) > 0 {
		for _, m := range cfg.Methods {
			methods[strings.ToUpper(m)] = true
		}
	} else {
		methods["POST"] = true
		methods["PUT"] = true
		methods["PATCH"] = true
	}

	keyScope := cfg.KeyScope
	if keyScope == "" {
		keyScope = "global"
	}

	maxKeyLength := cfg.MaxKeyLength
	if maxKeyLength == 0 {
		maxKeyLength = 256
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
		store = NewRedisStore(redisClient, "gw:idem:"+routeID+":")
	} else {
		store = NewMemoryStore(ttl)
	}

	return &CompiledIdempotency{
		headerName:   headerName,
		ttl:          ttl,
		methods:      methods,
		enforce:      cfg.Enforce,
		keyScope:     keyScope,
		maxKeyLength: maxKeyLength,
		maxBodySize:  maxBodySize,
		store:        store,
		metrics:      &IdempotencyMetrics{},
		routeID:      routeID,
		mode:         mode,
		inflight:     make(map[string]*inflightEntry),
	}, nil
}

// CheckResult indicates the outcome of an idempotency check.
type CheckResult int

const (
	// ResultProceed means the request should proceed to the backend.
	ResultProceed CheckResult = iota
	// ResultCached means a cached response was found and should be replayed.
	ResultCached
	// ResultReject means the request should be rejected (missing key + enforce).
	ResultReject
	// ResultInvalid means the key is invalid (too long).
	ResultInvalid
	// ResultWaited means the request waited for an in-flight request and should replay its result.
	ResultWaited
)

// CheckOutcome holds the result of an idempotency check.
type CheckOutcome struct {
	Result   CheckResult
	Response *StoredResponse
	Key      string
}

// Check validates the idempotency key for the request.
func (c *CompiledIdempotency) Check(r *http.Request) CheckOutcome {
	c.metrics.TotalRequests.Add(1)

	// Skip non-configured methods
	if !c.methods[r.Method] {
		return CheckOutcome{Result: ResultProceed}
	}

	key := r.Header.Get(c.headerName)
	if key == "" {
		if c.enforce {
			c.metrics.Enforced.Add(1)
			return CheckOutcome{Result: ResultReject}
		}
		return CheckOutcome{Result: ResultProceed}
	}

	// Validate key length
	if len(key) > c.maxKeyLength {
		c.metrics.InvalidKey.Add(1)
		return CheckOutcome{Result: ResultInvalid}
	}

	// Scope the key by client ID if configured
	scopedKey := key
	if c.keyScope == "per_client" {
		varCtx := variables.GetFromRequest(r)
		if varCtx.Identity != nil && varCtx.Identity.ClientID != "" {
			scopedKey = varCtx.Identity.ClientID + ":" + key
		}
	}

	// Check store for existing response
	stored, err := c.store.Get(r.Context(), scopedKey)
	if err != nil {
		c.metrics.StoreErrors.Add(1)
		// Fail-open: let request proceed
		return CheckOutcome{Result: ResultProceed, Key: scopedKey}
	}
	if stored != nil {
		c.metrics.CacheHits.Add(1)
		return CheckOutcome{Result: ResultCached, Response: stored, Key: scopedKey}
	}

	// Check in-flight map
	c.mu.Lock()
	if entry, ok := c.inflight[scopedKey]; ok {
		c.mu.Unlock()
		c.metrics.InFlightWaits.Add(1)

		// Wait for the in-flight request to complete or context to cancel
		select {
		case <-entry.done:
			if entry.resp != nil {
				return CheckOutcome{Result: ResultWaited, Response: entry.resp, Key: scopedKey}
			}
			// In-flight was cancelled without storing — let this request proceed
			return CheckOutcome{Result: ResultProceed, Key: scopedKey}
		case <-r.Context().Done():
			return CheckOutcome{Result: ResultReject}
		}
	}

	// Register as in-flight
	entry := &inflightEntry{done: make(chan struct{})}
	c.inflight[scopedKey] = entry
	c.mu.Unlock()

	c.metrics.CacheMisses.Add(1)
	return CheckOutcome{Result: ResultProceed, Key: scopedKey}
}

// RecordResponse stores the response and closes the in-flight channel.
func (c *CompiledIdempotency) RecordResponse(key string, resp *StoredResponse) {
	if key == "" {
		return
	}

	// Enforce max body size
	if c.maxBodySize > 0 && int64(len(resp.Body)) > c.maxBodySize {
		c.CancelInFlight(key)
		return
	}

	if err := c.store.Set(context.Background(), key, resp, c.ttl); err != nil {
		c.metrics.StoreErrors.Add(1)
	} else {
		c.metrics.ResponsesStored.Add(1)
	}

	c.mu.Lock()
	if entry, ok := c.inflight[key]; ok {
		entry.resp = resp
		close(entry.done)
		delete(c.inflight, key)
	}
	c.mu.Unlock()
}

// CancelInFlight closes the in-flight channel without storing a response.
func (c *CompiledIdempotency) CancelInFlight(key string) {
	if key == "" {
		return
	}
	c.mu.Lock()
	if entry, ok := c.inflight[key]; ok {
		close(entry.done)
		delete(c.inflight, key)
	}
	c.mu.Unlock()
}

// Status returns the admin status snapshot.
func (c *CompiledIdempotency) Status() IdempotencyStatus {
	return IdempotencyStatus{
		HeaderName:      c.headerName,
		TTL:             c.ttl.String(),
		Enforce:         c.enforce,
		KeyScope:        c.keyScope,
		Mode:            c.mode,
		TotalRequests:   c.metrics.TotalRequests.Load(),
		CacheHits:       c.metrics.CacheHits.Load(),
		CacheMisses:     c.metrics.CacheMisses.Load(),
		InFlightWaits:   c.metrics.InFlightWaits.Load(),
		Enforced:        c.metrics.Enforced.Load(),
		InvalidKey:      c.metrics.InvalidKey.Load(),
		StoreErrors:     c.metrics.StoreErrors.Load(),
		ResponsesStored: c.metrics.ResponsesStored.Load(),
	}
}

// Close releases store resources.
func (c *CompiledIdempotency) Close() {
	c.store.Close()
}

// CapturingWriter wraps http.ResponseWriter to capture the response for storage.
type CapturingWriter struct {
	http.ResponseWriter
	statusCode  int
	body        bytes.Buffer
	wroteHeader bool
}

// NewCapturingWriter creates a new CapturingWriter wrapping w.
func NewCapturingWriter(w http.ResponseWriter) *CapturingWriter {
	return &CapturingWriter{ResponseWriter: w, statusCode: 200}
}

func (w *CapturingWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *CapturingWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(200)
	}
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *CapturingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ToStoredResponse builds a StoredResponse from the captured data.
func (w *CapturingWriter) ToStoredResponse() *StoredResponse {
	return &StoredResponse{
		StatusCode: w.statusCode,
		Headers:    w.ResponseWriter.Header().Clone(),
		Body:       w.body.Bytes(),
	}
}

// ReplayResponse writes a stored response to the client with X-Idempotent-Replayed header.
func ReplayResponse(w http.ResponseWriter, resp *StoredResponse) {
	for k, vv := range resp.Headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Idempotent-Replayed", "true")
	w.WriteHeader(resp.StatusCode)
	w.Write(resp.Body)
}

// MergeIdempotencyConfig merges per-route overrides onto global config.
func MergeIdempotencyConfig(perRoute, global config.IdempotencyConfig) config.IdempotencyConfig {
	merged := global
	merged.Enabled = perRoute.Enabled

	if perRoute.HeaderName != "" {
		merged.HeaderName = perRoute.HeaderName
	}
	if perRoute.TTL > 0 {
		merged.TTL = perRoute.TTL
	}
	if len(perRoute.Methods) > 0 {
		merged.Methods = perRoute.Methods
	}
	if perRoute.Enforce {
		merged.Enforce = perRoute.Enforce
	}
	if perRoute.KeyScope != "" {
		merged.KeyScope = perRoute.KeyScope
	}
	if perRoute.Mode != "" {
		merged.Mode = perRoute.Mode
	}
	if perRoute.MaxKeyLength > 0 {
		merged.MaxKeyLength = perRoute.MaxKeyLength
	}
	if perRoute.MaxBodySize > 0 {
		merged.MaxBodySize = perRoute.MaxBodySize
	}

	return merged
}
