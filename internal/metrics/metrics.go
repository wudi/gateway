package metrics

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Collector tracks gateway metrics for Prometheus-compatible export
type Collector struct {
	mu sync.RWMutex

	// Request metrics
	requestsTotal    map[string]int64 // key: route|method|status
	requestDurations map[string]*HistogramData // key: route

	// Feature metrics
	cacheHits   map[string]int64 // key: route
	cacheMisses map[string]int64 // key: route
	retryTotal  map[string]int64 // key: route

	// Circuit breaker state: 0=closed, 1=open, 2=half_open
	circuitBreakerState map[string]int // key: route

	// Backend health: 0=unhealthy, 1=healthy
	backendHealth map[string]int // key: route|backend
}

// HistogramData stores histogram-like data for durations
type HistogramData struct {
	Count   int64
	Sum     float64
	Buckets map[float64]int64 // upper bound -> count
}

// DefaultBuckets are default histogram buckets in seconds
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}

// NewCollector creates a new metrics collector
func NewCollector() *Collector {
	return &Collector{
		requestsTotal:      make(map[string]int64),
		requestDurations:   make(map[string]*HistogramData),
		cacheHits:          make(map[string]int64),
		cacheMisses:        make(map[string]int64),
		retryTotal:         make(map[string]int64),
		circuitBreakerState: make(map[string]int),
		backendHealth:      make(map[string]int),
	}
}

// RecordRequest records a completed request
func (c *Collector) RecordRequest(route, method string, statusCode int, duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := route + "|" + method + "|" + strconv.Itoa(statusCode)
	c.requestsTotal[key]++

	hd, ok := c.requestDurations[route]
	if !ok {
		hd = &HistogramData{
			Buckets: make(map[float64]int64),
		}
		for _, b := range DefaultBuckets {
			hd.Buckets[b] = 0
		}
		c.requestDurations[route] = hd
	}

	secs := duration.Seconds()
	hd.Count++
	hd.Sum += secs
	for _, bound := range DefaultBuckets {
		if secs <= bound {
			hd.Buckets[bound]++
		}
	}
}

// RecordCacheHit records a cache hit
func (c *Collector) RecordCacheHit(route string) {
	c.mu.Lock()
	c.cacheHits[route]++
	c.mu.Unlock()
}

// RecordCacheMiss records a cache miss
func (c *Collector) RecordCacheMiss(route string) {
	c.mu.Lock()
	c.cacheMisses[route]++
	c.mu.Unlock()
}

// RecordRetry records a retry attempt
func (c *Collector) RecordRetry(route string) {
	c.mu.Lock()
	c.retryTotal[route]++
	c.mu.Unlock()
}

// SetCircuitBreakerState sets the circuit breaker state for a route
func (c *Collector) SetCircuitBreakerState(route string, state int) {
	c.mu.Lock()
	c.circuitBreakerState[route] = state
	c.mu.Unlock()
}

// SetBackendHealth sets the health status of a backend
func (c *Collector) SetBackendHealth(route, backend string, healthy bool) {
	c.mu.Lock()
	key := route + "|" + backend
	if healthy {
		c.backendHealth[key] = 1
	} else {
		c.backendHealth[key] = 0
	}
	c.mu.Unlock()
}

// MetricsSnapshot holds a snapshot of all metrics
type MetricsSnapshot struct {
	RequestsTotal       map[string]int64              `json:"requests_total"`
	RequestDurations    map[string]*HistogramSnapshot  `json:"request_durations"`
	CacheHits           map[string]int64              `json:"cache_hits"`
	CacheMisses         map[string]int64              `json:"cache_misses"`
	RetryTotal          map[string]int64              `json:"retry_total"`
	CircuitBreakerState map[string]int                `json:"circuit_breaker_state"`
	BackendHealth       map[string]int                `json:"backend_health"`
}

// HistogramSnapshot is a snapshot of histogram data
type HistogramSnapshot struct {
	Count   int64              `json:"count"`
	Sum     float64            `json:"sum"`
	Buckets map[float64]int64  `json:"buckets"`
}

