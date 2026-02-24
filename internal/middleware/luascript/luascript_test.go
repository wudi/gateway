package luascript

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/variables"
)

func TestRequestScript_SetHeader(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		RequestScript: `
			local method = req:method()
			local path = req:path()
			req:set_header("X-Lua-Method", method)
			req:set_header("X-Lua-Path", path)
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	var capturedMethod, capturedPath string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Header.Get("X-Lua-Method")
		capturedPath = r.Header.Get("X-Lua-Path")
		w.WriteHeader(http.StatusOK)
	})

	handler := ls.RequestMiddleware()(backend)

	req := httptest.NewRequest("POST", "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedMethod != "POST" {
		t.Errorf("expected X-Lua-Method=POST, got %q", capturedMethod)
	}
	if capturedPath != "/api/test" {
		t.Errorf("expected X-Lua-Path=/api/test, got %q", capturedPath)
	}
}

func TestRequestScript_GetHeader(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		RequestScript: `
			local val = req:get_header("X-Input")
			req:set_header("X-Output", val .. "-processed")
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	var captured string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("X-Output")
		w.WriteHeader(http.StatusOK)
	})

	handler := ls.RequestMiddleware()(backend)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Input", "hello")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured != "hello-processed" {
		t.Errorf("expected X-Output=hello-processed, got %q", captured)
	}
}

func TestRequestScript_QueryParam(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		RequestScript: `
			local v = req:query_param("foo")
			req:set_header("X-Foo", v)
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	var captured string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("X-Foo")
		w.WriteHeader(http.StatusOK)
	})

	handler := ls.RequestMiddleware()(backend)

	req := httptest.NewRequest("GET", "/path?foo=bar", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured != "bar" {
		t.Errorf("expected X-Foo=bar, got %q", captured)
	}
}

func TestResponseScript_ModifyBody(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		ResponseScript: `
			local b = resp:body()
			resp:set_body(b .. " modified")
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("original"))
	})

	handler := ls.ResponseMiddleware()(backend)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if body != "original modified" {
		t.Errorf("expected 'original modified', got %q", body)
	}
}

func TestResponseScript_SetHeader(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		ResponseScript: `
			resp:set_header("X-Lua", "added")
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := ls.ResponseMiddleware()(backend)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Lua") != "added" {
		t.Errorf("expected X-Lua=added, got %q", rec.Header().Get("X-Lua"))
	}
}

func TestResponseScript_ReadStatus(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		ResponseScript: `
			local s = resp:status()
			if s == 201 then
				resp:set_header("X-Was-Created", "true")
			end
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	})

	handler := ls.ResponseMiddleware()(backend)

	req := httptest.NewRequest("POST", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Was-Created") != "true" {
		t.Errorf("expected X-Was-Created=true, got %q", rec.Header().Get("X-Was-Created"))
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
}

func TestNoScript_Passthrough(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		// No scripts configured.
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	called := false
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("passthrough"))
	})

	// Request middleware with nil proto should pass through.
	reqHandler := ls.RequestMiddleware()(backend)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	reqHandler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected backend to be called")
	}
	if rec.Body.String() != "passthrough" {
		t.Errorf("expected 'passthrough', got %q", rec.Body.String())
	}

	// Response middleware with nil proto should pass through.
	called = false
	respHandler := ls.ResponseMiddleware()(backend)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	respHandler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected backend to be called for response middleware")
	}
}

func TestCompileError(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled:       true,
		RequestScript: "this is not valid lua @@@@",
	}

	_, err := New(cfg)
	if err == nil {
		t.Error("expected compile error, got nil")
	}
}

func TestRuntimeError_Request(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled:       true,
		RequestScript: `error("intentional")`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend should not be called on script error")
	})

	handler := ls.RequestMiddleware()(backend)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	if ls.errors.Load() != 1 {
		t.Errorf("expected 1 error, got %d", ls.errors.Load())
	}
}

func TestRuntimeError_Response(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled:        true,
		ResponseScript: `error("intentional")`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := ls.ResponseMiddleware()(backend)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	if ls.errors.Load() != 1 {
		t.Errorf("expected 1 error, got %d", ls.errors.Load())
	}
}

func TestStats(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled:       true,
		RequestScript: `req:set_header("X-Test", "1")`,
		ResponseScript: `
			local b = resp:body()
			resp:set_body(b)
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Run request middleware twice.
	reqHandler := ls.RequestMiddleware()(backend)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		reqHandler.ServeHTTP(rec, req)
	}

	// Run response middleware once.
	respHandler := ls.ResponseMiddleware()(backend)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	respHandler.ServeHTTP(rec, req)

	stats := ls.Stats()
	if stats["requests_run"].(int64) != 2 {
		t.Errorf("expected requests_run=2, got %v", stats["requests_run"])
	}
	if stats["responses_run"].(int64) != 1 {
		t.Errorf("expected responses_run=1, got %v", stats["responses_run"])
	}
	if stats["errors"].(int64) != 0 {
		t.Errorf("expected errors=0, got %v", stats["errors"])
	}
}

func TestLuaScriptByRoute(t *testing.T) {
	m := NewLuaScriptByRoute()
	err := m.AddRoute("route1", config.LuaConfig{
		Enabled:       true,
		RequestScript: `req:set_header("X-Route", "one")`,
	})
	if err != nil {
		t.Fatalf("AddRoute failed: %v", err)
	}

	if m.GetScript("route1") == nil {
		t.Error("expected script for route1")
	}
	if m.GetScript("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}

func TestLuaScriptByRoute_CompileError(t *testing.T) {
	m := NewLuaScriptByRoute()
	err := m.AddRoute("bad", config.LuaConfig{
		Enabled:       true,
		RequestScript: "invalid lua @@@",
	})
	if err == nil {
		t.Error("expected error for invalid script")
	}
	if m.GetScript("bad") != nil {
		t.Error("expected nil for failed route")
	}
}

func TestPoolReuse(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled:       true,
		RequestScript: `req:set_header("X-Pooled", "yes")`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := ls.RequestMiddleware()(backend)

	// Run multiple requests to exercise pool reuse.
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
		}
	}

	if ls.requestsRun.Load() != 10 {
		t.Errorf("expected 10 requests run, got %d", ls.requestsRun.Load())
	}
}

func TestResponseScript_FullPipeline(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		RequestScript: `
			req:set_header("X-Lua-Request", "processed")
		`,
		ResponseScript: `
			local b = resp:body()
			local h = resp:get_header("Content-Type")
			resp:set_header("X-Lua-Response", "processed")
			resp:set_body("lua: " .. b)
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Lua-Request") != "processed" {
			t.Error("expected X-Lua-Request header from request script")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	})

	// Chain: request middleware wrapping response middleware wrapping backend.
	handler := ls.RequestMiddleware()(ls.ResponseMiddleware()(backend))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body, _ := io.ReadAll(rec.Result().Body)
	if string(body) != "lua: hello" {
		t.Errorf("expected 'lua: hello', got %q", string(body))
	}
	if rec.Header().Get("X-Lua-Response") != "processed" {
		t.Errorf("expected X-Lua-Response=processed, got %q", rec.Header().Get("X-Lua-Response"))
	}
}

