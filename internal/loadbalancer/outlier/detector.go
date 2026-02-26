package outlier

import (
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/variables"
)

// ejectionInfo tracks current ejection state for a backend.
type ejectionInfo struct {
	ejectedAt time.Time
	duration  time.Duration
	count     int
	reason    string
}

// DetectorSnapshot is a point-in-time view of a detector's state.
type DetectorSnapshot struct {
	RouteID         string                      `json:"route_id"`
	BackendStats    map[string]StatsSnapshot    `json:"backend_stats"`
	EjectedBackends map[string]EjectionSnapshot `json:"ejected_backends"`
	TotalEjections  int64                       `json:"total_ejections"`
	TotalRecoveries int64                       `json:"total_recoveries"`
}

// EjectionSnapshot is a point-in-time view of an ejection.
type EjectionSnapshot struct {
	EjectedAt time.Time `json:"ejected_at"`
	Duration  string    `json:"duration"`
	Count     int       `json:"count"`
	Reason    string    `json:"reason"`
}

// Detector is the core outlier detection engine for a single route.
type Detector struct {
	routeID  string
	cfg      config.OutlierDetectionConfig
	balancer loadbalancer.Balancer

	mu      sync.RWMutex
	stats   map[string]*BackendStats
	ejected map[string]*ejectionInfo

	totalEjections  atomic.Int64
	totalRecoveries atomic.Int64

	onEject   func(routeID, backend, reason string)
	onRecover func(routeID, backend string)

	stopCh chan struct{}
	done   chan struct{}
}

// NewDetector creates and starts a new outlier detector.
func NewDetector(routeID string, cfg config.OutlierDetectionConfig, balancer loadbalancer.Balancer) *Detector {
	applyDefaults(&cfg)

	d := &Detector{
		routeID:  routeID,
		cfg:      cfg,
		balancer: balancer,
		stats:    make(map[string]*BackendStats),
		ejected:  make(map[string]*ejectionInfo),
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}

	go d.runDetectionLoop()
	return d
}

// SetCallbacks sets the ejection and recovery callbacks.
func (d *Detector) SetCallbacks(onEject func(routeID, backend, reason string), onRecover func(routeID, backend string)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onEject = onEject
	d.onRecover = onRecover
}

// Record records a request outcome for a backend.
func (d *Detector) Record(backendURL string, statusCode int, latency time.Duration) {
	d.mu.RLock()
	s, ok := d.stats[backendURL]
	d.mu.RUnlock()

	if !ok {
		d.mu.Lock()
		s, ok = d.stats[backendURL]
		if !ok {
			s = NewBackendStats(d.cfg.Window)
			d.stats[backendURL] = s
		}
		d.mu.Unlock()
	}

	s.Record(statusCode, latency)
}

// Stop stops the detection loop and waits for it to finish.
func (d *Detector) Stop() {
	close(d.stopCh)
	<-d.done
}

// Snapshot returns a point-in-time view of the detector's state.
func (d *Detector) Snapshot() DetectorSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	snap := DetectorSnapshot{
		RouteID:         d.routeID,
		BackendStats:    make(map[string]StatsSnapshot, len(d.stats)),
		EjectedBackends: make(map[string]EjectionSnapshot, len(d.ejected)),
		TotalEjections:  d.totalEjections.Load(),
		TotalRecoveries: d.totalRecoveries.Load(),
	}

	for url, s := range d.stats {
		snap.BackendStats[url] = s.Snapshot()
	}
	for url, ej := range d.ejected {
		snap.EjectedBackends[url] = EjectionSnapshot{
			EjectedAt: ej.ejectedAt,
			Duration:  ej.duration.String(),
			Count:     ej.count,
			Reason:    ej.reason,
		}
	}

	return snap
}

func (d *Detector) runDetectionLoop() {
	defer close(d.done)

	ticker := time.NewTicker(d.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.evaluate()
		}
	}
}

