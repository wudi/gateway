//go:build integration

package bench

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

// BenchmarkProxyComparison runs all three proxy modes at the same parallelism
// to produce comparable benchmark output:
//
//	BenchmarkProxyComparison/HTTP-8
//	BenchmarkProxyComparison/gRPC_Translation-8
//	BenchmarkProxyComparison/Thrift_Translation-8
func BenchmarkProxyComparison(b *testing.B) {
	// --- HTTP setup ---
	httpBackend, _ := newHTTPEchoBackend(b)
	httpCfg := baseConfig()
	httpCfg.Routes = []config.RouteConfig{{
		ID:         "compare-http",
		Path:       "/api/",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: httpBackend.URL,
		}},
	}}
	httpHandler, httpCleanup := newTestRunway(b, httpCfg)
	defer httpCleanup()

	// --- gRPC Translation setup ---
	grpcServer := startBenchGRPCServer(b)
	grpcTransCfg := baseConfig()
	grpcTransCfg.Routes = []config.RouteConfig{{
		ID:         "compare-grpc-trans",
		Path:       "/api/*method",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: "grpc://" + grpcServer.Address(),
		}},
		Protocol: config.ProtocolConfig{
			Type: "http_to_grpc",
			GRPC: config.GRPCTranslateConfig{
				Service: "benchtest.EchoService",
				Timeout: 10 * time.Second,
			},
		},
	}}
	grpcTransHandler, grpcTransCleanup := newTestRunway(b, grpcTransCfg)
	defer grpcTransCleanup()

	// Warmup gRPC translation (triggers reflection caching).
	warmReq := httptest.NewRequest(http.MethodPost, "/api/Echo", strings.NewReader(`{"id":0,"message":"warmup"}`))
	warmReq.Header.Set("Content-Type", "application/json")
	warmRec := httptest.NewRecorder()
	grpcTransHandler.ServeHTTP(warmRec, warmReq)
	if warmRec.Code != http.StatusOK {
		b.Fatalf("gRPC translation warmup failed: %d %s", warmRec.Code, warmRec.Body.String())
	}

	// --- Thrift setup ---
	thriftServer := startBenchThriftServer(b)
	thriftCfg := baseConfig()
	thriftCfg.Routes = []config.RouteConfig{{
		ID:         "compare-thrift",
		Path:       "/thrift/*method",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: "thrift://" + thriftServer.Address(),
		}},
		Protocol: config.ProtocolConfig{
			Type:   "http_to_thrift",
			Thrift: benchThriftConfig(),
		},
	}}
	thriftHandler, thriftCleanup := newTestRunway(b, thriftCfg)
	defer thriftCleanup()

	// Warmup thrift.
	thriftWarm := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(`{"id":"warmup"}`))
	thriftWarm.Header.Set("Content-Type", "application/json")
	thriftWarmRec := httptest.NewRecorder()
	thriftHandler.ServeHTTP(thriftWarmRec, thriftWarm)
	if thriftWarmRec.Code != http.StatusOK {
		b.Fatalf("Thrift warmup failed: %d %s", thriftWarmRec.Code, thriftWarmRec.Body.String())
	}

	// --- Benchmarks ---

	b.Run("HTTP", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
				rec := httptest.NewRecorder()
				httpHandler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					b.Fatalf("HTTP: expected 200, got %d", rec.Code)
				}
			}
		})
	})

	b.Run("gRPC_Translation", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				body := fmt.Sprintf(`{"id":%d,"message":"hello"}`, i)
				req := httptest.NewRequest(http.MethodPost, "/api/Echo", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				grpcTransHandler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					b.Fatalf("gRPC Translation: expected 200, got %d: %s", rec.Code, rec.Body.String())
				}
				i++
			}
		})
	})

	b.Run("Thrift_Translation", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				body := fmt.Sprintf(`{"id":"user-%d"}`, i)
				req := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				thriftHandler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					b.Fatalf("Thrift: expected 200, got %d: %s", rec.Code, rec.Body.String())
				}
				i++
			}
		})
	})
}
