package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/variables"
)

// TokenBucket implements token bucket rate limiting
type TokenBucket struct {
	rate       float64       // tokens per second
	burst      int           // max tokens
	burstStr   string        // cached strconv.Itoa(burst) for headers
	period     time.Duration // refill period
	buckets    *shardedMap[*bucket]
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
	Key    string        // custom key extraction strategy
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
		burstStr:   strconv.Itoa(cfg.Burst),
		period:     cfg.Period,
		buckets:    newShardedMap[*bucket](),
		cleanupInt: 5 * time.Minute,
	}

	// Start cleanup goroutine
	go tb.cleanup()

	return tb
}

// Allow checks if a request should be allowed
func (tb *TokenBucket) Allow(key string) (allowed bool, remaining int, resetTime time.Time) {
	now := time.Now()

	s := tb.buckets.getShard(key)
	s.mu.Lock()

	b, exists := s.items[key]
	if !exists {
		b = &bucket{
			tokens:    float64(tb.burst),
			lastTime:  now,
			maxTokens: tb.burst,
		}
		s.items[key] = b
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
		remaining = int(b.tokens)
		s.mu.Unlock()
		return true, remaining, resetTime
	}

	// Calculate time until next token
	waitTime := time.Duration((1 - b.tokens) / tb.rate * float64(time.Second))
	resetTime = now.Add(waitTime)
	s.mu.Unlock()

	return false, 0, resetTime
}

// cleanup removes stale buckets periodically
func (tb *TokenBucket) cleanup() {
	ticker := time.NewTicker(tb.cleanupInt)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		cutoff := 2 * tb.period
		tb.buckets.deleteFunc(func(_ string, b *bucket) bool {
			return now.Sub(b.lastTime) > cutoff
		})
	}
}

// Limiter provides rate limiting middleware
type Limiter struct {
	tb    *TokenBucket
	perIP bool
	keyFn func(*http.Request) string
}

// NewLimiter creates a new rate limiter
func NewLimiter(cfg Config) *Limiter {
	return &Limiter{
		tb:    NewTokenBucket(cfg),
		perIP: cfg.PerIP,
		keyFn: BuildKeyFunc(cfg.PerIP, cfg.Key),
	}
}

// BuildKeyFunc returns a key extraction function based on configuration.
// All strategies fall back to client IP when the specified value is absent.
func BuildKeyFunc(perIP bool, key string) func(*http.Request) string {
	if perIP || key == "ip" {
		return func(r *http.Request) string {
			return variables.ExtractClientIP(r)
		}
	}

	if key == "client_id" {
		return func(r *http.Request) string {
			varCtx := variables.GetFromRequest(r)
			if varCtx.Identity != nil && varCtx.Identity.ClientID != "" {
				return varCtx.Identity.ClientID
			}
			return variables.ExtractClientIP(r)
		}
	}

	if strings.HasPrefix(key, "header:") {
		name := key[len("header:"):]
		prefix := "header:" + name + ":"
		return func(r *http.Request) string {
			if v := r.Header.Get(name); v != "" {
				return prefix + v
			}
			return variables.ExtractClientIP(r)
		}
	}

	if strings.HasPrefix(key, "cookie:") {
		name := key[len("cookie:"):]
		prefix := "cookie:" + name + ":"
		return func(r *http.Request) string {
			if c, err := r.Cookie(name); err == nil && c.Value != "" {
				return prefix + c.Value
			}
			return variables.ExtractClientIP(r)
		}
	}

	if strings.HasPrefix(key, "jwt_claim:") {
		claim := key[len("jwt_claim:"):]
		prefix := "jwt_claim:" + claim + ":"
		return func(r *http.Request) string {
			varCtx := variables.GetFromRequest(r)
			if varCtx.Identity != nil && varCtx.Identity.Claims != nil {
				if val, ok := varCtx.Identity.Claims[claim]; ok {
					var s string
					switch v := val.(type) {
					case string:
						s = v
					case float64:
						s = strconv.FormatFloat(v, 'f', -1, 64)
					case int:
						s = strconv.Itoa(v)
					case bool:
						s = strconv.FormatBool(v)
					default:
						s = fmt.Sprintf("%v", v)
					}
					if s != "" {
						return prefix + s
					}
				}
			}
			return variables.ExtractClientIP(r)
		}
	}

	// Default: client ID if authenticated, else IP
	return func(r *http.Request) string {
		varCtx := variables.GetFromRequest(r)
		if varCtx.Identity != nil && varCtx.Identity.ClientID != "" {
			return varCtx.Identity.ClientID
		}
		return variables.ExtractClientIP(r)
	}
}