func TestResponseScript_GetHeader(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		ResponseScript: `
			local ct = resp:get_header("Content-Type")
			resp:set_header("X-Content-Type-Was", ct)
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	handler := ls.ResponseMiddleware()(backend)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Content-Type-Was") != "application/json" {
		t.Errorf("expected X-Content-Type-Was=application/json, got %q", rec.Header().Get("X-Content-Type-Was"))
	}
}

// withVarCtx attaches a variables.Context to the request.
func withVarCtx(r *http.Request, varCtx *variables.Context) *http.Request {
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
	return r.WithContext(ctx)
}

func TestRequestScript_CtxAccess(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		RequestScript: `
			local rid = ctx:route_id()
			req:set_header("X-Route-ID", rid)
			local cid = ctx:client_id()
			req:set_header("X-Client-ID", cid)
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	var capturedRouteID, capturedClientID string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRouteID = r.Header.Get("X-Route-ID")
		capturedClientID = r.Header.Get("X-Client-ID")
		w.WriteHeader(http.StatusOK)
	})

	handler := ls.RequestMiddleware()(backend)

	req := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{
		RouteID: "my-route",
		Identity: &variables.Identity{
			ClientID: "client-abc",
		},
	}
	req = withVarCtx(req, varCtx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedRouteID != "my-route" {
		t.Errorf("expected route_id 'my-route', got %q", capturedRouteID)
	}
	if capturedClientID != "client-abc" {
		t.Errorf("expected client_id 'client-abc', got %q", capturedClientID)
	}
}

func TestRequestScript_UtilityModules(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		RequestScript: `
			local encoded = json.encode({key = "value"})
			req:set_header("X-JSON", encoded)

			local b64 = base64.encode("hello")
			req:set_header("X-B64", b64)

			local matched = re.match("^/api", req:path())
			if matched then
				req:set_header("X-API", "true")
			end
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	var capturedJSON, capturedB64, capturedAPI string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedJSON = r.Header.Get("X-JSON")
		capturedB64 = r.Header.Get("X-B64")
		capturedAPI = r.Header.Get("X-API")
		w.WriteHeader(http.StatusOK)
	})

	handler := ls.RequestMiddleware()(backend)

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedJSON == "" {
		t.Error("expected non-empty X-JSON header")
	}
	if capturedB64 != "aGVsbG8=" {
		t.Errorf("expected X-B64 'aGVsbG8=', got %q", capturedB64)
	}
	if capturedAPI != "true" {
		t.Errorf("expected X-API 'true', got %q", capturedAPI)
	}
}

func TestRequestScript_EarlyTermination(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		RequestScript: `
			return 403, "forbidden by lua"
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backendCalled := false
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := ls.RequestMiddleware()(backend)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if backendCalled {
		t.Error("expected backend NOT to be called on early termination")
	}
	if rec.Code != 403 {
		t.Errorf("expected status 403, got %d", rec.Code)
	}
	if rec.Body.String() != "forbidden by lua" {
		t.Errorf("expected body 'forbidden by lua', got %q", rec.Body.String())
	}
}

func TestRequestScript_NoEarlyTermination(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		RequestScript: `
			req:set_header("X-Lua", "ok")
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backendCalled := false
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := ls.RequestMiddleware()(backend)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !backendCalled {
		t.Error("expected backend to be called")
	}
	if rec.Code != 200 {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestResponseScript_SetStatus(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		ResponseScript: `
			resp:set_status(201)
			resp:set_body("created by lua")
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("original"))
	})

	handler := ls.ResponseMiddleware()(backend)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
	if rec.Body.String() != "created by lua" {
		t.Errorf("expected body 'created by lua', got %q", rec.Body.String())
	}
}

func TestResponseScript_DelHeader(t *testing.T) {
	cfg := config.LuaConfig{
		Enabled: true,
		ResponseScript: `
			resp:del_header("X-Remove-Me")
		`,
	}

	ls, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create LuaScript: %v", err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Remove-Me", "value")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := ls.ResponseMiddleware()(backend)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Remove-Me") != "" {
		t.Errorf("expected X-Remove-Me to be deleted, got %q", rec.Header().Get("X-Remove-Me"))
	}
}

