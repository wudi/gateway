package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/graphql"
)

// Entry represents a cached response.
type Entry struct {
	StatusCode   int
	Headers      http.Header
	Body         []byte
	ETag         string    // strong ETag, e.g. `"abc123def..."`
	LastModified time.Time // when entry was cached (or backend value)
}

// Handler manages caching for a single route.
type Handler struct {
	cache       *Cache
	ttl         time.Duration
	maxBodySize int64
	keyHeaders  []string
	methods     map[string]bool
	conditional bool
}

// NewHandler creates a new cache handler for a route with the given store backend.
func NewHandler(cfg config.CacheConfig, store Store) *Handler {
	methods := cfg.Methods
	if len(methods) == 0 {
		methods = []string{"GET"}
	}

	methodMap := make(map[string]bool, len(methods))
	for _, m := range methods {
		methodMap[strings.ToUpper(m)] = true
	}

	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}

	maxBodySize := cfg.MaxBodySize
	if maxBodySize <= 0 {
		maxBodySize = 1 << 20 // 1MB
	}

	// Pre-sort keyHeaders so BuildKey doesn't need to copy+sort per request
	keyHeaders := make([]string, len(cfg.KeyHeaders))
	copy(keyHeaders, cfg.KeyHeaders)
	sort.Strings(keyHeaders)

	return &Handler{
		cache:       New(store),
		ttl:         ttl,
		maxBodySize: maxBodySize,
		keyHeaders:  keyHeaders,
		methods:     methodMap,
		conditional: cfg.Conditional,
	}
}

// BuildKey constructs a cache key from the request.
// keyHeaders must already be sorted (done at construction time).
func (h *Handler) BuildKey(r *http.Request, keyHeaders []string) string {
	hash := sha256.New()
	io.WriteString(hash, r.Method)
	hash.Write([]byte{'|'})
	io.WriteString(hash, r.URL.Path)
	if r.URL.RawQuery != "" {
		hash.Write([]byte{'?'})
		io.WriteString(hash, r.URL.RawQuery)
	}

	for _, hdr := range keyHeaders {
		val := r.Header.Get(hdr)
		if val != "" {
			hash.Write([]byte{'|'})
			io.WriteString(hash, hdr)
			hash.Write([]byte{'='})
			io.WriteString(hash, val)
		}
	}

	// Include GraphQL operation info in cache key if present
	if gqlInfo := graphql.GetInfo(r.Context()); gqlInfo != nil {
		io.WriteString(hash, "|gql:")
		io.WriteString(hash, gqlInfo.OperationName)
		io.WriteString(hash, "|vars:")
		io.WriteString(hash, gqlInfo.VariablesHash)
	}

	return hex.EncodeToString(hash.Sum(nil))
}

// ShouldCache checks if the request is cacheable.
func (h *Handler) ShouldCache(r *http.Request) bool {
	if !h.methods[r.Method] {
		// Allow POST caching for GraphQL query operations
		if r.Method == http.MethodPost {
			if gqlInfo := graphql.GetInfo(r.Context()); gqlInfo != nil && gqlInfo.OperationType == "query" {
				goto cacheControlCheck
			}
		}
		return false
	}

cacheControlCheck:
	// Check Cache-Control headers
	cc := r.Header.Get("Cache-Control")
	if strings.Contains(cc, "no-store") || strings.Contains(cc, "no-cache") {
		return false
	}

	return true
}

// ShouldStore checks if the response should be stored in cache.
func (h *Handler) ShouldStore(statusCode int, headers http.Header, bodySize int64) bool {
	// Only cache successful responses
	if statusCode < 200 || statusCode >= 300 {
		return false
	}

	// Check Cache-Control
	cc := headers.Get("Cache-Control")
	if strings.Contains(cc, "no-store") {
		return false
	}

	// Check body size
	if bodySize > h.maxBodySize {
		return false
	}

	return true
}

// Get retrieves a cached response.
func (h *Handler) Get(r *http.Request) (*Entry, bool) {
	key := h.BuildKey(r, h.keyHeaders)
	return h.cache.Get(key)
}

// Store stores a response in the cache.
func (h *Handler) Store(r *http.Request, entry *Entry) {
	key := h.BuildKey(r, h.keyHeaders)
	h.cache.Set(key, entry)
}

