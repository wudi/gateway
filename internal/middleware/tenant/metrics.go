package tenant

import (
	"net/http"
	"sync/atomic"
	"time"
)

// TenantMetrics tracks per-tenant usage analytics.
type TenantMetrics struct {
	RequestCount   atomic.Int64
	TotalLatencyNs atomic.Int64
	BytesIn        atomic.Int64
	BytesOut       atomic.Int64
	StatusCodes    [6]atomic.Int64  // index 0=1xx, 1=2xx, 2=3xx, 3=4xx, 4=5xx, 5=other
	LatencyBuckets [12]atomic.Int64 // 1ms,5ms,10ms,25ms,50ms,100ms,250ms,500ms,1s,5s,10s,inf
}

// Record records a completed request's metrics.
func (m *TenantMetrics) Record(status int, latency time.Duration, bytesIn, bytesOut int64) {
	m.RequestCount.Add(1)
	m.TotalLatencyNs.Add(int64(latency))
	m.BytesIn.Add(bytesIn)
	m.BytesOut.Add(bytesOut)

	// Status code bucket
	idx := status/100 - 1
	if idx < 0 || idx > 4 {
		idx = 5
	}
	m.StatusCodes[idx].Add(1)

	// Latency bucket
	ms := latency.Milliseconds()
	switch {
	case ms <= 1:
		m.LatencyBuckets[0].Add(1)
	case ms <= 5:
		m.LatencyBuckets[1].Add(1)
	case ms <= 10:
		m.LatencyBuckets[2].Add(1)
	case ms <= 25:
		m.LatencyBuckets[3].Add(1)
	case ms <= 50:
		m.LatencyBuckets[4].Add(1)
	case ms <= 100:
		m.LatencyBuckets[5].Add(1)
	case ms <= 250:
		m.LatencyBuckets[6].Add(1)
	case ms <= 500:
		m.LatencyBuckets[7].Add(1)
	case ms <= 1000:
		m.LatencyBuckets[8].Add(1)
	case ms <= 5000:
		m.LatencyBuckets[9].Add(1)
	case ms <= 10000:
		m.LatencyBuckets[10].Add(1)
	default:
		m.LatencyBuckets[11].Add(1)
	}
}

// Snapshot returns a JSON-friendly snapshot of the metrics.
func (m *TenantMetrics) Snapshot() map[string]interface{} {
	count := m.RequestCount.Load()
	totalNs := m.TotalLatencyNs.Load()
	var avgMs float64
	if count > 0 {
		avgMs = float64(totalNs) / float64(count) / 1e6
	}
	return map[string]interface{}{
		"request_count":  count,
		"avg_latency_ms": avgMs,
		"bytes_in":       m.BytesIn.Load(),
		"bytes_out":      m.BytesOut.Load(),
		"status_1xx":     m.StatusCodes[0].Load(),
		"status_2xx":     m.StatusCodes[1].Load(),
		"status_3xx":     m.StatusCodes[2].Load(),
		"status_4xx":     m.StatusCodes[3].Load(),
		"status_5xx":     m.StatusCodes[4].Load(),
	}
}

// tenantResponseWriter wraps http.ResponseWriter to capture status and bytes written
// for tenant usage analytics.
type tenantResponseWriter struct {
	http.ResponseWriter
	status   int
	bytes    int64
	wroteHdr bool
}

func (w *tenantResponseWriter) WriteHeader(code int) {
	if !w.wroteHdr {
		w.wroteHdr = true
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *tenantResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHdr {
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *tenantResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
