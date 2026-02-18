package spikearrest

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/variables"
	"golang.org/x/time/rate"
)

// SpikeArrester enforces continuous rate limiting with immediate rejection.
type SpikeArrester struct {
	global   *rate.Limiter // non-nil when !perIP
	perIP    bool
	limiters sync.Map // ip -> *ipEntry
	rps      rate.Limit
	burst    int
	allowed  atomic.Int64
	rejected atomic.Int64
}

type ipEntry struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64 // unix nano
}

// New creates a SpikeArrester from config.
func New(cfg config.SpikeArrestConfig) *SpikeArrester {
	burst := cfg.Burst
	if burst == 0 {
		burst = cfg.Rate
	}
	period := cfg.Period
	if period == 0 {
		period = time.Second
	}
	rps := rate.Limit(float64(cfg.Rate) / period.Seconds())

	sa := &SpikeArrester{
		perIP: cfg.PerIP,
		rps:   rps,
		burst: burst,
	}
	if !cfg.PerIP {
		sa.global = rate.NewLimiter(rps, burst)
	} else {
		// Start cleanup goroutine for per-IP limiters
		go sa.cleanup()
	}
	return sa
}

// Middleware returns a middleware that enforces spike arrest.
func (sa *SpikeArrester) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var limiter *rate.Limiter
			if sa.perIP {
				ip := variables.ExtractClientIP(r)
				entry, _ := sa.limiters.LoadOrStore(ip, &ipEntry{
					limiter: rate.NewLimiter(sa.rps, sa.burst),
				})
				e := entry.(*ipEntry)
				e.lastSeen.Store(time.Now().UnixNano())
				limiter = e.limiter
			} else {
				limiter = sa.global
			}

			if !limiter.Allow() {
				sa.rejected.Add(1)
				errors.New(http.StatusTooManyRequests, "Spike arrest: rate exceeded").WriteJSON(w)
				return
			}
			sa.allowed.Add(1)
			next.ServeHTTP(w, r)
		})
	}
}

// cleanup removes per-IP limiters that haven't been seen for 5 minutes.
func (sa *SpikeArrester) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	cutoff := 5 * time.Minute
	for range ticker.C {
		now := time.Now().UnixNano()
		sa.limiters.Range(func(key, value interface{}) bool {
			e := value.(*ipEntry)
			if now-e.lastSeen.Load() > cutoff.Nanoseconds() {
				sa.limiters.Delete(key)
			}
			return true
		})
	}
}

// Stats returns metrics for this arrester.
func (sa *SpikeArrester) Stats() map[string]interface{} {
	result := map[string]interface{}{
		"allowed":  sa.allowed.Load(),
		"rejected": sa.rejected.Load(),
		"per_ip":   sa.perIP,
	}
	if sa.perIP {
		count := 0
		sa.limiters.Range(func(_, _ interface{}) bool {
			count++
			return true
		})
		result["tracked_ips"] = count
	}
	return result
}

// MergeSpikeArrestConfig merges per-route config over global config.
func MergeSpikeArrestConfig(perRoute, global config.SpikeArrestConfig) config.SpikeArrestConfig {
	merged := global
	if perRoute.Rate > 0 {
		merged.Rate = perRoute.Rate
	}
	if perRoute.Period > 0 {
		merged.Period = perRoute.Period
	}
	if perRoute.Burst > 0 {
		merged.Burst = perRoute.Burst
	}
	if perRoute.PerIP {
		merged.PerIP = true
	}
	merged.Enabled = true
	return merged
}

// SpikeArrestByRoute manages per-route spike arresters.
type SpikeArrestByRoute struct {
	arresters map[string]*SpikeArrester
	mu        sync.RWMutex
}

// NewSpikeArrestByRoute creates a new per-route spike arrest manager.
func NewSpikeArrestByRoute() *SpikeArrestByRoute {
	return &SpikeArrestByRoute{}
}

// AddRoute adds a spike arrester for a route.
func (m *SpikeArrestByRoute) AddRoute(routeID string, cfg config.SpikeArrestConfig) {
	m.mu.Lock()
	if m.arresters == nil {
		m.arresters = make(map[string]*SpikeArrester)
	}
	m.arresters[routeID] = New(cfg)
	m.mu.Unlock()
}

// GetArrester returns the spike arrester for a route.
func (m *SpikeArrestByRoute) GetArrester(routeID string) *SpikeArrester {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.arresters[routeID]
}

// RouteIDs returns all route IDs with spike arresters.
func (m *SpikeArrestByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.arresters))
	for id := range m.arresters {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns per-route spike arrest metrics.
func (m *SpikeArrestByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := make(map[string]interface{}, len(m.arresters))
	for id, sa := range m.arresters {
		stats[id] = sa.Stats()
	}
	return stats
}
