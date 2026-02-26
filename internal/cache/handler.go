package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/graphql"
	"github.com/wudi/runway/internal/middleware/tenant"
)

// Entry represents a cached response.
type Entry struct {
	StatusCode   int
	Headers      http.Header
	Body         []byte
	ETag         string        // strong ETag, e.g. `"abc123def..."`
	LastModified time.Time     // when entry was cached (or backend value)
	StoredAt     time.Time     // when this entry was stored (for staleness checks)
	Tags         []string      // cache tags for tag-based purge
	Path         string        // original request path for pattern-based purge
	TTL          time.Duration // per-entry TTL override (0 = use handler default)
}

// Handler manages caching for a single route.
type Handler struct {
	cache                *Cache
	ttl                  time.Duration
	maxBodySize          int64
	keyHeaders           []string
	methods              map[string]bool
	conditional          bool
	staleWhileRevalidate time.Duration
	staleIfError         time.Duration
	revalidating         sync.Map // key dedup for background refresh
	pathIndex            map[string]map[string]struct{} // path → set of cache keys
	pathMu               sync.RWMutex
	tagHeaders           []string // response headers to extract tags from
	staticTags           []string // static tags for all entries
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
		cache:                New(store),
		ttl:                  ttl,
		maxBodySize:          maxBodySize,
		keyHeaders:           keyHeaders,
		methods:              methodMap,
		conditional:          cfg.Conditional,
		staleWhileRevalidate: cfg.StaleWhileRevalidate,
		staleIfError:         cfg.StaleIfError,
		pathIndex:            make(map[string]map[string]struct{}),
		tagHeaders:           cfg.TagHeaders,
		staticTags:           cfg.Tags,
	}
}

