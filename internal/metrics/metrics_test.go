package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCollectorRecordRequest(t *testing.T) {
	c := NewCollector()

	c.RecordRequest("route1", "GET", 200, 100*time.Millisecond)
	c.RecordRequest("route1", "GET", 200, 200*time.Millisecond)
	c.RecordRequest("route1", "POST", 500, 50*time.Millisecond)

	snap := c.Snapshot()

	if snap.RequestsTotal["route1|GET|200"] != 2 {
		t.Errorf("expected 2 GET 200 requests, got %d", snap.RequestsTotal["route1|GET|200"])
	}

	if snap.RequestsTotal["route1|POST|500"] != 1 {
		t.Errorf("expected 1 POST 500 request, got %d", snap.RequestsTotal["route1|POST|500"])
	}

	hd := snap.RequestDurations["route1"]
	if hd == nil {
		t.Fatal("expected histogram data for route1")
	}
	if hd.Count != 3 {
		t.Errorf("expected 3 duration entries, got %d", hd.Count)
	}
}

func TestCollectorCacheMetrics(t *testing.T) {
	c := NewCollector()

	c.RecordCacheHit("route1")
	c.RecordCacheHit("route1")
	c.RecordCacheMiss("route1")

	snap := c.Snapshot()

	if snap.CacheHits["route1"] != 2 {
		t.Errorf("expected 2 cache hits, got %d", snap.CacheHits["route1"])
	}
	if snap.CacheMisses["route1"] != 1 {
		t.Errorf("expected 1 cache miss, got %d", snap.CacheMisses["route1"])
	}
}

func TestCollectorCircuitBreakerState(t *testing.T) {
	c := NewCollector()

	c.SetCircuitBreakerState("route1", 1)
	snap := c.Snapshot()

	if snap.CircuitBreakerState["route1"] != 1 {
		t.Errorf("expected state 1, got %d", snap.CircuitBreakerState["route1"])
	}
}

func TestCollectorBackendHealth(t *testing.T) {
	c := NewCollector()

	c.SetBackendHealth("route1", "http://backend1", true)
	c.SetBackendHealth("route1", "http://backend2", false)

	snap := c.Snapshot()

	if snap.BackendHealth["route1|http://backend1"] != 1 {
		t.Error("expected backend1 healthy")
	}
	if snap.BackendHealth["route1|http://backend2"] != 0 {
		t.Error("expected backend2 unhealthy")
	}
}

func TestWritePrometheus(t *testing.T) {
	c := NewCollector()

	c.RecordRequest("api", "GET", 200, 50*time.Millisecond)
	c.RecordCacheHit("api")
	c.SetCircuitBreakerState("api", 0)

	w := httptest.NewRecorder()
	c.WritePrometheus(w)

	body := w.Body.String()

	if !strings.Contains(body, "gateway_requests_total") {
		t.Error("missing gateway_requests_total")
	}
	if !strings.Contains(body, "gateway_cache_hits_total") {
		t.Error("missing gateway_cache_hits_total")
	}
	if !strings.Contains(body, "gateway_circuit_breaker_state") {
		t.Error("missing gateway_circuit_breaker_state")
	}

	if w.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("unexpected content type: %s", w.Header().Get("Content-Type"))
	}
}
