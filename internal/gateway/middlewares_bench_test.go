package gateway

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/cache"
	"github.com/wudi/gateway/internal/circuitbreaker"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/metrics"
	"github.com/wudi/gateway/internal/router"
	"github.com/wudi/gateway/variables"
)

func BenchmarkVarContextMW(b *testing.B) {
	mw := varContextMW("bench-route")
	handler := mw(ok200())

	baseReq := httptest.NewRequest("GET", "/test", nil)
	varCtx := variables.NewContext(baseReq)
	varCtx.PathParams = map[string]string{"id": "42"}
	ctx := context.WithValue(baseReq.Context(), variables.RequestContextKey{}, varCtx)
	req := baseReq.WithContext(ctx)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}
}

func BenchmarkRequestTransformMW(b *testing.B) {
	route := &router.Route{
		ID:   "bench-route",
		Path: "/api",
		Transform: config.TransformConfig{
			Request: config.RequestTransform{
				Headers: config.HeaderTransform{
					Add: map[string]string{
						"X-Gateway":    "bench",
						"X-Request-Id": "req-001",
					},
					Set: map[string]string{
						"X-Forwarded-Proto": "https",
						"X-Real-IP":         "10.0.0.1",
					},
					Remove: []string{"X-Secret"},
				},
			},
		},
	}

	mw := requestTransformMW(route, nil, nil)
	handler := mw(ok200())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Secret", "should-be-removed")
		varCtx := variables.NewContext(req)
		ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, varCtx)
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}
}

func BenchmarkCacheMW_Miss(b *testing.B) {
	h := cache.NewHandler(config.CacheConfig{
		Enabled:     true,
		TTL:         5 * time.Second,
		MaxSize:     100,
		MaxBodySize: 1 << 20,
		Methods:     []string{"GET"},
	}, cache.NewMemoryStore(100, 5*time.Second))
	mc := metrics.NewCollector()

	mw := cacheMW(h, mc, "bench-route")
	handler := mw(ok200())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// POST is uncacheable — exercises the miss/skip path without populating cache
		req := httptest.NewRequest("POST", "/data", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}
}

func BenchmarkCircuitBreakerMW(b *testing.B) {
	cb := circuitbreaker.NewBreaker(config.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 1000,
		MaxRequests:      1,
		Timeout:          5 * time.Second,
	}, nil)

	mw := circuitBreakerMW(cb, false)
	handler := mw(ok200())

	req := httptest.NewRequest("GET", "/test", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}
}
