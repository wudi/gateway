package ratelimit

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/example/gateway/internal/errors"
	"github.com/example/gateway/internal/middleware"
	"github.com/example/gateway/internal/variables"
)

// TokenBucket implements token bucket rate limiting
type TokenBucket struct {
	rate       float64       // tokens per second
	burst      int           // max tokens
	period     time.Duration // refill period
	buckets    map[string]*bucket
	mu         sync.Mutex
	cleanupInt time.Duration
}

type bucket struct {
	tokens    float64
	lastTime  time.Time
	maxTokens int
}

// Config holds rate limiter configuration
type Config struct {
	Rate   int           // requests per period
	Period time.Duration // time period
	Burst  int           // max burst size
	PerIP  bool          // rate limit per IP instead of globally
}

// NewTokenBucket creates a new token bucket rate limiter
func NewTokenBucket(cfg Config) *TokenBucket {
	if cfg.Period == 0 {
		cfg.Period = time.Minute
	}
	if cfg.Burst == 0 {
		cfg.Burst = cfg.Rate
	}

	tb := &TokenBucket{
		rate:       float64(cfg.Rate) / cfg.Period.Seconds(),
		burst:      cfg.Burst,
		period:     cfg.Period,
		buckets:    make(map[string]*bucket),
		cleanupInt: 5 * time.Minute,
	}

	// Start cleanup goroutine
	go tb.cleanup()

	return tb
}

// Allow checks if a request should be allowed
func (tb *TokenBucket) Allow(key string) (allowed bool, remaining int, resetTime time.Time) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()

	b, exists := tb.buckets[key]
	if !exists {
		b = &bucket{
			tokens:    float64(tb.burst),
			lastTime:  now,
			maxTokens: tb.burst,
		}
		tb.buckets[key] = b
	}

	// Add tokens based on time elapsed
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * tb.rate
	if b.tokens > float64(b.maxTokens) {
		b.tokens = float64(b.maxTokens)
	}
	b.lastTime = now

	// Calculate reset time
	resetTime = now.Add(tb.period)

	if b.tokens >= 1 {
		b.tokens--
		return true, int(b.tokens), resetTime
	}

	// Calculate time until next token
	waitTime := time.Duration((1 - b.tokens) / tb.rate * float64(time.Second))
	resetTime = now.Add(waitTime)

	return false, 0, resetTime
}

// cleanup removes stale buckets periodically
func (tb *TokenBucket) cleanup() {
	ticker := time.NewTicker(tb.cleanupInt)
	defer ticker.Stop()

	for range ticker.C {
		tb.mu.Lock()
		now := time.Now()
		for key, b := range tb.buckets {
			// Remove buckets that haven't been used in 2x the period
			if now.Sub(b.lastTime) > 2*tb.period {
				delete(tb.buckets, key)
			}
		}
		tb.mu.Unlock()
	}
}

// Limiter provides rate limiting middleware
type Limiter struct {
	tb     *TokenBucket
	perIP  bool
	keyFn  func(*http.Request) string
}

// NewLimiter creates a new rate limiter
func NewLimiter(cfg Config) *Limiter {
	return &Limiter{
		tb:    NewTokenBucket(cfg),
		perIP: cfg.PerIP,
		keyFn: defaultKeyFunc(cfg.PerIP),
	}
}

func defaultKeyFunc(perIP bool) func(*http.Request) string {
	return func(r *http.Request) string {
		if perIP {
			return variables.ExtractClientIP(r)
		}
		// Try to use authenticated client ID
		varCtx := variables.GetFromRequest(r)
		if varCtx.Identity != nil && varCtx.Identity.ClientID != "" {
			return varCtx.Identity.ClientID
		}
		// Fall back to IP
		return variables.ExtractClientIP(r)
	}
}

// SetKeyFunc sets a custom key function for rate limiting
func (l *Limiter) SetKeyFunc(fn func(*http.Request) string) {
	l.keyFn = fn
}