// Snapshot returns a point-in-time snapshot of all metrics
func (c *Collector) Snapshot() *MetricsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snap := &MetricsSnapshot{
		RequestsTotal:       make(map[string]int64),
		RequestDurations:    make(map[string]*HistogramSnapshot),
		CacheHits:           make(map[string]int64),
		CacheMisses:         make(map[string]int64),
		RetryTotal:          make(map[string]int64),
		CircuitBreakerState: make(map[string]int),
		BackendHealth:       make(map[string]int),
	}

	for k, v := range c.requestsTotal {
		snap.RequestsTotal[k] = v
	}

	for k, v := range c.requestDurations {
		hs := &HistogramSnapshot{
			Count:   v.Count,
			Sum:     v.Sum,
			Buckets: make(map[float64]int64),
		}
		for b, cnt := range v.Buckets {
			hs.Buckets[b] = cnt
		}
		snap.RequestDurations[k] = hs
	}

	for k, v := range c.cacheHits {
		snap.CacheHits[k] = v
	}
	for k, v := range c.cacheMisses {
		snap.CacheMisses[k] = v
	}
	for k, v := range c.retryTotal {
		snap.RetryTotal[k] = v
	}
	for k, v := range c.circuitBreakerState {
		snap.CircuitBreakerState[k] = v
	}
	for k, v := range c.backendHealth {
		snap.BackendHealth[k] = v
	}

	return snap
}

// WritePrometheus writes metrics in Prometheus text exposition format
func (c *Collector) WritePrometheus(w http.ResponseWriter) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// gateway_requests_total
	writeHelp(w, "gateway_requests_total", "Total number of requests", "counter")
	for key, count := range c.requestsTotal {
		parts := splitKey(key, 3)
		if len(parts) == 3 {
			writeMetric(w, "gateway_requests_total", count,
				"route", parts[0], "method", parts[1], "status", parts[2])
		}
	}

	// gateway_request_duration_seconds
	writeHelp(w, "gateway_request_duration_seconds", "Request duration in seconds", "histogram")
	for route, hd := range c.requestDurations {
		for _, bound := range DefaultBuckets {
			cnt := hd.Buckets[bound]
			writeMetricFloat(w, "gateway_request_duration_seconds_bucket", float64(cnt),
				"route", route, "le", strconv.FormatFloat(bound, 'f', -1, 64))
		}
		writeMetricFloat(w, "gateway_request_duration_seconds_bucket", float64(hd.Count),
			"route", route, "le", "+Inf")
		writeMetricFloat(w, "gateway_request_duration_seconds_sum", hd.Sum,
			"route", route)
		writeMetric(w, "gateway_request_duration_seconds_count", hd.Count,
			"route", route)
	}

	// gateway_cache_hits_total
	writeHelp(w, "gateway_cache_hits_total", "Total cache hits", "counter")
	for route, count := range c.cacheHits {
		writeMetric(w, "gateway_cache_hits_total", count, "route", route)
	}

	// gateway_cache_misses_total
	writeHelp(w, "gateway_cache_misses_total", "Total cache misses", "counter")
	for route, count := range c.cacheMisses {
		writeMetric(w, "gateway_cache_misses_total", count, "route", route)
	}

	// gateway_retry_total
	writeHelp(w, "gateway_retry_total", "Total retry attempts", "counter")
	for route, count := range c.retryTotal {
		writeMetric(w, "gateway_retry_total", count, "route", route)
	}

	// gateway_circuit_breaker_state
	writeHelp(w, "gateway_circuit_breaker_state", "Circuit breaker state (0=closed, 1=open, 2=half_open)", "gauge")
	for route, state := range c.circuitBreakerState {
		writeMetric(w, "gateway_circuit_breaker_state", int64(state), "route", route)
	}

	// gateway_backend_health
	writeHelp(w, "gateway_backend_health", "Backend health (0=unhealthy, 1=healthy)", "gauge")
	for key, health := range c.backendHealth {
		parts := splitKey(key, 2)
		if len(parts) == 2 {
			writeMetric(w, "gateway_backend_health", int64(health),
				"route", parts[0], "backend", parts[1])
		}
	}
}

func writeHelp(w http.ResponseWriter, name, help, metricType string) {
	w.Write([]byte("# HELP " + name + " " + help + "\n"))
	w.Write([]byte("# TYPE " + name + " " + metricType + "\n"))
}

func writeMetric(w http.ResponseWriter, name string, value int64, labels ...string) {
	line := name + formatLabels(labels) + " " + strconv.FormatInt(value, 10) + "\n"
	w.Write([]byte(line))
}

func writeMetricFloat(w http.ResponseWriter, name string, value float64, labels ...string) {
	line := name + formatLabels(labels) + " " + strconv.FormatFloat(value, 'f', -1, 64) + "\n"
	w.Write([]byte(line))
}

func formatLabels(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	result := "{"
	for i := 0; i < len(labels)-1; i += 2 {
		if i > 0 {
			result += ","
		}
		result += labels[i] + "=\"" + labels[i+1] + "\""
	}
	return result + "}"
}

func splitKey(key string, n int) []string {
	parts := make([]string, 0, n)
	start := 0
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			parts = append(parts, key[start:i])
			start = i + 1
			if len(parts) == n-1 {
				parts = append(parts, key[start:])
				return parts
			}
		}
	}
	if start < len(key) {
		parts = append(parts, key[start:])
	}
	return parts
}