// InvalidateByPath invalidates cache entries matching the request path prefix.
func (h *Handler) InvalidateByPath(path string) {
	// Use hash prefix won't work well here, so purge for mutation requests
	// This is a simplification; in production you'd want more granular invalidation
	h.cache.DeleteByPrefix(path)
}

// Stats returns cache statistics.
func (h *Handler) Stats() CacheStats {
	return h.cache.Stats()
}

// Purge clears all cache entries.
func (h *Handler) Purge() {
	h.cache.Purge()
}

// IsMutatingMethod returns true if the HTTP method may mutate resources.
func IsMutatingMethod(method string) bool {
	switch method {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

// CachingResponseWriter wraps http.ResponseWriter to capture the response for caching.
type CachingResponseWriter struct {
	http.ResponseWriter
	statusCode  int
	Body        bytes.Buffer
	wroteHeader bool
}

// NewCachingResponseWriter creates a new caching response writer.
func NewCachingResponseWriter(w http.ResponseWriter) *CachingResponseWriter {
	return &CachingResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

// WriteHeader captures the status code and writes it to the underlying writer.
func (crw *CachingResponseWriter) WriteHeader(code int) {
	if !crw.wroteHeader {
		crw.statusCode = code
		crw.wroteHeader = true
		crw.ResponseWriter.WriteHeader(code)
	}
}

// StatusCode returns the captured status code.
func (crw *CachingResponseWriter) StatusCode() int {
	return crw.statusCode
}

// Write captures the body and writes it to the underlying writer.
func (crw *CachingResponseWriter) Write(b []byte) (int, error) {
	crw.Body.Write(b)
	return crw.ResponseWriter.Write(b)
}

// Flush implements http.Flusher.
func (crw *CachingResponseWriter) Flush() {
	if flusher, ok := crw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack implements http.Hijacker for WebSocket support.
func (crw *CachingResponseWriter) Hijack() (c interface{}, brw interface{}, err error) {
	// This does not actually implement Hijack properly since we're buffering
	// WebSocket connections should bypass the cache layer
	return nil, nil, fmt.Errorf("hijack not supported on caching response writer")
}

// IsConditional returns true if conditional caching (ETag/Last-Modified/304) is enabled.
func (h *Handler) IsConditional() bool {
	return h.conditional
}

// RecordNotModified increments the 304 Not Modified counter.
func (h *Handler) RecordNotModified() {
	h.cache.RecordNotModified()
}

// GenerateETag generates a strong ETag from a response body using SHA-256.
func GenerateETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// PopulateConditionalFields sets ETag and LastModified on a cache entry.
// If the backend provided these headers, they are used; otherwise they are generated.
func PopulateConditionalFields(entry *Entry) {
	// Use backend-provided ETag if present
	if etag := entry.Headers.Get("ETag"); etag != "" {
		entry.ETag = etag
	} else {
		entry.ETag = GenerateETag(entry.Body)
	}

	// Use backend-provided Last-Modified if present and parseable
	if lm := entry.Headers.Get("Last-Modified"); lm != "" {
		if t, err := http.ParseTime(lm); err == nil {
			entry.LastModified = t.Truncate(time.Second)
			return
		}
	}
	entry.LastModified = time.Now().Truncate(time.Second)
}

// CheckConditional checks request conditional headers against a cache entry.
// Returns true if the client's cached version is still fresh (304 should be sent).
func CheckConditional(r *http.Request, entry *Entry) bool {
	// If-None-Match takes precedence per RFC 7232 §6
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		return etagMatch(inm, entry.ETag)
	}

	// Fall back to If-Modified-Since
	if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		t, err := http.ParseTime(ims)
		if err != nil {
			return false
		}
		return !entry.LastModified.After(t.Truncate(time.Second))
	}

	return false
}

// etagMatch checks if the given If-None-Match header value matches the entry's ETag.
// Handles wildcard `*` and comma-separated lists.
func etagMatch(inm, etag string) bool {
	if inm == "*" {
		return true
	}
	for _, candidate := range strings.Split(inm, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == etag {
			return true
		}
	}
	return false
}

// WriteCachedResponse writes a cached entry to the response writer.
// When conditional is true and the request has matching conditional headers,
// a 304 Not Modified is returned instead of the full body.
// Returns true if a 304 was sent.
func WriteCachedResponse(w http.ResponseWriter, r *http.Request, entry *Entry, conditional bool) bool {
	// Copy headers
	for key, values := range entry.Headers {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.Header().Set("X-Cache", "HIT")

	if conditional {
		// Inject conditional headers
		if entry.ETag != "" {
			w.Header().Set("ETag", entry.ETag)
		}
		if !entry.LastModified.IsZero() {
			w.Header().Set("Last-Modified", entry.LastModified.UTC().Format(http.TimeFormat))
		}

		// Check if client already has fresh content
		if CheckConditional(r, entry) {
			// Remove body-related headers for 304
			w.Header().Del("Content-Length")
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}

	w.WriteHeader(entry.StatusCode)
	w.Write(entry.Body)
	return false
}

// CacheByRoute manages cache handlers per route.
type CacheByRoute struct {
	handlers     map[string]*Handler
	bucketStores map[string]Store
	buckets      map[string]string // routeID → bucket name
	redisClient  *redis.Client
	mu           sync.RWMutex
}

// NewCacheByRoute creates a new route-based cache manager.
// Pass a non-nil redisClient to enable distributed caching for routes with mode "distributed".
func NewCacheByRoute(redisClient *redis.Client) *CacheByRoute {
	return &CacheByRoute{
		handlers:     make(map[string]*Handler),
		bucketStores: make(map[string]Store),
		buckets:      make(map[string]string),
		redisClient:  redisClient,
	}
}

// SetRedisClient sets the Redis client for distributed caching.
func (cbr *CacheByRoute) SetRedisClient(client *redis.Client) {
	cbr.mu.Lock()
	defer cbr.mu.Unlock()
	cbr.redisClient = client
}

// AddRoute adds a cache handler for a route.
func (cbr *CacheByRoute) AddRoute(routeID string, cfg config.CacheConfig) {
	cbr.mu.Lock()
	defer cbr.mu.Unlock()

	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}

	var store Store
	if cfg.Bucket != "" {
		// Shared bucket mode — reuse store if already created
		if existing, ok := cbr.bucketStores[cfg.Bucket]; ok {
			store = existing
		} else {
			store = cbr.createStore(cfg, "gw:cache:bucket:"+cfg.Bucket+":", ttl)
			cbr.bucketStores[cfg.Bucket] = store
		}
		cbr.buckets[routeID] = cfg.Bucket
	} else {
		store = cbr.createStore(cfg, "gw:cache:"+routeID+":", ttl)
	}

	cbr.handlers[routeID] = NewHandler(cfg, store)
}

// createStore creates a Store based on config mode.
func (cbr *CacheByRoute) createStore(cfg config.CacheConfig, redisPrefix string, ttl time.Duration) Store {
	if cfg.Mode == "distributed" && cbr.redisClient != nil {
		return NewRedisStore(cbr.redisClient, redisPrefix, ttl)
	}
	maxSize := cfg.MaxSize
	if maxSize <= 0 {
		maxSize = 1000
	}
	return NewMemoryStore(maxSize, ttl)
}

// GetHandler returns the cache handler for a route.
func (cbr *CacheByRoute) GetHandler(routeID string) *Handler {
	cbr.mu.RLock()
	defer cbr.mu.RUnlock()
	return cbr.handlers[routeID]
}

// RouteIDs returns all route IDs with cache handlers.
func (cbr *CacheByRoute) RouteIDs() []string {
	cbr.mu.RLock()
	defer cbr.mu.RUnlock()
	ids := make([]string, 0, len(cbr.handlers))
	for id := range cbr.handlers {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns cache statistics for all routes.
func (cbr *CacheByRoute) Stats() map[string]CacheStats {
	cbr.mu.RLock()
	defer cbr.mu.RUnlock()

	result := make(map[string]CacheStats, len(cbr.handlers))
	for id, h := range cbr.handlers {
		stats := h.Stats()
		if bucket, ok := cbr.buckets[id]; ok {
			stats.Bucket = bucket
		}
		result[id] = stats
	}
	return result
}
