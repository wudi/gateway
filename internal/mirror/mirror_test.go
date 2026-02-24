package mirror

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func newMirror(cfg config.MirrorConfig) *Mirror {
	m, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return m
}

func TestMirrorEnabled(t *testing.T) {
	m := newMirror(config.MirrorConfig{
		Enabled:    true,
		Backends:   []config.BackendConfig{{URL: "http://mirror:8080"}},
		Percentage: 100,
	})

	if !m.IsEnabled() {
		t.Error("mirror should be enabled")
	}
}

func TestMirrorDisabled(t *testing.T) {
	m := newMirror(config.MirrorConfig{
		Enabled: false,
	})

	if m.IsEnabled() {
		t.Error("mirror should be disabled")
	}

	r := httptest.NewRequest("GET", "/test", nil)
	if m.ShouldMirror(r) {
		t.Error("disabled mirror should not mirror")
	}
}

func TestMirrorPercentage(t *testing.T) {
	m := newMirror(config.MirrorConfig{
		Enabled:    true,
		Backends:   []config.BackendConfig{{URL: "http://mirror:8080"}},
		Percentage: 50,
	})

	mirrored := 0
	iterations := 10000

	for i := 0; i < iterations; i++ {
		r := httptest.NewRequest("GET", "/test", nil)
		if m.ShouldMirror(r) {
			mirrored++
		}
	}

	ratio := float64(mirrored) / float64(iterations)
	if ratio < 0.40 || ratio > 0.60 {
		t.Errorf("mirror ratio %.2f out of expected range [0.40, 0.60]", ratio)
	}
}

func TestBufferRequestBody(t *testing.T) {
	body := []byte(`{"key":"value"}`)
	r := httptest.NewRequest("POST", "/", bytes.NewReader(body))

	buf, err := BufferRequestBody(r)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(buf, body) {
		t.Error("buffered body doesn't match")
	}

	// Read from restored body
	restoredBody := make([]byte, len(body))
	n, _ := r.Body.Read(restoredBody)
	if !bytes.Equal(restoredBody[:n], body) {
		t.Error("restored body doesn't match")
	}
}

func TestMirrorSendAsync(t *testing.T) {
	var received atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := newMirror(config.MirrorConfig{
		Enabled:    true,
		Backends:   []config.BackendConfig{{URL: server.URL}},
		Percentage: 100,
	})

	r := httptest.NewRequest("GET", "/test", nil)
	m.SendAsync(r, nil, nil)

	// Wait for async request
	time.Sleep(500 * time.Millisecond)

	if received.Load() != 1 {
		t.Errorf("expected 1 mirrored request, got %d", received.Load())
	}
}

func TestMirrorByRoute(t *testing.T) {
	m := NewMirrorByRoute()
	err := m.AddRoute("route1", config.MirrorConfig{
		Enabled:    true,
		Backends:   []config.BackendConfig{{URL: "http://mirror:8080"}},
		Percentage: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	mirror := m.GetMirror("route1")
	if mirror == nil || !mirror.IsEnabled() {
		t.Fatal("expected mirror for route1")
	}

	if m.GetMirror("unknown") != nil {
		t.Error("expected nil for unknown route")
	}
}

func TestMirrorConditionsMethod(t *testing.T) {
	m := newMirror(config.MirrorConfig{
		Enabled:    true,
		Backends:   []config.BackendConfig{{URL: "http://mirror:8080"}},
		Percentage: 100,
		Conditions: config.MirrorConditionsConfig{
			Methods: []string{"POST", "PUT"},
		},
	})

	get := httptest.NewRequest("GET", "/test", nil)
	if m.ShouldMirror(get) {
		t.Error("GET should not be mirrored when conditions restrict to POST/PUT")
	}

	post := httptest.NewRequest("POST", "/test", nil)
	if !m.ShouldMirror(post) {
		t.Error("POST should be mirrored")
	}
}

func TestMirrorConditionsPathRegex(t *testing.T) {
	m := newMirror(config.MirrorConfig{
		Enabled:    true,
		Backends:   []config.BackendConfig{{URL: "http://mirror:8080"}},
		Percentage: 100,
		Conditions: config.MirrorConditionsConfig{
			PathRegex: "^/api/v2/",
		},
	})

	v1 := httptest.NewRequest("GET", "/api/v1/users", nil)
	if m.ShouldMirror(v1) {
		t.Error("v1 path should not be mirrored")
	}

	v2 := httptest.NewRequest("GET", "/api/v2/users", nil)
	if !m.ShouldMirror(v2) {
		t.Error("v2 path should be mirrored")
	}
}

func TestMirrorMetricsIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	m := newMirror(config.MirrorConfig{
		Enabled:    true,
		Backends:   []config.BackendConfig{{URL: server.URL}},
		Percentage: 100,
	})

	r := httptest.NewRequest("GET", "/test", nil)
	m.SendAsync(r, nil, nil)
	time.Sleep(500 * time.Millisecond)

	snap := m.metrics.Snapshot()
	if snap.TotalMirrored != 1 {
		t.Errorf("expected TotalMirrored=1, got %d", snap.TotalMirrored)
	}
}

func TestMirrorStats(t *testing.T) {
	mbr := NewMirrorByRoute()
	mbr.AddRoute("r1", config.MirrorConfig{
		Enabled:    true,
		Backends:   []config.BackendConfig{{URL: "http://mirror:8080"}},
		Percentage: 100,
	})

	stats := mbr.Stats()
	if _, ok := stats["r1"]; !ok {
		t.Error("expected stats for r1")
	}
}
