package circuitbreaker

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/config"
)

// Lua script: atomically check state and decide whether to allow a request.
// Keys: state, failures, successes, opened_at, half_open_count
// Args: timeout_seconds, max_requests, now_unix
// Returns: [allowed(0/1), state_string]
var allowScript = redis.NewScript(`
local state = redis.call('GET', KEYS[1]) or 'closed'
local timeout = tonumber(ARGV[1])
local max_requests = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

if state == 'open' then
    local opened_at = tonumber(redis.call('GET', KEYS[4]) or '0')
    if now - opened_at >= timeout then
        -- Transition open -> half-open
        redis.call('SET', KEYS[1], 'half-open')
        redis.call('SET', KEYS[5], '1')
        redis.call('SET', KEYS[3], '0')
        local ttl = timeout * 2
        redis.call('EXPIRE', KEYS[1], ttl)
        redis.call('EXPIRE', KEYS[5], ttl)
        redis.call('EXPIRE', KEYS[3], ttl)
        return {1, 'half-open'}
    end
    return {0, 'open'}
end

if state == 'half-open' then
    local count = tonumber(redis.call('GET', KEYS[5]) or '0')
    if count >= max_requests then
        return {0, 'half-open'}
    end
    redis.call('INCR', KEYS[5])
    return {1, 'half-open'}
end

-- closed: always allow
return {1, 'closed'}
`)

// Lua script: report success or failure and handle state transitions.
// Keys: state, failures, successes, opened_at, half_open_count
// Args: is_failure(0/1), failure_threshold, timeout_seconds
// Returns: [new_state, old_state]
var reportScript = redis.NewScript(`
local state = redis.call('GET', KEYS[1]) or 'closed'
local is_failure = tonumber(ARGV[1])
local threshold = tonumber(ARGV[2])
local timeout = tonumber(ARGV[3])
local ttl = timeout * 2
local old_state = state

if state == 'closed' then
    if is_failure == 1 then
        local failures = redis.call('INCR', KEYS[2])
        redis.call('EXPIRE', KEYS[2], ttl)
        if failures >= threshold then
            redis.call('SET', KEYS[1], 'open')
            redis.call('SET', KEYS[4], tostring(redis.call('TIME')[1]))
            redis.call('SET', KEYS[2], '0')
            redis.call('EXPIRE', KEYS[1], ttl)
            redis.call('EXPIRE', KEYS[4], ttl)
            redis.call('EXPIRE', KEYS[2], ttl)
            return {'open', old_state}
        end
    else
        redis.call('SET', KEYS[2], '0')
        redis.call('EXPIRE', KEYS[2], ttl)
    end
    return {'closed', old_state}
end

if state == 'half-open' then
    if is_failure == 1 then
        -- half-open -> open
        redis.call('SET', KEYS[1], 'open')
        redis.call('SET', KEYS[4], tostring(redis.call('TIME')[1]))
        redis.call('SET', KEYS[2], '0')
        redis.call('SET', KEYS[3], '0')
        redis.call('SET', KEYS[5], '0')
        redis.call('EXPIRE', KEYS[1], ttl)
        redis.call('EXPIRE', KEYS[4], ttl)
        redis.call('EXPIRE', KEYS[2], ttl)
        redis.call('EXPIRE', KEYS[3], ttl)
        redis.call('EXPIRE', KEYS[5], ttl)
        return {'open', old_state}
    else
        local successes = redis.call('INCR', KEYS[3])
        redis.call('EXPIRE', KEYS[3], ttl)
        local ho_count = tonumber(redis.call('GET', KEYS[5]) or '0')
        if successes >= ho_count then
            -- half-open -> closed
            redis.call('SET', KEYS[1], 'closed')
            redis.call('SET', KEYS[2], '0')
            redis.call('SET', KEYS[3], '0')
            redis.call('SET', KEYS[5], '0')
            redis.call('EXPIRE', KEYS[1], ttl)
            redis.call('EXPIRE', KEYS[2], ttl)
            redis.call('EXPIRE', KEYS[3], ttl)
            redis.call('EXPIRE', KEYS[5], ttl)
            return {'closed', old_state}
        end
    end
    return {'half-open', old_state}
end

return {state, old_state}
`)