// BuildKey constructs a cache key from the request.
// keyHeaders must already be sorted (done at construction time).
// If a tenant is resolved, its ID is prepended to isolate cache entries per tenant.
func (h *Handler) BuildKey(r *http.Request, keyHeaders []string) string {
	hash := sha256.New()
	if ti := tenant.FromContext(r.Context()); ti != nil {
		io.WriteString(hash, ti.ID)
		hash.Write([]byte{'|'})
	}
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

// KeyForRequest returns the cache key for a request using the handler's configured key headers.
func (h *Handler) KeyForRequest(r *http.Request) string {
	return h.BuildKey(r, h.keyHeaders)
}

// GetWithStaleness retrieves a cached response and indicates freshness.
// Returns (entry, fresh=true, stale=false) for fresh entries,
// (entry, fresh=false, stale=true) for stale-but-usable entries,
// and (nil, false, false) for cache misses or expired entries.
func (h *Handler) GetWithStaleness(key string) (entry *Entry, fresh bool, stale bool) {
	e, ok := h.cache.Get(key)
	if !ok {
		return nil, false, false
	}
	ttl := h.ttl
	if e.TTL > 0 {
		ttl = e.TTL
	}
	age := time.Since(e.StoredAt)
	if age <= ttl {
		return e, true, false
	}
	swr := h.staleWhileRevalidate
	sie := h.staleIfError
	maxStale := swr
	if sie > maxStale {
		maxStale = sie
	}
	if age <= ttl+maxStale {
		return e, false, true
	}
	return nil, false, false
}

// TTL returns the cache TTL duration.
func (h *Handler) TTL() time.Duration {
	return h.ttl
}

// HasStaleSupport returns true if stale-while-revalidate or stale-if-error is configured.
func (h *Handler) HasStaleSupport() bool {
	return h.staleWhileRevalidate > 0 || h.staleIfError > 0
}

// StaleWhileRevalidate returns the stale-while-revalidate duration.
func (h *Handler) StaleWhileRevalidate() time.Duration {
	return h.staleWhileRevalidate
}

// StaleIfError returns the stale-if-error duration.
func (h *Handler) StaleIfError() time.Duration {
	return h.staleIfError
}

// IsRevalidating checks if a background revalidation is already in-flight for the given key.
// Returns true if a revalidation was already running (caller should skip).
func (h *Handler) IsRevalidating(key string) bool {
	_, loaded := h.revalidating.LoadOrStore(key, struct{}{})
	return loaded
}

// DoneRevalidating marks a background revalidation as complete for the given key.
func (h *Handler) DoneRevalidating(key string) {
	h.revalidating.Delete(key)
}

// StoreByKey stores a response entry using a pre-computed cache key.
func (h *Handler) StoreByKey(key string, entry *Entry) {
	entry.StoredAt = time.Now()
	h.cache.Set(key, entry)
}

// Store stores a response in the cache.
func (h *Handler) Store(r *http.Request, entry *Entry) {
	entry.StoredAt = time.Now()
	key := h.BuildKey(r, h.keyHeaders)
	h.cache.Set(key, entry)
}

// StoreWithMeta stores a response in the cache with tag and path metadata.
// It extracts tags from configured response headers and static tags,
// and maintains a path→key reverse index for pattern-based purge.
func (h *Handler) StoreWithMeta(key, reqPath string, entry *Entry) {
	entry.StoredAt = time.Now()
	entry.Path = reqPath

	// Collect tags: static tags + header-extracted tags
	tags := h.extractTags(entry.Headers)
	entry.Tags = tags

	// Store with tags
	type tagSetter interface {
		SetWithTags(key string, entry *Entry, tags []string)
	}
	if ts, ok := h.cache.store.(tagSetter); ok && len(tags) > 0 {
		ts.SetWithTags(key, entry, tags)
	} else {
		h.cache.Set(key, entry)
	}

	// Update path index
	h.pathMu.Lock()
	if h.pathIndex[reqPath] == nil {
		h.pathIndex[reqPath] = make(map[string]struct{})
	}
	h.pathIndex[reqPath][key] = struct{}{}
	h.pathMu.Unlock()
}

// extractTags collects tags from static config and response headers.
func (h *Handler) extractTags(headers http.Header) []string {
	var tags []string
	tags = append(tags, h.staticTags...)
	for _, hdr := range h.tagHeaders {
		val := headers.Get(hdr)
		if val == "" {
			continue
		}
		// Split on comma and space
		for _, part := range strings.FieldsFunc(val, func(r rune) bool {
			return r == ',' || r == ' '
		}) {
			part = strings.TrimSpace(part)
			if part != "" {
				tags = append(tags, part)
			}
		}
	}
	return tags
}

// PurgeByPathPattern removes all entries whose path matches the given glob pattern.
// Returns count of purged entries.
func (h *Handler) PurgeByPathPattern(pattern string) int {
	h.pathMu.Lock()
	defer h.pathMu.Unlock()

	var count int
	for p, keys := range h.pathIndex {
		matched, err := path.Match(pattern, p)
		if err != nil || !matched {
			continue
		}
		for key := range keys {
			h.cache.Delete(key)
			count++
		}
		delete(h.pathIndex, p)
	}
	return count
}

// PurgeByTags removes all entries matching any of the given tags.
// Returns count of purged entries.
func (h *Handler) PurgeByTags(tags []string) int {
	count := h.cache.store.DeleteByTags(tags)

	// Clean up path index entries for deleted keys
	// We can't know exactly which keys were deleted, so we rebuild lazily
	// by removing path entries pointing to keys that no longer exist in the store.
	h.pathMu.Lock()
	for p, keys := range h.pathIndex {
		for key := range keys {
			if _, ok := h.cache.store.Get(key); !ok {
				delete(keys, key)
			}
		}
		if len(keys) == 0 {
			delete(h.pathIndex, p)
		}
	}
	h.pathMu.Unlock()

	return count
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

// CapturingResponseWriter captures the response without writing to any underlying writer.
// Used for background revalidation where there is no client to send to.
type CapturingResponseWriter struct {
	statusCode  int
	headers     http.Header
	Body        bytes.Buffer
	wroteHeader bool
}

// NewCapturingResponseWriter creates a new capturing response writer.
func NewCapturingResponseWriter() *CapturingResponseWriter {
	return &CapturingResponseWriter{
		statusCode: http.StatusOK,
		headers:    make(http.Header),
	}
}

// Header returns the response headers.
func (crw *CapturingResponseWriter) Header() http.Header {
	return crw.headers
}

// WriteHeader captures the status code.
func (crw *CapturingResponseWriter) WriteHeader(code int) {
	if !crw.wroteHeader {
		crw.statusCode = code
		crw.wroteHeader = true
	}
}

// StatusCode returns the captured status code.
func (crw *CapturingResponseWriter) StatusCode() int {
	return crw.statusCode
}

// Write captures the body.
func (crw *CapturingResponseWriter) Write(b []byte) (int, error) {
	return crw.Body.Write(b)
}

// Flush implements http.Flusher (no-op).
func (crw *CapturingResponseWriter) Flush() {}

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
	byroute.Manager[*Handler]
	extraMu      sync.RWMutex
	bucketStores map[string]Store
	buckets      map[string]string // routeID → bucket name
	redisClient  *redis.Client
}

// NewCacheByRoute creates a new route-based cache manager.
// Pass a non-nil redisClient to enable distributed caching for routes with mode "distributed".
func NewCacheByRoute(redisClient *redis.Client) *CacheByRoute {
	return &CacheByRoute{
		redisClient: redisClient,
	}
}

// SetRedisClient sets the Redis client for distributed caching.
func (cbr *CacheByRoute) SetRedisClient(client *redis.Client) {
	cbr.extraMu.Lock()
	defer cbr.extraMu.Unlock()
	cbr.redisClient = client
}

// AddRoute adds a cache handler for a route.
func (cbr *CacheByRoute) AddRoute(routeID string, cfg config.CacheConfig) {
	cbr.extraMu.Lock()

	if cbr.bucketStores == nil {
		cbr.bucketStores = make(map[string]Store)
		cbr.buckets = make(map[string]string)
	}

	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}

	// Extend store TTL to cover stale window so entries survive beyond the fresh period.
	storeTTL := ttl
	staleMax := cfg.StaleWhileRevalidate
	if cfg.StaleIfError > staleMax {
		staleMax = cfg.StaleIfError
	}
	storeTTL += staleMax

	var store Store
	if cfg.Bucket != "" {
		// Shared bucket mode — reuse store if already created
		if existing, ok := cbr.bucketStores[cfg.Bucket]; ok {
			store = existing
		} else {
			store = cbr.createStore(cfg, "gw:cache:bucket:"+cfg.Bucket+":", storeTTL)
			cbr.bucketStores[cfg.Bucket] = store
		}
		cbr.buckets[routeID] = cfg.Bucket
	} else {
		store = cbr.createStore(cfg, "gw:cache:"+routeID+":", storeTTL)
	}

	cbr.extraMu.Unlock()
	cbr.Add(routeID, NewHandler(cfg, store))
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
	v, _ := cbr.Get(routeID)
	return v
}

