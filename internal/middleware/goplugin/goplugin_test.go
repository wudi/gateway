package goplugin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

// mockPlugin implements GatewayPlugin for testing without starting real plugin processes.
type mockPlugin struct {
	initErr     error
	onReqResp   PluginResponse
	onRespResp  PluginResponse
	initCalled  bool
	reqCalled   bool
	respCalled  bool
	lastReq     PluginRequest
	lastRespReq PluginRequest
}

func (m *mockPlugin) Init(cfg map[string]string) error {
	m.initCalled = true
	return m.initErr
}

func (m *mockPlugin) OnRequest(req PluginRequest) PluginResponse {
	m.reqCalled = true
	m.lastReq = req
	return m.onReqResp
}

func (m *mockPlugin) OnResponse(req PluginRequest, statusCode int, respHeaders map[string]string, respBody []byte) PluginResponse {
	m.respCalled = true
	m.lastRespReq = req
	return m.onRespResp
}

func TestGoPluginRequestMiddleware_Continue(t *testing.T) {
	mp := &mockPlugin{
		onReqResp: PluginResponse{Action: "continue"},
	}
	p := &GoPlugin{name: "test", phase: "request", impl: mp}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})

	handler := p.RequestMiddleware()(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Custom", "value")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !mp.reqCalled {
		t.Fatal("OnRequest was not called")
	}
	if !called {
		t.Fatal("inner handler was not called")
	}
	if mp.lastReq.Method != "GET" {
		t.Fatalf("expected method GET, got %s", mp.lastReq.Method)
	}
	if mp.lastReq.Path != "/test" {
		t.Fatalf("expected path /test, got %s", mp.lastReq.Path)
	}
	if p.Served() != 1 {
		t.Fatalf("expected served 1, got %d", p.Served())
	}
}

func TestGoPluginRequestMiddleware_SendResponse(t *testing.T) {
	mp := &mockPlugin{
		onReqResp: PluginResponse{
			Action:     "send_response",
			StatusCode: 403,
			Headers:    map[string]string{"X-Blocked": "true"},
			Body:       []byte("forbidden"),
		},
	}
	p := &GoPlugin{name: "test", phase: "request", impl: mp}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := p.RequestMiddleware()(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if called {
		t.Fatal("inner handler should not have been called")
	}
	if w.Code != 403 {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
	if w.Header().Get("X-Blocked") != "true" {
		t.Fatal("expected X-Blocked header")
	}
	if w.Body.String() != "forbidden" {
		t.Fatalf("expected body 'forbidden', got %q", w.Body.String())
	}
}

func TestGoPluginRequestMiddleware_ModifyHeaders(t *testing.T) {
	mp := &mockPlugin{
		onReqResp: PluginResponse{
			Action:  "continue",
			Headers: map[string]string{"X-Added": "by-plugin"},
		},
	}
	p := &GoPlugin{name: "test", phase: "request", impl: mp}

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Added")
	})

	handler := p.RequestMiddleware()(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if gotHeader != "by-plugin" {
		t.Fatalf("expected X-Added header 'by-plugin', got %q", gotHeader)
	}
}

func TestGoPluginResponseMiddleware_Continue(t *testing.T) {
	mp := &mockPlugin{
		onRespResp: PluginResponse{Action: "continue"},
	}
	p := &GoPlugin{name: "test", phase: "response", impl: mp}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "ok")
		w.WriteHeader(200)
		w.Write([]byte("backend response"))
	})

	handler := p.ResponseMiddleware()(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !mp.respCalled {
		t.Fatal("OnResponse was not called")
	}
	if w.Body.String() != "backend response" {
		t.Fatalf("expected 'backend response', got %q", w.Body.String())
	}
}

func TestGoPluginResponseMiddleware_SendResponse(t *testing.T) {
	mp := &mockPlugin{
		onRespResp: PluginResponse{
			Action:     "send_response",
			StatusCode: 200,
			Headers:    map[string]string{"X-Modified": "true"},
			Body:       []byte("modified body"),
		},
	}
	p := &GoPlugin{name: "test", phase: "response", impl: mp}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("error"))
	})

	handler := p.ResponseMiddleware()(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("X-Modified") != "true" {
		t.Fatal("expected X-Modified header")
	}
	if w.Body.String() != "modified body" {
		t.Fatalf("expected 'modified body', got %q", w.Body.String())
	}
}

