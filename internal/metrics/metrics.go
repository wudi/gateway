package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// DefaultBuckets are default histogram buckets in seconds
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}

// Collector tracks gateway metrics using prometheus/client_golang
type Collector struct {
	registry *prometheus.Registry

	requestsTotal    *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	cacheHitsTotal   *prometheus.CounterVec
	cacheMissesTotal *prometheus.CounterVec
	retryTotal       *prometheus.CounterVec
	cbState          *prometheus.GaugeVec
	backendHealth    *prometheus.GaugeVec
	activeRequests   *prometheus.GaugeVec
	rateLimitRejects *prometheus.CounterVec
}

// NewCollector creates a new metrics collector backed by prometheus/client_golang
func NewCollector() *Collector {
	reg := prometheus.NewRegistry()

	c := &Collector{
		registry: reg,
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of requests",
		}, []string{"route", "method", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "Request duration in seconds",
			Buckets: DefaultBuckets,
		}, []string{"route"}),
		cacheHitsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_cache_hits_total",
			Help: "Total cache hits",
		}, []string{"route"}),
		cacheMissesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_cache_misses_total",
			Help: "Total cache misses",
		}, []string{"route"}),
		retryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_retry_total",
			Help: "Total retry attempts",
		}, []string{"route"}),
		cbState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_circuit_breaker_state",
			Help: "Circuit breaker state (0=closed, 1=open, 2=half_open)",
		}, []string{"route"}),
		backendHealth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_backend_health",
			Help: "Backend health (0=unhealthy, 1=healthy)",
		}, []string{"route", "backend"}),
		activeRequests: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gateway_active_requests",
			Help: "Number of currently active requests",
		}, []string{"route"}),
		rateLimitRejects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_rate_limit_rejects_total",
			Help: "Total rate limit rejections",
		}, []string{"route"}),
	}

	reg.MustRegister(
		c.requestsTotal,
		c.requestDuration,
		c.cacheHitsTotal,
		c.cacheMissesTotal,
		c.retryTotal,
		c.cbState,
		c.backendHealth,
		c.activeRequests,
		c.rateLimitRejects,
	)

	return c
}

// RecordRequest records a completed request
func (c *Collector) RecordRequest(route, method string, statusCode int, duration time.Duration) {
	c.requestsTotal.WithLabelValues(route, method, strconv.Itoa(statusCode)).Inc()
	c.requestDuration.WithLabelValues(route).Observe(duration.Seconds())
}

// RecordCacheHit records a cache hit
func (c *Collector) RecordCacheHit(route string) {
	c.cacheHitsTotal.WithLabelValues(route).Inc()
}

// RecordCacheMiss records a cache miss
func (c *Collector) RecordCacheMiss(route string) {
	c.cacheMissesTotal.WithLabelValues(route).Inc()
}

// RecordRetry records a retry attempt
func (c *Collector) RecordRetry(route string) {
	c.retryTotal.WithLabelValues(route).Inc()
}

// SetCircuitBreakerState sets the circuit breaker state for a route
func (c *Collector) SetCircuitBreakerState(route string, state int) {
	c.cbState.WithLabelValues(route).Set(float64(state))
}

// SetBackendHealth sets the health status of a backend
func (c *Collector) SetBackendHealth(route, backend string, healthy bool) {
	v := 0.0
	if healthy {
		v = 1.0
	}
	c.backendHealth.WithLabelValues(route, backend).Set(v)
}

// RecordActiveRequest increments/decrements the active request gauge
func (c *Collector) RecordActiveRequest(route string, delta float64) {
	c.activeRequests.WithLabelValues(route).Add(delta)
}

// RecordRateLimitReject records a rate limit rejection
func (c *Collector) RecordRateLimitReject(route string) {
	c.rateLimitRejects.WithLabelValues(route).Inc()
}

// Handler returns an http.Handler that serves the Prometheus metrics
func (c *Collector) Handler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{})
}

// WritePrometheus writes metrics in Prometheus text exposition format.
// Retained for backward compatibility with the admin API.
func (c *Collector) WritePrometheus(w http.ResponseWriter) {
	c.Handler().ServeHTTP(w, &http.Request{})
}

// MetricsSnapshot holds a snapshot of all metrics (for JSON admin endpoints)
type MetricsSnapshot struct {
	RequestsTotal       map[string]int64             `json:"requests_total"`
	RequestDurations    map[string]*HistogramSnapshot `json:"request_durations"`
	CacheHits           map[string]int64             `json:"cache_hits"`
	CacheMisses         map[string]int64             `json:"cache_misses"`
	RetryTotal          map[string]int64             `json:"retry_total"`
	CircuitBreakerState map[string]int               `json:"circuit_breaker_state"`
	BackendHealth       map[string]int               `json:"backend_health"`
}

// HistogramSnapshot is a snapshot of histogram data
type HistogramSnapshot struct {
	Count   int64             `json:"count"`
	Sum     float64           `json:"sum"`
	Buckets map[float64]int64 `json:"buckets"`
}

// Snapshot returns a point-in-time snapshot of all metrics by gathering from the registry.
func (c *Collector) Snapshot() *MetricsSnapshot {
	snap := &MetricsSnapshot{
		RequestsTotal:       make(map[string]int64),
		RequestDurations:    make(map[string]*HistogramSnapshot),
		CacheHits:           make(map[string]int64),
		CacheMisses:         make(map[string]int64),
		RetryTotal:          make(map[string]int64),
		CircuitBreakerState: make(map[string]int),
		BackendHealth:       make(map[string]int),
	}

	families, _ := c.registry.Gather()
	for _, fam := range families {
		for _, m := range fam.GetMetric() {
			labels := make(map[string]string)
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}

			switch fam.GetName() {
			case "gateway_requests_total":
				key := labels["route"] + "|" + labels["method"] + "|" + labels["status"]
				snap.RequestsTotal[key] = int64(m.GetCounter().GetValue())
			case "gateway_request_duration_seconds":
				h := m.GetHistogram()
				hs := &HistogramSnapshot{
					Count:   int64(h.GetSampleCount()),
					Sum:     h.GetSampleSum(),
					Buckets: make(map[float64]int64),
				}
				for _, b := range h.GetBucket() {
					hs.Buckets[b.GetUpperBound()] = int64(b.GetCumulativeCount())
				}
				snap.RequestDurations[labels["route"]] = hs
			case "gateway_cache_hits_total":
				snap.CacheHits[labels["route"]] = int64(m.GetCounter().GetValue())
			case "gateway_cache_misses_total":
				snap.CacheMisses[labels["route"]] = int64(m.GetCounter().GetValue())
			case "gateway_retry_total":
				snap.RetryTotal[labels["route"]] = int64(m.GetCounter().GetValue())
			case "gateway_circuit_breaker_state":
				snap.CircuitBreakerState[labels["route"]] = int(m.GetGauge().GetValue())
			case "gateway_backend_health":
				key := labels["route"] + "|" + labels["backend"]
				snap.BackendHealth[key] = int(m.GetGauge().GetValue())
			}
		}
	}

	return snap
}