// SetKeyFunc sets a custom key function for rate limiting
func (l *Limiter) SetKeyFunc(fn func(*http.Request) string) {
	l.keyFn = fn
}

// Middleware creates a rate limiting middleware
func (l *Limiter) Middleware() middleware.Middleware {
	burstStr := l.tb.burstStr
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := l.keyFn(r)

			allowed, remaining, resetTime := l.tb.Allow(key)

			// Set rate limit headers
			w.Header().Set("X-RateLimit-Limit", burstStr)
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

// TieredLimiter provides per-tier rate limiting, each tier having independent limits.
type TieredLimiter struct {
	tiers       map[string]*TokenBucket
	tierKeyFn   func(*http.Request) string
	keyFn       func(*http.Request) string
	defaultTier string
}

// TieredConfig holds tiered rate limiter configuration.
type TieredConfig struct {
	Tiers       map[string]Config // per-tier limits
	TierKey     string            // "header:<name>" or "jwt_claim:<name>"
	DefaultTier string            // fallback tier
	KeyFn       func(*http.Request) string // per-client key function
}

// NewTieredLimiter creates a new tiered rate limiter.
func NewTieredLimiter(cfg TieredConfig) *TieredLimiter {
	tl := &TieredLimiter{
		tiers:       make(map[string]*TokenBucket, len(cfg.Tiers)),
		tierKeyFn:   buildTierKeyFunc(cfg.TierKey),
		keyFn:       cfg.KeyFn,
		defaultTier: cfg.DefaultTier,
	}
	if tl.keyFn == nil {
		tl.keyFn = func(r *http.Request) string {
			return variables.ExtractClientIP(r)
		}
	}
	for name, tc := range cfg.Tiers {
		tl.tiers[name] = NewTokenBucket(tc)
	}
	return tl
}

// buildTierKeyFunc returns a function that extracts the tier name from a request.
func buildTierKeyFunc(tierKey string) func(*http.Request) string {
	if strings.HasPrefix(tierKey, "header:") {
		name := tierKey[len("header:"):]
		return func(r *http.Request) string {
			return r.Header.Get(name)
		}
	}
	if strings.HasPrefix(tierKey, "jwt_claim:") {
		claim := tierKey[len("jwt_claim:"):]
		return func(r *http.Request) string {
			varCtx := variables.GetFromRequest(r)
			if varCtx.Identity != nil && varCtx.Identity.Claims != nil {
				if val, ok := varCtx.Identity.Claims[claim]; ok {
					switch v := val.(type) {
					case string:
						return v
					case float64:
						return strconv.FormatFloat(v, 'f', -1, 64)
					case int:
						return strconv.Itoa(v)
					case bool:
						return strconv.FormatBool(v)
					default:
						return fmt.Sprintf("%v", v)
					}
				}
			}
			return ""
		}
	}
	return func(r *http.Request) string { return "" }
}

// Middleware returns a middleware that applies tiered rate limiting.
func (tl *TieredLimiter) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Determine tier (rule override takes precedence)
			tierName := tl.tierKeyFn(r)
			if varCtx := variables.GetFromRequest(r); varCtx.Overrides != nil && varCtx.Overrides.RateLimitTier != "" {
				tierName = varCtx.Overrides.RateLimitTier
			}
			tb, ok := tl.tiers[tierName]
			if !ok {
				tb = tl.tiers[tl.defaultTier]
			}
			if tb == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Rate limit within tier using the per-client key
			key := tl.keyFn(r)
			allowed, remaining, resetTime := tb.Allow(key)

			w.Header().Set("X-RateLimit-Limit", tb.burstStr)
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))
			w.Header().Set("X-RateLimit-Tier", tierName)

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

