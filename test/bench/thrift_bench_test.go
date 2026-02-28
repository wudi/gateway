//go:build integration

package bench

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	athrift "github.com/apache/thrift/lib/go/thrift"
	"github.com/wudi/runway/config"

	// Register the thrift translator
	_ "github.com/wudi/runway/internal/proxy/protocol/thrift"
)

// benchThriftServer is a simple Thrift server for benchmarks.
type benchThriftServer struct {
	listener net.Listener
	tracker  *connTracker
	done     chan struct{}
}

func startBenchThriftServer(tb testing.TB) *benchThriftServer {
	tb.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("failed to listen: %v", err)
	}
	tracker := newConnTracker(lis)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			conn, err := tracker.Accept()
			if err != nil {
				return // listener closed
			}
			go handleThriftConn(conn)
		}
	}()

	tb.Cleanup(func() {
		lis.Close()
		<-done
	})

	return &benchThriftServer{
		listener: lis,
		tracker:  tracker,
		done:     done,
	}
}

func (s *benchThriftServer) Address() string {
	return s.listener.Addr().String()
}

// handleThriftConn processes Thrift RPC calls on a connection.
// It reads binary-framed messages and echoes back a User struct.
func handleThriftConn(conn net.Conn) {
	defer conn.Close()

	transport := athrift.NewTSocketFromConnConf(conn, nil)
	framed := athrift.NewTFramedTransportConf(transport, nil)
	iprot := athrift.NewTBinaryProtocolConf(framed, nil)
	oprot := athrift.NewTBinaryProtocolConf(framed, nil)
	ctx := context.Background()

	for {
		name, _, seqID, err := iprot.ReadMessageBegin(ctx)
		if err != nil {
			return // connection closed or error
		}

		// Skip the args struct entirely.
		if err := iprot.Skip(ctx, athrift.STRUCT); err != nil {
			return
		}
		if err := iprot.ReadMessageEnd(ctx); err != nil {
			return
		}

		// Write reply with a hardcoded User struct.
		if err := oprot.WriteMessageBegin(ctx, name, athrift.REPLY, seqID); err != nil {
			return
		}

		// Write the result struct (field 0 = success, which is a User struct).
		oprot.WriteStructBegin(ctx, "result")

		// Field 0: success (User struct)
		oprot.WriteFieldBegin(ctx, "success", athrift.STRUCT, 0)
		writeUserStruct(ctx, oprot)
		oprot.WriteFieldEnd(ctx)

		oprot.WriteFieldStop(ctx)
		oprot.WriteStructEnd(ctx)

		if err := oprot.WriteMessageEnd(ctx); err != nil {
			return
		}
		if err := oprot.Flush(ctx); err != nil {
			return
		}
	}
}

// writeUserStruct writes a hardcoded User struct to the protocol.
func writeUserStruct(ctx context.Context, oprot athrift.TProtocol) {
	oprot.WriteStructBegin(ctx, "User")

	// Field 1: name (string)
	oprot.WriteFieldBegin(ctx, "name", athrift.STRING, 1)
	oprot.WriteString(ctx, "bench-user")
	oprot.WriteFieldEnd(ctx)

	// Field 2: age (i32)
	oprot.WriteFieldBegin(ctx, "age", athrift.I32, 2)
	oprot.WriteI32(ctx, 30)
	oprot.WriteFieldEnd(ctx)

	// Field 8: active (bool)
	oprot.WriteFieldBegin(ctx, "active", athrift.BOOL, 8)
	oprot.WriteBool(ctx, true)
	oprot.WriteFieldEnd(ctx)

	oprot.WriteFieldStop(ctx)
	oprot.WriteStructEnd(ctx)
}

// benchThriftConfig returns a minimal ThriftTranslateConfig for benchmarks.
func benchThriftConfig() config.ThriftTranslateConfig {
	return config.ThriftTranslateConfig{
		Service: "UserService",
		Methods: map[string]config.ThriftMethodDef{
			"getUser": {
				Args: []config.ThriftFieldDef{
					{ID: 1, Name: "id", Type: "string"},
				},
				Result: []config.ThriftFieldDef{
					{ID: 0, Name: "success", Type: "struct", Struct: "User"},
				},
			},
		},
		Structs: map[string][]config.ThriftFieldDef{
			"User": {
				{ID: 1, Name: "name", Type: "string"},
				{ID: 2, Name: "age", Type: "i32"},
				{ID: 8, Name: "active", Type: "bool"},
			},
		},
	}
}

