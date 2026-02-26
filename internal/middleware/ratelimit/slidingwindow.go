package ratelimit

import (
	"net/http"
	"strconv"
	"time"

	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/internal/middleware"
)

// window tracks counts for two adjacent fixed windows.
type window struct {
	prevCount int
	currCount int
	currStart time.Time
	lastUsed  time.Time
}

// SlidingWindowCounter implements the sliding window counter rate limiting algorithm.
// It interpolates between two adjacent fixed-time windows for near-perfect accuracy
// with O(1) memory per key.
type SlidingWindowCounter struct {
	rate       int
	period     time.Duration
	windows    *shardedMap[*window]
	cleanupInt time.Duration
}

// NewSlidingWindowCounter creates a new sliding window counter rate limiter.
func NewSlidingWindowCounter(cfg Config) *SlidingWindowCounter {
	if cfg.Period == 0 {
		cfg.Period = time.Minute
	}
	rate := cfg.Rate
	if cfg.Burst > 0 && cfg.Burst > rate {
		rate = cfg.Burst
	}

	sw := &SlidingWindowCounter{
		rate:       rate,
		period:     cfg.Period,
		windows:    newShardedMap[*window](),
		cleanupInt: 5 * time.Minute,
	}

	go sw.cleanup()

	return sw
}

// Allow checks if a request should be allowed using sliding window interpolation.
func (sw *SlidingWindowCounter) Allow(key string) (allowed bool, remaining int, resetTime time.Time) {
	now := time.Now()
	resetTime = now.Add(sw.period)

	s := sw.windows.getShard(key)
	s.mu.Lock()

	w, exists := s.items[key]
	if !exists {
		w = &window{
			currStart: now.Truncate(sw.period),
		}
		s.items[key] = w
	}

	// Rotate windows if we've moved past the current window
	for now.Sub(w.currStart) >= sw.period {
		w.prevCount = w.currCount
		w.currCount = 0
		w.currStart = w.currStart.Add(sw.period)
	}

	// If we're more than 2 periods past, prev is also zero
	if now.Sub(w.currStart) >= 2*sw.period {
		w.prevCount = 0
	}

	// Calculate weighted estimate
	elapsed := now.Sub(w.currStart)
	weight := 1.0 - float64(elapsed)/float64(sw.period)
	estimate := float64(w.prevCount)*weight + float64(w.currCount)

	// Reset time is the end of the current window
	resetTime = w.currStart.Add(sw.period)

	if estimate < float64(sw.rate) {
		w.currCount++
		w.lastUsed = now
		rem := float64(sw.rate) - estimate - 1
		if rem < 0 {
			rem = 0
		}
		s.mu.Unlock()
		return true, int(rem), resetTime
	}

	w.lastUsed = now
	s.mu.Unlock()
	return false, 0, resetTime
}

// cleanup removes stale windows periodically.
func (sw *SlidingWindowCounter) cleanup() {
	ticker := time.NewTicker(sw.cleanupInt)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		cutoff := 2 * sw.period
		sw.windows.deleteFunc(func(_ string, w *window) bool {
			return now.Sub(w.lastUsed) > cutoff
		})
	}
}

// SlidingWindowLimiter provides sliding window rate limiting middleware.
type SlidingWindowLimiter struct {
	sw    *SlidingWindowCounter
	perIP bool
	keyFn func(*http.Request) string
}

// NewSlidingWindowLimiter creates a new sliding window rate limiter.
func NewSlidingWindowLimiter(cfg Config) *SlidingWindowLimiter {
	return &SlidingWindowLimiter{
		sw:    NewSlidingWindowCounter(cfg),
		perIP: cfg.PerIP,
		keyFn: BuildKeyFunc(cfg.PerIP, cfg.Key),
	}
}

// Middleware creates a rate limiting middleware using the sliding window algorithm.
func (l *SlidingWindowLimiter) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := l.keyFn(r)

			allowed, remaining, resetTime := l.sw.Allow(key)

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(l.sw.rate))
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

// Allow checks if a request is allowed (for manual checking).
func (l *SlidingWindowLimiter) Allow(r *http.Request) bool {
	key := l.keyFn(r)
	allowed, _, _ := l.sw.Allow(key)
	return allowed
}

// ensure SlidingWindowLimiter implements RateLimitMiddleware
var _ RateLimitMiddleware = (*SlidingWindowLimiter)(nil)
