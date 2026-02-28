//go:build integration

package bench

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/wudi/runway/config"
)

func BenchmarkHTTPProxy(b *testing.B) {
	backend, _ := newHTTPEchoBackend(b)

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:         "http-bench",
		Path:       "/api/",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: backend.URL,
		}},
	}}

	handler, cleanup := newTestRunway(b, cfg)
	defer cleanup()

	b.Run("Serial", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
		}
	})

	b.Run("Parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					b.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
				}
			}
		})
	})
}

func TestHTTPConnectionReuse(t *testing.T) {
	backend, tracker := newHTTPEchoBackend(t)

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:         "http-connreuse",
		Path:       "/api/",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: backend.URL,
		}},
	}}

	handler, cleanup := newTestRunway(t, cfg)
	defer cleanup()

	// Send 100 serial requests.
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rec.Code)
		}
	}

	conns := tracker.UniqueConns()
	t.Logf("HTTP serial: 100 requests used %d unique connections", conns)
	if conns > 5 {
		t.Errorf("expected <= 5 unique connections, got %d (connection reuse failing)", conns)
	}
}

func TestHTTPConnectionReuse_Parallel(t *testing.T) {
	backend, tracker := newHTTPEchoBackend(t)

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:         "http-connreuse-par",
		Path:       "/api/",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: backend.URL,
		}},
	}}

	handler, cleanup := newTestRunway(t, cfg)
	defer cleanup()

	// 10 goroutines x 10 requests each.
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("expected 200, got %d", rec.Code)
				}
			}
		}()
	}
	wg.Wait()

	conns := tracker.UniqueConns()
	t.Logf("HTTP parallel: 100 requests (10x10) used %d unique connections", conns)
	if conns > 15 {
		t.Errorf("expected <= 15 unique connections, got %d (connection reuse failing)", conns)
	}
}