// Middleware creates a rate limiting middleware
func (l *Limiter) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := l.keyFn(r)

			allowed, remaining, resetTime := l.tb.Allow(key)

			// Set rate limit headers
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(l.tb.burst))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))

			if !allowed {
				retryAfter := int(time.Until(resetTime).Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				errors.ErrTooManyRequests.WriteJSON(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Allow checks if a request is allowed (for manual checking)
func (l *Limiter) Allow(r *http.Request) bool {
	key := l.keyFn(r)
	allowed, _, _ := l.tb.Allow(key)
	return allowed
}

// RateLimitMiddleware is the interface for both local and distributed rate limiters.
type RateLimitMiddleware interface {
	Middleware() middleware.Middleware
}

// RateLimitByRoute creates a map of rate limiters per route
type RateLimitByRoute struct {
	limiters      map[string]*Limiter
	distributed   map[string]*RedisLimiter
	slidingWindow map[string]*SlidingWindowLimiter
	mu            sync.RWMutex
}

// NewRateLimitByRoute creates a new route-based rate limiter
func NewRateLimitByRoute() *RateLimitByRoute {
	return &RateLimitByRoute{
		limiters:      make(map[string]*Limiter),
		distributed:   make(map[string]*RedisLimiter),
		slidingWindow: make(map[string]*SlidingWindowLimiter),
	}
}

// AddRoute adds a rate limiter for a specific route
func (rl *RateLimitByRoute) AddRoute(routeID string, cfg Config) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.limiters[routeID] = NewLimiter(cfg)
}

// AddRouteDistributed adds a Redis-backed rate limiter for a specific route.
func (rl *RateLimitByRoute) AddRouteDistributed(routeID string, cfg RedisLimiterConfig) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.distributed[routeID] = NewRedisLimiter(cfg)
}

// AddRouteSlidingWindow adds a sliding window rate limiter for a specific route.
func (rl *RateLimitByRoute) AddRouteSlidingWindow(routeID string, cfg Config) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.slidingWindow[routeID] = NewSlidingWindowLimiter(cfg)
}

// RouteIDs returns all route IDs with rate limiters.
func (rl *RateLimitByRoute) RouteIDs() []string {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	seen := make(map[string]bool)
	var ids []string
	for id := range rl.limiters {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	for id := range rl.distributed {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	for id := range rl.slidingWindow {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids
}

// GetSlidingWindowLimiter returns the sliding window rate limiter for a route.
func (rl *RateLimitByRoute) GetSlidingWindowLimiter(routeID string) *SlidingWindowLimiter {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.slidingWindow[routeID]
}

// GetLimiter returns the local rate limiter for a route
func (rl *RateLimitByRoute) GetLimiter(routeID string) *Limiter {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.limiters[routeID]
}

// GetDistributedLimiter returns the Redis rate limiter for a route.
func (rl *RateLimitByRoute) GetDistributedLimiter(routeID string) *RedisLimiter {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.distributed[routeID]
}

// GetMiddleware returns the appropriate middleware for a route (distributed > sliding_window > token_bucket).
func (rl *RateLimitByRoute) GetMiddleware(routeID string) middleware.Middleware {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	if dl, ok := rl.distributed[routeID]; ok {
		return dl.Middleware()
	}
	if sw, ok := rl.slidingWindow[routeID]; ok {
		return sw.Middleware()
	}
	if l, ok := rl.limiters[routeID]; ok {
		return l.Middleware()
	}
	return nil
}

// Middleware creates a middleware that rate limits based on route
func (rl *RateLimitByRoute) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)
			routeID := varCtx.RouteID

			rl.mu.RLock()
			limiter, exists := rl.limiters[routeID]
			rl.mu.RUnlock()

			if !exists || limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			key := limiter.keyFn(r)
			allowed, remaining, resetTime := limiter.tb.Allow(key)

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limiter.tb.burst))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))

			if !allowed {
				retryAfter := int(time.Until(resetTime).Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				errors.ErrTooManyRequests.WriteJSON(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