// rateLimiterVariant wraps different rate limiter implementations behind a single type.
type rateLimiterVariant struct {
	algorithm string                // "token_bucket", "sliding_window", "tiered"
	mode      string                // "local" or "distributed"
	local     *Limiter              // set for token_bucket
	sliding   *SlidingWindowLimiter // set for local sliding_window
	redis     *RedisLimiter         // set for distributed sliding_window
	tiered    *TieredLimiter        // set for tiered
}

func (v *rateLimiterVariant) Middleware() middleware.Middleware {
	switch {
	case v.tiered != nil:
		return v.tiered.Middleware()
	case v.redis != nil:
		return v.redis.Middleware()
	case v.sliding != nil:
		return v.sliding.Middleware()
	default:
		return v.local.Middleware()
	}
}

// RateLimitByRoute manages per-route rate limiters backed by byroute.Manager.
type RateLimitByRoute struct {
	byroute.Manager[*rateLimiterVariant]
}

// NewRateLimitByRoute creates a new route-based rate limiter.
func NewRateLimitByRoute() *RateLimitByRoute {
	return &RateLimitByRoute{}
}

// AddRoute adds a token-bucket rate limiter for a specific route.
func (rl *RateLimitByRoute) AddRoute(routeID string, cfg Config) {
	rl.Add(routeID, &rateLimiterVariant{
		algorithm: "token_bucket",
		mode:      "local",
		local:     NewLimiter(cfg),
	})
}

// AddRouteDistributed adds a Redis-backed rate limiter for a specific route.
func (rl *RateLimitByRoute) AddRouteDistributed(routeID string, cfg RedisLimiterConfig) {
	rl.Add(routeID, &rateLimiterVariant{
		algorithm: "sliding_window",
		mode:      "distributed",
		redis:     NewRedisLimiter(cfg),
	})
}

// AddRouteSlidingWindow adds a sliding window rate limiter for a specific route.
func (rl *RateLimitByRoute) AddRouteSlidingWindow(routeID string, cfg Config) {
	rl.Add(routeID, &rateLimiterVariant{
		algorithm: "sliding_window",
		mode:      "local",
		sliding:   NewSlidingWindowLimiter(cfg),
	})
}

// AddRouteTiered adds a tiered rate limiter for a specific route.
func (rl *RateLimitByRoute) AddRouteTiered(routeID string, cfg TieredConfig) {
	rl.Add(routeID, &rateLimiterVariant{
		algorithm: "tiered",
		mode:      "local",
		tiered:    NewTieredLimiter(cfg),
	})
}

// GetLimiter returns the local token-bucket rate limiter for a route, or nil.
func (rl *RateLimitByRoute) GetLimiter(routeID string) *Limiter {
	if v := rl.Lookup(routeID); v != nil {
		return v.local
	}
	return nil
}

// GetDistributedLimiter returns the Redis rate limiter for a route, or nil.
func (rl *RateLimitByRoute) GetDistributedLimiter(routeID string) *RedisLimiter {
	if v := rl.Lookup(routeID); v != nil {
		return v.redis
	}
	return nil
}

// GetSlidingWindowLimiter returns the sliding window rate limiter for a route, or nil.
func (rl *RateLimitByRoute) GetSlidingWindowLimiter(routeID string) *SlidingWindowLimiter {
	if v := rl.Lookup(routeID); v != nil {
		return v.sliding
	}
	return nil
}

// GetTieredLimiter returns the tiered rate limiter for a route, or nil.
func (rl *RateLimitByRoute) GetTieredLimiter(routeID string) *TieredLimiter {
	if v := rl.Lookup(routeID); v != nil {
		return v.tiered
	}
	return nil
}

// GetMiddleware returns the middleware for a route, or nil if no limiter is configured.
func (rl *RateLimitByRoute) GetMiddleware(routeID string) middleware.Middleware {
	if v := rl.Lookup(routeID); v != nil {
		return v.Middleware()
	}
	return nil
}

// LimiterInfo returns the mode and algorithm for a route's rate limiter.
func (rl *RateLimitByRoute) LimiterInfo(routeID string) (mode, algorithm string) {
	if v := rl.Lookup(routeID); v != nil {
		return v.mode, v.algorithm
	}
	return "", ""
}
