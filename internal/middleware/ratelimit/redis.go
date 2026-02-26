package ratelimit

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/internal/logging"
	"github.com/wudi/runway/internal/middleware"
	"go.uber.org/zap"
)

// slidingWindowScript implements a sliding window rate limiter using Redis sorted sets.
// Returns: [allowed (0/1), remaining, resetTimestamp]
var slidingWindowScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])

-- Remove entries outside the window
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)

-- Count current entries
local count = redis.call('ZCARD', key)

if count < limit then
    -- Add the current request
    redis.call('ZADD', key, now, now .. '-' .. math.random(1000000))
    redis.call('PEXPIRE', key, window)
    return {1, limit - count - 1, now + window}
else
    -- Rejected
    local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
    local reset = now + window
    if #oldest >= 2 then
        reset = tonumber(oldest[2]) + window
    end
    return {0, 0, reset}
end
`)

// RedisLimiter provides Redis-backed distributed rate limiting.
type RedisLimiter struct {
	client *redis.Client
	prefix string
	rate   int
	window time.Duration
	burst  int
	perIP  bool
	keyFn  func(*http.Request) string
}

// RedisLimiterConfig holds config for creating a RedisLimiter.
type RedisLimiterConfig struct {
	Client *redis.Client
	Prefix string
	Rate   int
	Period time.Duration
	Burst  int
	PerIP  bool
	Key    string
}

// NewRedisLimiter creates a new Redis-backed rate limiter.
func NewRedisLimiter(cfg RedisLimiterConfig) *RedisLimiter {
	if cfg.Period == 0 {
		cfg.Period = time.Minute
	}
	if cfg.Burst == 0 {
		cfg.Burst = cfg.Rate
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "gw:rl:"
	}
	return &RedisLimiter{
		client: cfg.Client,
		prefix: cfg.Prefix,
		rate:   cfg.Burst, // burst is the window limit
		window: cfg.Period,
		burst:  cfg.Burst,
		perIP:  cfg.PerIP,
		keyFn:  BuildKeyFunc(cfg.PerIP, cfg.Key),
	}
}

// Middleware creates a rate limiting middleware.
func (rl *RedisLimiter) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := rl.prefix + rl.keyFn(r)

			ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
			defer cancel()

			nowMs := time.Now().UnixMilli()
			windowMs := rl.window.Milliseconds()

			result, err := slidingWindowScript.Run(ctx, rl.client,
				[]string{key},
				nowMs,
				windowMs,
				rl.rate,
			).Int64Slice()

			if err != nil {
				// Fail open: if Redis is unreachable, allow the request
				logging.Warn("Redis rate limit unavailable, failing open", zap.Error(err))
				next.ServeHTTP(w, r)
				return
			}

			allowed := result[0] == 1
			remaining := int(result[1])
			resetMs := result[2]
			resetTime := time.UnixMilli(resetMs)

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.burst))
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