func (d *Detector) evaluate() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	// Phase 1: Recover backends whose ejection duration has expired
	for url, ej := range d.ejected {
		if now.Sub(ej.ejectedAt) >= ej.duration {
			d.balancer.MarkHealthy(url)
			d.totalRecoveries.Add(1)
			if d.onRecover != nil {
				d.onRecover(d.routeID, url)
			}
			delete(d.ejected, url)
		}
	}

	// Phase 2: Collect stats snapshots for backends with enough samples
	allBackends := d.balancer.GetBackends()
	totalBackends := len(allBackends)

	type entry struct {
		url  string
		snap StatsSnapshot
	}
	var eligible []entry
	for _, b := range allBackends {
		s, ok := d.stats[b.URL]
		if !ok {
			continue
		}
		snap := s.Snapshot()
		if snap.TotalRequests >= int64(d.cfg.MinRequests) {
			eligible = append(eligible, entry{url: b.URL, snap: snap})
		}
	}

	// Skip if fewer than 2 eligible backends — can't determine outliers
	if len(eligible) < 2 {
		return
	}

	// Phase 3: Compute medians
	errorRates := make([]float64, len(eligible))
	p99s := make([]time.Duration, len(eligible))
	for i, e := range eligible {
		errorRates[i] = e.snap.ErrorRate
		p99s[i] = e.snap.P99
	}

	medianErrorRate := medianFloat64(errorRates)
	medianP99 := medianDuration(p99s)

	// Phase 4: Enforce max ejection percent
	maxEjectable := int(float64(totalBackends) * d.cfg.MaxEjectionPercent / 100)
	currentEjected := len(d.ejected)

	// Phase 5: Eject outliers
	for _, e := range eligible {
		if _, alreadyEjected := d.ejected[e.url]; alreadyEjected {
			continue
		}
		if currentEjected >= maxEjectable {
			break
		}

		var reason string

		// Error rate check: above absolute threshold AND above multiplier * median
		if e.snap.ErrorRate > d.cfg.ErrorRateThreshold &&
			e.snap.ErrorRate > d.cfg.ErrorRateMultiplier*medianErrorRate {
			reason = "error_rate"
		}

		// Latency check: p99 above multiplier * median p99
		if reason == "" && medianP99 > 0 &&
			e.snap.P99 > time.Duration(float64(medianP99)*d.cfg.LatencyMultiplier) {
			reason = "latency"
		}

		if reason != "" {
			d.ejectBackend(e.url, reason)
			currentEjected++
		}
	}
}

func (d *Detector) ejectBackend(url, reason string) {
	// Determine ejection count for exponential back-off
	count := 1
	if prev, ok := d.ejected[url]; ok {
		count = prev.count + 1
	}

	duration := time.Duration(count) * d.cfg.BaseEjectionDuration
	if duration > d.cfg.MaxEjectionDuration {
		duration = d.cfg.MaxEjectionDuration
	}

	d.ejected[url] = &ejectionInfo{
		ejectedAt: time.Now(),
		duration:  duration,
		count:     count,
		reason:    reason,
	}

	d.balancer.MarkUnhealthy(url)
	d.totalEjections.Add(1)

	if d.onEject != nil {
		d.onEject(d.routeID, url, reason)
	}
}

func applyDefaults(cfg *config.OutlierDetectionConfig) {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	if cfg.Window <= 0 {
		cfg.Window = 30 * time.Second
	}
	if cfg.MinRequests <= 0 {
		cfg.MinRequests = 10
	}
	if cfg.ErrorRateThreshold <= 0 {
		cfg.ErrorRateThreshold = 0.5
	}
	if cfg.ErrorRateMultiplier <= 0 {
		cfg.ErrorRateMultiplier = 2.0
	}
	if cfg.LatencyMultiplier <= 0 {
		cfg.LatencyMultiplier = 3.0
	}
	if cfg.BaseEjectionDuration <= 0 {
		cfg.BaseEjectionDuration = 30 * time.Second
	}
	if cfg.MaxEjectionDuration <= 0 {
		cfg.MaxEjectionDuration = 5 * time.Minute
	}
	if cfg.MaxEjectionPercent <= 0 {
		cfg.MaxEjectionPercent = 50
	}
}

func medianFloat64(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

func medianDuration(vals []time.Duration) time.Duration {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(vals))
	copy(sorted, vals)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

// Middleware returns a middleware that records per-backend request outcomes for outlier detection.
func (det *Detector) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			varCtx := variables.GetFromRequest(r)
			if varCtx.UpstreamAddr != "" {
				status := varCtx.UpstreamStatus
				if status == 0 {
					status = 502
				}
				det.Record(varCtx.UpstreamAddr, status, varCtx.UpstreamResponseTime)
			}
		})
	}
}
