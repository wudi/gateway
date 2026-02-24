package rules

import (
	"net/http/httptest"
	"sync"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/luautil"
	"github.com/wudi/gateway/variables"
)

func newTestLuaPool() *sync.Pool {
	return &sync.Pool{
		New: func() interface{} {
			L := lua.NewState(lua.Options{SkipOpenLibs: true})
			lua.OpenBase(L)
			lua.OpenString(L)
			lua.OpenTable(L)
			lua.OpenMath(L)
			luautil.RegisterAll(L)
			return L
		},
	}
}

func TestExecuteLuaRequest_SetHeader(t *testing.T) {
	pool := newTestLuaPool()
	proto, err := luautil.CompileScript(`req:set_header("X-Lua-Rule", "hello")`, "test")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{RouteID: "test-route"}

	if err := ExecuteLuaRequest(pool, proto, r, varCtx); err != nil {
		t.Fatalf("execute error: %v", err)
	}

	if r.Header.Get("X-Lua-Rule") != "hello" {
		t.Errorf("expected X-Lua-Rule=hello, got %q", r.Header.Get("X-Lua-Rule"))
	}
}

func TestExecuteLuaRequest_ContextAccess(t *testing.T) {
	pool := newTestLuaPool()
	proto, err := luautil.CompileScript(`
		local rid = ctx:route_id()
		req:set_header("X-Route", rid)
	`, "test-ctx")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{RouteID: "my-route-42"}

	if err := ExecuteLuaRequest(pool, proto, r, varCtx); err != nil {
		t.Fatalf("execute error: %v", err)
	}

	if r.Header.Get("X-Route") != "my-route-42" {
		t.Errorf("expected X-Route=my-route-42, got %q", r.Header.Get("X-Route"))
	}
}

func TestExecuteLuaResponse_ModifyBody(t *testing.T) {
	pool := newTestLuaPool()
	proto, err := luautil.CompileScript(`
		local b = resp:body()
		resp:set_body(b .. " modified")
		resp:set_header("X-Modified", "true")
	`, "test-resp")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	rec := httptest.NewRecorder()
	rw := NewRulesResponseWriter(rec)
	rw.WriteHeader(200)
	rw.Write([]byte("original"))

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{}

	if err := ExecuteLuaResponse(pool, proto, rw, r, varCtx); err != nil {
		t.Fatalf("execute error: %v", err)
	}

	if rw.ReadBody() != "original modified" {
		t.Errorf("expected body 'original modified', got %q", rw.ReadBody())
	}
	if rw.Header().Get("X-Modified") != "true" {
		t.Errorf("expected X-Modified=true, got %q", rw.Header().Get("X-Modified"))
	}
}

func TestExecuteLuaRequest_JSONModule(t *testing.T) {
	pool := newTestLuaPool()
	proto, err := luautil.CompileScript(`
		local data = json.decode('{"key":"value"}')
		req:set_header("X-Key", data.key)
		local encoded = json.encode({result = "ok"})
		req:set_header("X-Encoded", encoded)
	`, "test-json")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{}

	if err := ExecuteLuaRequest(pool, proto, r, varCtx); err != nil {
		t.Fatalf("execute error: %v", err)
	}

	if r.Header.Get("X-Key") != "value" {
		t.Errorf("expected X-Key=value, got %q", r.Header.Get("X-Key"))
	}
	if r.Header.Get("X-Encoded") == "" {
		t.Error("expected non-empty X-Encoded")
	}
}

func TestExecuteLuaRequest_RuntimeError(t *testing.T) {
	pool := newTestLuaPool()
	proto, err := luautil.CompileScript(`error("intentional error")`, "test-err")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{}

	err = ExecuteLuaRequest(pool, proto, r, varCtx)
	if err == nil {
		t.Error("expected runtime error, got nil")
	}
}

func TestExecuteLuaRequest_NilPool(t *testing.T) {
	proto, err := luautil.CompileScript(`req:set_header("X-Lua", "ok")`, "test")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	if err := ExecuteLuaRequest(nil, proto, r, nil); err != nil {
		t.Fatalf("expected no error for nil pool, got %v", err)
	}
}