// PurgeRoute purges all cache entries for a specific route. Returns pre-purge size and whether the route was found.
func (cbr *CacheByRoute) PurgeRoute(routeID string) (int, bool) {
	h := cbr.GetHandler(routeID)
	if h == nil {
		return 0, false
	}
	size := h.Stats().Size
	h.Purge()
	return size, true
}

// PurgeRouteKey deletes a specific key from a route's cache. Returns true if the route was found.
func (cbr *CacheByRoute) PurgeRouteKey(routeID, key string) bool {
	h := cbr.GetHandler(routeID)
	if h == nil {
		return false
	}
	h.cache.Delete(key)
	return true
}

// PurgeByPathPattern removes entries matching a glob pattern from a route's cache.
// Returns (count, true) if the route was found, (0, false) otherwise.
func (cbr *CacheByRoute) PurgeByPathPattern(routeID, pattern string) (int, bool) {
	h := cbr.GetHandler(routeID)
	if h == nil {
		return 0, false
	}
	return h.PurgeByPathPattern(pattern), true
}

// PurgeByTags removes entries matching any of the given tags from a route's cache.
// Returns (count, true) if the route was found, (0, false) otherwise.
func (cbr *CacheByRoute) PurgeByTags(routeID string, tags []string) (int, bool) {
	h := cbr.GetHandler(routeID)
	if h == nil {
		return 0, false
	}
	return h.PurgeByTags(tags), true
}

// PurgeAll purges all cache entries across all routes. Returns total pre-purge entry count.
func (cbr *CacheByRoute) PurgeAll() int {
	total := 0
	cbr.Range(func(_ string, h *Handler) bool {
		total += h.Stats().Size
		h.Purge()
		return true
	})
	return total
}

// Stats returns cache statistics for all routes.
func (cbr *CacheByRoute) Stats() map[string]CacheStats {
	cbr.extraMu.RLock()
	buckets := cbr.buckets
	cbr.extraMu.RUnlock()

	result := make(map[string]CacheStats)
	cbr.Range(func(id string, h *Handler) bool {
		stats := h.Stats()
		if bucket, ok := buckets[id]; ok {
			stats.Bucket = bucket
		}
		result[id] = stats
		return true
	})
	return result
}
