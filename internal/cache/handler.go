package cache

import (
	"bytes"
	"crypto/sha256"
	"fmt"
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
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// Handler manages caching for a single route.
type Handler struct {
	cache       *Cache
	ttl         time.Duration
	maxBodySize int64
	keyHeaders  []string
	methods     map[string]bool
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

	return &Handler{
		cache:       New(store),
		ttl:         ttl,
		maxBodySize: maxBodySize,
		keyHeaders:  cfg.KeyHeaders,
		methods:     methodMap,
	}
}

// BuildKey constructs a cache key from the request.
func (h *Handler) BuildKey(r *http.Request, keyHeaders []string) string {
	var b strings.Builder
	b.WriteString(r.Method)
	b.WriteByte('|')
	b.WriteString(r.URL.Path)
	if r.URL.RawQuery != "" {
		b.WriteByte('?')
		b.WriteString(r.URL.RawQuery)
	}

	if len(keyHeaders) > 0 {
		// Sort headers for consistent key generation
		sorted := make([]string, len(keyHeaders))
		copy(sorted, keyHeaders)
		sort.Strings(sorted)

		for _, hdr := range sorted {
			val := r.Header.Get(hdr)
			if val != "" {
				b.WriteByte('|')
				b.WriteString(hdr)
				b.WriteByte('=')
				b.WriteString(val)
			}
		}
	}

	// Include GraphQL operation info in cache key if present
	if gqlInfo := graphql.GetInfo(r.Context()); gqlInfo != nil {
		b.WriteString("|gql:")
		b.WriteString(gqlInfo.OperationName)
		b.WriteString("|vars:")
		b.WriteString(gqlInfo.VariablesHash)
	}

	// Hash for a fixed-length key
	hash := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", hash)
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

// WriteCachedResponse writes a cached entry to the response writer.
func WriteCachedResponse(w http.ResponseWriter, entry *Entry) {
	// Copy headers
	for key, values := range entry.Headers {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.Header().Set("X-Cache", "HIT")
	w.WriteHeader(entry.StatusCode)
	w.Write(entry.Body)
}

// CacheByRoute manages cache handlers per route.
type CacheByRoute struct {
	handlers    map[string]*Handler
	redisClient *redis.Client
	mu          sync.RWMutex
}

// NewCacheByRoute creates a new route-based cache manager.
// Pass a non-nil redisClient to enable distributed caching for routes with mode "distributed".
func NewCacheByRoute(redisClient *redis.Client) *CacheByRoute {
	return &CacheByRoute{
		handlers:    make(map[string]*Handler),
		redisClient: redisClient,
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
	if cfg.Mode == "distributed" && cbr.redisClient != nil {
		store = NewRedisStore(cbr.redisClient, "gw:cache:"+routeID+":", ttl)
	} else {
		maxSize := cfg.MaxSize
		if maxSize <= 0 {
			maxSize = 1000
		}
		store = NewMemoryStore(maxSize, ttl)
	}

	cbr.handlers[routeID] = NewHandler(cfg, store)
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
		result[id] = h.Stats()
	}
	return result
}