func TestGoPluginChain_RequestMiddleware(t *testing.T) {
	mp1 := &mockPlugin{
		onReqResp: PluginResponse{
			Action:  "continue",
			Headers: map[string]string{"X-Plugin-1": "first"},
		},
	}
	mp2 := &mockPlugin{
		onReqResp: PluginResponse{
			Action:  "continue",
			Headers: map[string]string{"X-Plugin-2": "second"},
		},
	}

	chain := &GoPluginChain{
		requestPlugins: []*GoPlugin{
			{name: "p1", impl: mp1},
			{name: "p2", impl: mp2},
		},
		allPlugins: []*GoPlugin{
			{name: "p1", impl: mp1},
			{name: "p2", impl: mp2},
		},
	}

	var gotH1, gotH2 string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotH1 = r.Header.Get("X-Plugin-1")
		gotH2 = r.Header.Get("X-Plugin-2")
	})

	mw := chain.RequestMiddleware()
	if mw == nil {
		t.Fatal("expected non-nil middleware")
	}

	handler := mw(inner)
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if gotH1 != "first" {
		t.Fatalf("expected X-Plugin-1 'first', got %q", gotH1)
	}
	if gotH2 != "second" {
		t.Fatalf("expected X-Plugin-2 'second', got %q", gotH2)
	}
}

func TestGoPluginChain_EmptyMiddleware(t *testing.T) {
	chain := &GoPluginChain{}

	if chain.RequestMiddleware() != nil {
		t.Fatal("expected nil request middleware for empty chain")
	}
	if chain.ResponseMiddleware() != nil {
		t.Fatal("expected nil response middleware for empty chain")
	}
}

func TestGoPluginChain_Stats(t *testing.T) {
	p1 := &GoPlugin{name: "auth", phase: "request"}
	p1.served.Store(5)
	p2 := &GoPlugin{name: "logger", phase: "both"}
	p2.served.Store(10)

	chain := &GoPluginChain{
		allPlugins: []*GoPlugin{p1, p2},
	}

	stats := chain.Stats()
	if len(stats) != 2 {
		t.Fatalf("expected 2 stats entries, got %d", len(stats))
	}

	authStats := stats["auth"].(map[string]interface{})
	if authStats["phase"] != "request" {
		t.Fatalf("expected phase 'request', got %v", authStats["phase"])
	}
	if authStats["served"] != int64(5) {
		t.Fatalf("expected served 5, got %v", authStats["served"])
	}
}

func TestGoPluginByRoute(t *testing.T) {
	mgr := NewGoPluginByRoute("test-key")

	// GetChain on non-existent route returns nil
	if mgr.GetChain("nonexistent") != nil {
		t.Fatal("expected nil chain for nonexistent route")
	}

	// RouteIDs should be empty
	if len(mgr.RouteIDs()) != 0 {
		t.Fatalf("expected 0 route IDs, got %d", len(mgr.RouteIDs()))
	}
}

func TestGoPluginByRoute_AddRoute_NoEnabled(t *testing.T) {
	mgr := NewGoPluginByRoute("test-key")

	// All plugins disabled - should still succeed
	err := mgr.AddRoute("route1", []config.GoPluginRouteConfig{
		{Enabled: false, Name: "disabled", Path: "/nonexistent"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The chain exists but is empty
	chain := mgr.GetChain("route1")
	if chain == nil {
		t.Fatal("expected non-nil chain")
	}
	if len(chain.allPlugins) != 0 {
		t.Fatalf("expected 0 plugins, got %d", len(chain.allPlugins))
	}
}

func TestResponseRecorder(t *testing.T) {
	base := httptest.NewRecorder()
	rec := &responseRecorder{ResponseWriter: base, statusCode: 200}

	rec.WriteHeader(404)
	if rec.statusCode != 404 {
		t.Fatalf("expected status 404, got %d", rec.statusCode)
	}
	if !rec.headerWritten {
		t.Fatal("expected headerWritten to be true")
	}

	n, err := rec.Write([]byte("test body"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 9 {
		t.Fatalf("expected 9 bytes written, got %d", n)
	}
	if rec.body.String() != "test body" {
		t.Fatalf("expected 'test body', got %q", rec.body.String())
	}
}

func TestExtractHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("X-Custom", "value")

	result := extractHeaders(h)
	if result["Content-Type"] != "application/json" {
		t.Fatalf("expected application/json, got %q", result["Content-Type"])
	}
	if result["X-Custom"] != "value" {
		t.Fatalf("expected value, got %q", result["X-Custom"])
	}
}

func TestMakeHandshake(t *testing.T) {
	h := MakeHandshake("")
	if h.MagicCookieValue != "gateway-v1" {
		t.Fatalf("expected default key 'gateway-v1', got %q", h.MagicCookieValue)
	}

	h2 := MakeHandshake("custom-key")
	if h2.MagicCookieValue != "custom-key" {
		t.Fatalf("expected 'custom-key', got %q", h2.MagicCookieValue)
	}
}

func TestMakePluginMap(t *testing.T) {
	pm := MakePluginMap()
	if _, ok := pm["gateway"]; !ok {
		t.Fatal("expected 'gateway' key in plugin map")
	}
}

func TestGoPluginRequestMiddleware_SendResponse_DefaultStatus(t *testing.T) {
	mp := &mockPlugin{
		onReqResp: PluginResponse{
			Action:     "send_response",
			StatusCode: 0, // should default to 200
			Body:       []byte("ok"),
		},
	}
	p := &GoPlugin{name: "test", phase: "request", impl: mp}

	handler := p.RequestMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not be called")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}