func TestExecuteLuaResponse_NilPool(t *testing.T) {
	proto, err := luautil.CompileScript(`resp:set_body("x")`, "test")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	rec := httptest.NewRecorder()
	rw := NewRulesResponseWriter(rec)
	r := httptest.NewRequest("GET", "/", nil)

	if err := ExecuteLuaResponse(nil, proto, rw, r, nil); err != nil {
		t.Fatalf("expected no error for nil pool, got %v", err)
	}
}

func TestLuaPool_Reuse(t *testing.T) {
	pool := newTestLuaPool()
	proto, err := luautil.CompileScript(`req:set_header("X-Run", "yes")`, "test-reuse")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	for i := 0; i < 10; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		varCtx := &variables.Context{}
		if err := ExecuteLuaRequest(pool, proto, r, varCtx); err != nil {
			t.Fatalf("run %d: execute error: %v", i, err)
		}
		if r.Header.Get("X-Run") != "yes" {
			t.Errorf("run %d: expected X-Run=yes, got %q", i, r.Header.Get("X-Run"))
		}
	}
}

func TestEngine_LuaPoolInitialization(t *testing.T) {
	// Engine without lua rules should have nil pool
	engine, err := NewEngine(
		[]config.RuleConfig{
			{ID: "r1", Expression: `true`, Action: "set_headers", Headers: config.HeaderTransform{Set: map[string]string{"X": "Y"}}},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}
	if engine.LuaPool() != nil {
		t.Error("expected nil lua pool when no lua actions")
	}

	// Engine with lua rule should have non-nil pool
	engine, err = NewEngine(
		[]config.RuleConfig{
			{ID: "lua1", Expression: `true`, Action: "lua", LuaScript: `req:set_header("X-Lua", "ok")`},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}
	if engine.LuaPool() == nil {
		t.Error("expected non-nil lua pool when lua actions exist")
	}
}

func TestEngine_LuaAction_Integration(t *testing.T) {
	engine, err := NewEngine(
		[]config.RuleConfig{
			{
				ID:         "lua-set-header",
				Expression: `http.request.method == "POST"`,
				Action:     "lua",
				LuaScript:  `req:set_header("X-Lua-Processed", "true")`,
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}

	// POST request → rule matches, Lua sets header
	r := httptest.NewRequest("POST", "http://localhost/", nil)
	env := NewRequestEnv(r, nil)
	results := engine.EvaluateRequest(env)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Action.Type != "lua" {
		t.Errorf("expected action type 'lua', got %q", results[0].Action.Type)
	}
	if results[0].Action.LuaProto == nil {
		t.Error("expected non-nil LuaProto")
	}

	// Execute the lua action
	varCtx := &variables.Context{}
	if err := ExecuteLuaRequest(engine.LuaPool(), results[0].Action.LuaProto, r, varCtx); err != nil {
		t.Fatalf("lua execution error: %v", err)
	}
	if r.Header.Get("X-Lua-Processed") != "true" {
		t.Errorf("expected X-Lua-Processed=true, got %q", r.Header.Get("X-Lua-Processed"))
	}

	// GET request → rule doesn't match
	r = httptest.NewRequest("GET", "http://localhost/", nil)
	env = NewRequestEnv(r, nil)
	results = engine.EvaluateRequest(env)
	if len(results) != 0 {
		t.Errorf("expected 0 results for GET, got %d", len(results))
	}
}

func TestEngine_LuaResponseAction(t *testing.T) {
	engine, err := NewEngine(
		nil,
		[]config.RuleConfig{
			{
				ID:         "lua-resp-modify",
				Expression: `http.response.code == 200`,
				Action:     "lua",
				LuaScript:  `resp:set_header("X-Lua-Resp", "modified")`,
			},
		},
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}

	rec := httptest.NewRecorder()
	rw := NewRulesResponseWriter(rec)
	rw.WriteHeader(200)
	rw.Write([]byte("body"))

	r := httptest.NewRequest("GET", "http://localhost/", nil)
	varCtx := &variables.Context{}
	respEnv := NewResponseEnv(r, varCtx, rw.StatusCode(), rw.Header())

	results := engine.EvaluateResponse(respEnv)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if err := ExecuteLuaResponse(engine.LuaPool(), results[0].Action.LuaProto, rw, r, varCtx); err != nil {
		t.Fatalf("lua execution error: %v", err)
	}

	if rw.Header().Get("X-Lua-Resp") != "modified" {
		t.Errorf("expected X-Lua-Resp=modified, got %q", rw.Header().Get("X-Lua-Resp"))
	}
}
