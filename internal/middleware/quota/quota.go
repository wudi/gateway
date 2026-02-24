package quota

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/middleware/ratelimit"
)

// quotaEntry tracks usage within a billing window.
type quotaEntry struct {
	count       int64
	windowStart time.Time
}

// QuotaEnforcer enforces per-client quotas over billing periods.
type QuotaEnforcer struct {
	limit      int64
	period     string
	keyFn      func(*http.Request) string
	routeID    string

	// In-memory store
	entries sync.Map // map[string]*quotaEntry

	// Redis store (nil for in-memory mode)
	redisClient *redis.Client

	allowed  atomic.Int64
	rejected atomic.Int64
	stopCh   chan struct{}
}

// New creates a QuotaEnforcer.
func New(routeID string, cfg config.QuotaConfig, redisClient *redis.Client) *QuotaEnforcer {
	var rc *redis.Client
	if cfg.Redis && redisClient != nil {
		rc = redisClient
	}

	qe := &QuotaEnforcer{
		limit:       cfg.Limit,
		period:      cfg.Period,
		keyFn:       ratelimit.BuildKeyFunc(false, cfg.Key),
		routeID:     routeID,
		redisClient: rc,
		stopCh:      make(chan struct{}),
	}

	// Start background cleanup for in-memory entries
	if rc == nil {
		go qe.cleanup()
	}

	return qe
}

// Middleware returns quota enforcement middleware.
func (qe *QuotaEnforcer) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := qe.keyFn(r)
			windowStart, windowEnd := qe.currentWindow(time.Now())

			var count int64
			var err error

			if qe.redisClient != nil {
				count, err = qe.redisAllow(r.Context(), key, windowStart, windowEnd)
			} else {
				count, err = qe.memoryAllow(key, windowStart)
			}

			if err != nil {
				// On error, allow the request (fail open)
				next.ServeHTTP(w, r)
				return
			}

			remaining := qe.limit - count
			if remaining < 0 {
				remaining = 0
			}

			w.Header().Set("X-Quota-Limit", strconv.FormatInt(qe.limit, 10))
			w.Header().Set("X-Quota-Remaining", strconv.FormatInt(remaining, 10))
			w.Header().Set("X-Quota-Reset", strconv.FormatInt(windowEnd.Unix(), 10))

			if count > qe.limit {
				qe.rejected.Add(1)
				w.Header().Set("Retry-After", strconv.FormatInt(int64(time.Until(windowEnd).Seconds())+1, 10))
				http.Error(w, "Quota exceeded", http.StatusTooManyRequests)
				return
			}

			qe.allowed.Add(1)
			next.ServeHTTP(w, r)
		})
	}
}

func (qe *QuotaEnforcer) memoryAllow(key string, windowStart time.Time) (int64, error) {
	actual, _ := qe.entries.LoadOrStore(key, &quotaEntry{
		windowStart: windowStart,
	})
	entry := actual.(*quotaEntry)

	// Check if window has rolled over
	if !entry.windowStart.Equal(windowStart) {
		// Reset for new window
		entry.count = 0
		entry.windowStart = windowStart
	}

	entry.count++
	return entry.count, nil
}

func (qe *QuotaEnforcer) redisAllow(ctx context.Context, key string, windowStart, windowEnd time.Time) (int64, error) {
	rKey := fmt.Sprintf("quota:%s:%s:%d:%s", qe.routeID, qe.period, windowStart.Unix(), key)
	count, err := qe.redisClient.Incr(ctx, rKey).Result()
	if err != nil {
		return 0, err
	}

	// Set expiry on first increment
	if count == 1 {
		qe.redisClient.ExpireAt(ctx, rKey, windowEnd.Add(time.Minute))
	}

	return count, nil
}

// currentWindow returns the start and end of the current billing window.
func (qe *QuotaEnforcer) currentWindow(now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	switch qe.period {
	case "hourly":
		start := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, time.UTC)
		return start, start.Add(time.Hour)
	case "daily":
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 0, 1)
	case "monthly":
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 1, 0)
	case "yearly":
		start := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(1, 0, 0)
	default:
		// Default to daily
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 0, 1)
	}
}

// cleanup periodically removes expired in-memory entries.
func (qe *QuotaEnforcer) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			windowStart, _ := qe.currentWindow(now)
			qe.entries.Range(func(key, value any) bool {
				entry := value.(*quotaEntry)
				if !entry.windowStart.Equal(windowStart) {
					qe.entries.Delete(key)
				}
				return true
			})
		case <-qe.stopCh:
			return
		}
	}
}

// Close stops the background cleanup goroutine.
func (qe *QuotaEnforcer) Close() {
	select {
	case <-qe.stopCh:
	default:
		close(qe.stopCh)
	}
}

// Stats returns quota enforcer stats.
func (qe *QuotaEnforcer) Stats() map[string]interface{} {
	return map[string]interface{}{
		"limit":    qe.limit,
		"period":   qe.period,
		"allowed":  qe.allowed.Load(),
		"rejected": qe.rejected.Load(),
		"redis":    qe.redisClient != nil,
	}
}

// QuotaByRoute manages per-route quota enforcers.
type QuotaByRoute struct {
	byroute.Manager[*QuotaEnforcer]
}

// NewQuotaByRoute creates a new per-route quota manager.
func NewQuotaByRoute() *QuotaByRoute {
	return &QuotaByRoute{}
}

// AddRoute adds a quota enforcer for a route.
func (m *QuotaByRoute) AddRoute(routeID string, cfg config.QuotaConfig, redisClient *redis.Client) {
	m.Add(routeID, New(routeID, cfg, redisClient))
}

// GetEnforcer returns the quota enforcer for a route.
func (m *QuotaByRoute) GetEnforcer(routeID string) *QuotaEnforcer {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route quota stats.
func (m *QuotaByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(qe *QuotaEnforcer) interface{} { return qe.Stats() })
}

// CloseAll stops all background goroutines.
func (m *QuotaByRoute) CloseAll() {
	m.Range(func(_ string, qe *QuotaEnforcer) bool {
		qe.Close()
		return true
	})
}

// ValidateKey checks that a quota key format is valid.
func ValidateKey(key string) bool {
	switch key {
	case "ip", "client_id":
		return true
	}
	if strings.HasPrefix(key, "header:") && len(key) > len("header:") {
		return true
	}
	if strings.HasPrefix(key, "jwt_claim:") && len(key) > len("jwt_claim:") {
		return true
	}
	return false
}
