package rules

import (
	"net/http"
	"sync"

	lua "github.com/yuin/gopher-lua"

	"github.com/wudi/gateway/internal/luautil"
	"github.com/wudi/gateway/internal/variables"
)

// ExecuteLuaRequest runs a pre-compiled Lua script in the request phase.
// The script has access to req, ctx, and all utility modules.
func ExecuteLuaRequest(pool *sync.Pool, proto *lua.FunctionProto, r *http.Request, varCtx *variables.Context) error {
	if pool == nil || proto == nil {
		return nil
	}

	L := pool.Get().(*lua.LState)
	defer pool.Put(L)

	L.SetGlobal("req", luautil.NewRequestUserData(L, r))
	L.SetGlobal("ctx", luautil.NewContextUserData(L, r, varCtx))

	fn := L.NewFunctionFromProto(proto)
	return L.CallByParam(lua.P{
		Fn:      fn,
		NRet:    0,
		Protect: true,
	})
}

// ExecuteLuaResponse runs a pre-compiled Lua script in the response phase.
// The script has access to resp, ctx, and all utility modules.
func ExecuteLuaResponse(pool *sync.Pool, proto *lua.FunctionProto, rw *RulesResponseWriter, r *http.Request, varCtx *variables.Context) error {
	if pool == nil || proto == nil {
		return nil
	}

	L := pool.Get().(*lua.LState)
	defer pool.Put(L)

	L.SetGlobal("resp", luautil.NewResponseUserData(L, rw))
	L.SetGlobal("ctx", luautil.NewContextUserData(L, r, varCtx))

	fn := L.NewFunctionFromProto(proto)
	return L.CallByParam(lua.P{
		Fn:      fn,
		NRet:    0,
		Protect: true,
	})
}