func BenchmarkThriftTranslation(b *testing.B) {
	server := startBenchThriftServer(b)

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:         "thrift-bench",
		Path:       "/thrift/*method",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: "thrift://" + server.Address(),
		}},
		Protocol: config.ProtocolConfig{
			Type:   "http_to_thrift",
			Thrift: benchThriftConfig(),
		},
	}}

	handler, cleanup := newTestRunway(b, cfg)
	defer cleanup()

	// Warmup: establish connection.
	warmReq := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(`{"id":"warmup"}`))
	warmReq.Header.Set("Content-Type", "application/json")
	warmRec := httptest.NewRecorder()
	handler.ServeHTTP(warmRec, warmReq)
	if warmRec.Code != http.StatusOK {
		b.Fatalf("warmup failed: %d %s", warmRec.Code, warmRec.Body.String())
	}

	b.Run("Serial", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			body := fmt.Sprintf(`{"id":"user-%d"}`, i)
			req := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
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
			i := 0
			for pb.Next() {
				body := fmt.Sprintf(`{"id":"user-%d"}`, i)
				req := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					b.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
				}
				i++
			}
		})
	})
}

func BenchmarkThriftTranslation_Contention(b *testing.B) {
	server := startBenchThriftServer(b)

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:         "thrift-contention",
		Path:       "/thrift/*method",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: "thrift://" + server.Address(),
		}},
		Protocol: config.ProtocolConfig{
			Type:   "http_to_thrift",
			Thrift: benchThriftConfig(),
		},
	}}

	handler, cleanup := newTestRunway(b, cfg)
	defer cleanup()

	// Warmup.
	warmReq := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(`{"id":"warmup"}`))
	warmReq.Header.Set("Content-Type", "application/json")
	warmRec := httptest.NewRecorder()
	handler.ServeHTTP(warmRec, warmReq)
	if warmRec.Code != http.StatusOK {
		b.Fatalf("warmup failed: %d %s", warmRec.Code, warmRec.Body.String())
	}

	// Run at increasing parallelism to expose mutex serialization.
	for _, p := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(fmt.Sprintf("Goroutines_%d", p), func(b *testing.B) {
			b.SetParallelism(p)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					body := fmt.Sprintf(`{"id":"user-%d"}`, i)
					req := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					if rec.Code != http.StatusOK {
						b.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
					}
					i++
				}
			})
		})
	}
}

func TestThriftConnectionReuse(t *testing.T) {
	server := startBenchThriftServer(t)

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:         "thrift-connreuse",
		Path:       "/thrift/*method",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: "thrift://" + server.Address(),
		}},
		Protocol: config.ProtocolConfig{
			Type:   "http_to_thrift",
			Thrift: benchThriftConfig(),
		},
	}}

	handler, cleanup := newTestRunway(t, cfg)
	defer cleanup()

	for i := 0; i < 50; i++ {
		body := fmt.Sprintf(`{"id":"user-%d"}`, i)
		req := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d: %s", i, rec.Code, rec.Body.String())
		}
	}

	// Thrift maintains a single pooled connection per backend.
	conns := server.tracker.UniqueConns()
	t.Logf("Thrift translation: 50 requests used %d unique connections", conns)
	if conns > 2 {
		t.Errorf("expected <= 2 unique connections (pool reuse), got %d", conns)
	}
}

func TestThriftMutexSerialization(t *testing.T) {
	server := startBenchThriftServer(t)

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:         "thrift-mutex",
		Path:       "/thrift/*method",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: "thrift://" + server.Address(),
		}},
		Protocol: config.ProtocolConfig{
			Type:   "http_to_thrift",
			Thrift: benchThriftConfig(),
		},
	}}

	handler, cleanup := newTestRunway(t, cfg)
	defer cleanup()

	// Warmup.
	warmReq := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(`{"id":"warmup"}`))
	warmReq.Header.Set("Content-Type", "application/json")
	warmRec := httptest.NewRecorder()
	handler.ServeHTTP(warmRec, warmReq)
	if warmRec.Code != http.StatusOK {
		t.Fatalf("warmup failed: %d %s", warmRec.Code, warmRec.Body.String())
	}

	const n = 20

	doRequests := func(handler http.Handler) time.Duration {
		start := time.Now()
		for i := 0; i < n; i++ {
			body := fmt.Sprintf(`{"id":"user-%d"}`, i)
			req := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}
		return time.Since(start)
	}

	// Sequential timing.
	seqDuration := doRequests(handler)

	// Concurrent timing.
	start := time.Now()
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"id":"user-%d"}`, i)
			req := httptest.NewRequest(http.MethodPost, "/thrift/getUser", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}(g)
	}
	wg.Wait()
	concDuration := time.Since(start)

	speedup := float64(seqDuration) / float64(concDuration)
	t.Logf("Thrift mutex serialization test:")
	t.Logf("  Sequential (%d requests): %v", n, seqDuration)
	t.Logf("  Concurrent (%d goroutines): %v", n, concDuration)
	t.Logf("  Speedup ratio: %.2fx", speedup)
	t.Logf("  (Expected ~1x due to mutex serialization at translator.go:167)")

	// The speedup should be close to 1x because the connection mutex
	// serializes all concurrent requests through a single TCP connection.
	// We don't fail the test — this is observational.
}
