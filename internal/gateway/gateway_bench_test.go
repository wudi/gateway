package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func BenchmarkServeHTTP(b *testing.B) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "bench-route",
				Path:     "/api",
				Backends: []config.BackendConfig{{URL: backend.URL}},
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		b.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()
	req := httptest.NewRequest("GET", "/api/users", nil)
	req.Header.Set("Accept", "application/json")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}
}

func BenchmarkServeHTTP_Miss(b *testing.B) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "bench-route",
				Path:     "/api",
				Backends: []config.BackendConfig{{URL: backend.URL}},
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		b.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()
	req := httptest.NewRequest("GET", "/not-found", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}
}
