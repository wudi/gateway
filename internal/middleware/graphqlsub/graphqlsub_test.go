package graphqlsub

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func TestNonWebSocketPassesThrough(t *testing.T) {
	h := New(config.GraphQLSubscriptionConfig{Enabled: true, MaxConnections: 5})
	called := false
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/graphql", nil)
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected next handler to be called for non-WebSocket request")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if h.TotalConns() != 0 {
		t.Errorf("expected 0 total conns, got %d", h.TotalConns())
	}
}

func TestNonGraphQLWebSocketPassesThrough(t *testing.T) {
	h := New(config.GraphQLSubscriptionConfig{Enabled: true, MaxConnections: 5})
	called := false
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	// No Sec-WebSocket-Protocol header — not a GraphQL subscription
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected next handler to be called for non-GraphQL WebSocket")
	}
	if h.TotalConns() != 0 {
		t.Errorf("expected 0 total conns, got %d", h.TotalConns())
	}
}

func TestGraphQLSubscriptionTracked(t *testing.T) {
	h := New(config.GraphQLSubscriptionConfig{Enabled: true, MaxConnections: 10})

	var activeInside int64
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activeInside = h.ActiveConns()
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/graphql", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Protocol", "graphql-transport-ws")
	handler.ServeHTTP(rec, req)

	if activeInside != 1 {
		t.Errorf("expected 1 active conn inside handler, got %d", activeInside)
	}
	// After handler returns, activeConns should be decremented
	if h.ActiveConns() != 0 {
		t.Errorf("expected 0 active conns after handler, got %d", h.ActiveConns())
	}
	if h.TotalConns() != 1 {
		t.Errorf("expected 1 total conn, got %d", h.TotalConns())
	}
}

func TestGraphQLSubscriptionWithGraphQLWSProtocol(t *testing.T) {
	h := New(config.GraphQLSubscriptionConfig{Enabled: true, MaxConnections: 10})
	called := false
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/graphql", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Protocol", "graphql-ws")
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected next handler to be called for graphql-ws protocol")
	}
	if h.TotalConns() != 1 {
		t.Errorf("expected 1 total conn, got %d", h.TotalConns())
	}
}

func TestConnectionLimitEnforced(t *testing.T) {
	h := New(config.GraphQLSubscriptionConfig{Enabled: true, MaxConnections: 2})

	// Block the first two connections so they stay active
	block := make(chan struct{})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))

	makeReq := func() *http.Request {
		req := httptest.NewRequest("GET", "/graphql", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Sec-WebSocket-Protocol", "graphql-transport-ws")
		return req
	}

	var wg sync.WaitGroup
	// Fill the connection limit
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, makeReq())
		}()
	}

	// Wait for goroutines to register
	deadline := time.Now().Add(2 * time.Second)
	for h.ActiveConns() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.ActiveConns() != 2 {
		t.Fatalf("expected 2 active conns, got %d", h.ActiveConns())
	}

	// Third connection should be rejected with 503
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, makeReq())
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
	if h.TotalConns() != 2 {
		t.Errorf("expected 2 total conns (rejected not counted), got %d", h.TotalConns())
	}

	// Release blocked connections
	close(block)
	wg.Wait()

	if h.ActiveConns() != 0 {
		t.Errorf("expected 0 active conns after release, got %d", h.ActiveConns())
	}
}

func TestNoLimitWhenMaxConnectionsZero(t *testing.T) {
	h := New(config.GraphQLSubscriptionConfig{Enabled: true, MaxConnections: 0})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/graphql", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Sec-WebSocket-Protocol", "graphql-transport-ws")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
		}
	}
	if h.TotalConns() != 10 {
		t.Errorf("expected 10 total conns, got %d", h.TotalConns())
	}
}

func TestDefaultConfig(t *testing.T) {
	h := New(config.GraphQLSubscriptionConfig{Enabled: true})
	if h.Protocol() != "graphql-transport-ws" {
		t.Errorf("expected default protocol graphql-transport-ws, got %s", h.Protocol())
	}
	if h.pingInterval != 30*time.Second {
		t.Errorf("expected default ping interval 30s, got %s", h.pingInterval)
	}
}

func TestByRouteManager(t *testing.T) {
	m := NewSubscriptionByRoute()
	m.AddRoute("route1", config.GraphQLSubscriptionConfig{
		Enabled:        true,
		Protocol:       "graphql-ws",
		MaxConnections: 100,
	})
	m.AddRoute("route2", config.GraphQLSubscriptionConfig{
		Enabled:        true,
		MaxConnections: 50,
	})

	h1 := m.GetHandler("route1")
	if h1 == nil {
		t.Fatal("expected handler for route1")
	}
	if h1.Protocol() != "graphql-ws" {
		t.Errorf("expected graphql-ws, got %s", h1.Protocol())
	}

	h2 := m.GetHandler("route2")
	if h2 == nil {
		t.Fatal("expected handler for route2")
	}
	if h2.Protocol() != "graphql-transport-ws" {
		t.Errorf("expected default protocol for route2, got %s", h2.Protocol())
	}

	if m.GetHandler("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := m.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats entries, got %d", len(stats))
	}
	s1, ok := stats["route1"]
	if !ok {
		t.Fatal("expected stats for route1")
	}
	sm := s1.(map[string]interface{})
	if sm["protocol"] != "graphql-ws" {
		t.Errorf("expected graphql-ws in stats, got %v", sm["protocol"])
	}
	if sm["max_connections"] != 100 {
		t.Errorf("expected max_connections 100 in stats, got %v", sm["max_connections"])
	}
}