// RedisBreaker is a distributed circuit breaker backed by Redis.
type RedisBreaker struct {
	client           *redis.Client
	keyPrefix        string
	failureThreshold int
	maxRequests      int
	timeout          time.Duration
	onStateChange    func(from, to string)

	// Lifetime counters (local per-instance, for admin stats)
	totalRequests  atomic.Int64
	totalSuccesses atomic.Int64
	totalFailures  atomic.Int64
	totalRejected  atomic.Int64
}

// NewRedisBreaker creates a distributed circuit breaker using Redis for shared state.
func NewRedisBreaker(routeID string, cfg config.CircuitBreakerConfig, client *redis.Client, onStateChange func(from, to string)) *RedisBreaker {
	failureThreshold := cfg.FailureThreshold
	if failureThreshold <= 0 {
		failureThreshold = 5
	}
	maxRequests := cfg.MaxRequests
	if maxRequests <= 0 {
		maxRequests = 1
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &RedisBreaker{
		client:           client,
		keyPrefix:        "gw:cb:" + routeID + ":",
		failureThreshold: failureThreshold,
		maxRequests:      maxRequests,
		timeout:          timeout,
		onStateChange:    onStateChange,
	}
}

func (rb *RedisBreaker) keys() []string {
	return []string{
		rb.keyPrefix + "state",
		rb.keyPrefix + "failures",
		rb.keyPrefix + "successes",
		rb.keyPrefix + "opened_at",
		rb.keyPrefix + "half_open_count",
	}
}

// Allow checks Redis state to decide if request is allowed. Fails open on Redis errors.
func (rb *RedisBreaker) Allow() (func(error), error) {
	rb.totalRequests.Add(1)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := allowScript.Run(ctx, rb.client, rb.keys(),
		int(rb.timeout.Seconds()),
		rb.maxRequests,
		time.Now().Unix(),
	).Int64Slice()

	if err != nil {
		// Fail open: allow request when Redis is unreachable
		return func(error) {}, nil
	}

	if result[0] == 0 {
		rb.totalRejected.Add(1)
		return nil, fmt.Errorf("circuit breaker is open")
	}

	return func(outcomeErr error) {
		rb.reportOutcome(outcomeErr)
	}, nil
}

func (rb *RedisBreaker) reportOutcome(outcomeErr error) {
	if outcomeErr != nil {
		rb.totalFailures.Add(1)
	} else {
		rb.totalSuccesses.Add(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	isFailure := 0
	if outcomeErr != nil {
		isFailure = 1
	}

	result, err := reportScript.Run(ctx, rb.client, rb.keys(),
		isFailure,
		rb.failureThreshold,
		int(rb.timeout.Seconds()),
	).StringSlice()

	if err != nil {
		return // fail silently
	}

	newState := result[0]
	oldState := result[1]
	if newState != oldState && rb.onStateChange != nil {
		rb.onStateChange(oldState, newState)
	}
}

// Snapshot returns a point-in-time view of the breaker state from Redis.
func (rb *RedisBreaker) Snapshot() BreakerSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	state := "closed"
	if s, err := rb.client.Get(ctx, rb.keyPrefix+"state").Result(); err == nil {
		state = s
	}

	return BreakerSnapshot{
		State:            state,
		Mode:             "distributed",
		FailureThreshold: rb.failureThreshold,
		MaxRequests:      rb.maxRequests,
		TotalRequests:    rb.totalRequests.Load(),
		TotalFailures:    rb.totalFailures.Load(),
		TotalSuccesses:   rb.totalSuccesses.Load(),
		TotalRejected:    rb.totalRejected.Load(),
	}
}
