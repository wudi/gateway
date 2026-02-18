package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/router"
	"github.com/wudi/gateway/internal/variables"
)

func BenchmarkCreateProxyRequest(b *testing.B) {
	p := New(Config{})

	target, _ := url.Parse("http://backend.local:8080")
	route := &router.Route{
		ID:          "bench-route",
		Path:        "/api/v1",
		StripPrefix: true,
	}

	baseReq := httptest.NewRequest("GET", "/api/v1/users/123", nil)
	baseReq.Header.Set("Accept", "application/json")
	baseReq.Header.Set("Content-Type", "application/json")
	baseReq.Header.Set("Authorization", "Bearer token123")
	baseReq.Header.Set("X-Request-ID", "req-001")
	baseReq.Header.Set("X-Forwarded-For", "10.0.0.1")
	baseReq.Header.Set("User-Agent", "bench/1.0")
	baseReq.Header.Set("Accept-Encoding", "gzip, deflate")
	baseReq.Header.Set("Cache-Control", "no-cache")
	baseReq.Header.Set("X-Custom-1", "value1")
	baseReq.Header.Set("X-Custom-2", "value2")

	varCtx := variables.NewContext(baseReq)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.createProxyRequest(baseReq.Context(), baseReq, target, route, varCtx, nil)
	}
}

func BenchmarkProxyRoundTrip(b *testing.B) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backend.Close()

	p := New(Config{})
	route := &router.Route{
		ID:   "bench-route",
		Path: "/api",
	}
	backends := []*loadbalancer.Backend{
		{URL: backend.URL, Weight: 1, Healthy: true},
	}
	balancer := loadbalancer.NewRoundRobin(backends)
	handler := p.Handler(route, balancer)

	req := httptest.NewRequest("GET", "/api/users", nil)
	req.Header.Set("Accept", "application/json")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}
}
