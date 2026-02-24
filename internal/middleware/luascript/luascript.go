package luascript

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	lua "github.com/yuin/gopher-lua"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/luautil"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/variables"
)

// LuaScript holds pre-compiled Lua scripts and a pool of Lua VMs.
type LuaScript struct {
	requestProto  *lua.FunctionProto
	responseProto *lua.FunctionProto
	pool          sync.Pool

	requestsRun  atomic.Int64
	responsesRun atomic.Int64
	errors       atomic.Int64
}

// New creates a LuaScript from config, pre-compiling request and response scripts.
func New(cfg config.LuaConfig) (*LuaScript, error) {
	ls := &LuaScript{}

	if cfg.RequestScript != "" {
		proto, err := luautil.CompileScript(cfg.RequestScript, "request")
		if err != nil {
			return nil, err
		}
		ls.requestProto = proto
	}

	if cfg.ResponseScript != "" {
		proto, err := luautil.CompileScript(cfg.ResponseScript, "response")
		if err != nil {
			return nil, err
		}
		ls.responseProto = proto
	}

	ls.pool = sync.Pool{
		New: func() interface{} {
			L := lua.NewState(lua.Options{SkipOpenLibs: true})
			// Open only safe libraries.
			lua.OpenBase(L)
			lua.OpenString(L)
			lua.OpenTable(L)
			lua.OpenMath(L)
			luautil.RegisterAll(L)
			return L
		},
	}

	return ls, nil
}

// getLuaState obtains a Lua VM from the pool.
func (ls *LuaScript) getLuaState() *lua.LState {
	return ls.pool.Get().(*lua.LState)
}

// putLuaState returns a Lua VM to the pool.
func (ls *LuaScript) putLuaState(L *lua.LState) {
	ls.pool.Put(L)
}

// RequestMiddleware returns a middleware that executes the request-phase Lua script.
// The script may return two values (status, body) to short-circuit the request.
func (ls *LuaScript) RequestMiddleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		if ls.requestProto == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			L := ls.getLuaState()
			defer ls.putLuaState(L)

			reqUD := luautil.NewRequestUserData(L, r)
			fn := L.NewFunctionFromProto(ls.requestProto)

			L.SetGlobal("req", reqUD)

			// Set ctx global if variable context is available.
			if varCtx := variables.GetFromRequest(r); varCtx != nil {
				L.SetGlobal("ctx", luautil.NewContextUserData(L, r, varCtx))
			}

			ls.requestsRun.Add(1)
			if err := L.CallByParam(lua.P{
				Fn:      fn,
				NRet:    2,
				Protect: true,
			}); err != nil {
				ls.errors.Add(1)
				http.Error(w, "lua request script error", http.StatusInternalServerError)
				return
			}

			// Check for early termination: return status, body
			ret1 := L.Get(-2)
			ret2 := L.Get(-1)
			L.Pop(2)

			if status, ok := ret1.(lua.LNumber); ok && int(status) > 0 {
				code := int(status)
				w.WriteHeader(code)
				if body, ok := ret2.(lua.LString); ok {
					w.Write([]byte(string(body)))
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ResponseMiddleware returns a middleware that executes the response-phase Lua script.
// It buffers the response body so the Lua script can read and modify it.
func (ls *LuaScript) ResponseMiddleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		if ls.responseProto == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &bufferedResponseWriter{
				header: make(http.Header),
				body:   &bytes.Buffer{},
				code:   http.StatusOK,
			}

			next.ServeHTTP(bw, r)

			L := ls.getLuaState()
			defer ls.putLuaState(L)

			respUD := luautil.NewResponseUserData(L, bw)
			fn := L.NewFunctionFromProto(ls.responseProto)

			L.SetGlobal("resp", respUD)

			// Set ctx global if variable context is available.
			if varCtx := variables.GetFromRequest(r); varCtx != nil {
				L.SetGlobal("ctx", luautil.NewContextUserData(L, r, varCtx))
			}

			ls.responsesRun.Add(1)
			if err := L.CallByParam(lua.P{
				Fn:      fn,
				NRet:    0,
				Protect: true,
			}); err != nil {
				ls.errors.Add(1)
				http.Error(w, "lua response script error", http.StatusInternalServerError)
				return
			}

			// Copy buffered response to the real writer.
			for k, vals := range bw.header {
				for _, v := range vals {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(bw.code)
			w.Write(bw.body.Bytes())
		})
	}
}

// Stats returns execution statistics for this script.
func (ls *LuaScript) Stats() map[string]interface{} {
	return map[string]interface{}{
		"requests_run":  ls.requestsRun.Load(),
		"responses_run": ls.responsesRun.Load(),
		"errors":        ls.errors.Load(),
	}
}

// --- Buffered response writer ---

// bufferedResponseWriter captures the response so Lua can inspect and modify it.
// It implements luautil.ResponseBuffer.
type bufferedResponseWriter struct {
	header      http.Header
	body        *bytes.Buffer
	code        int
	wroteHeader bool
}

func (bw *bufferedResponseWriter) Header() http.Header {
	return bw.header
}

func (bw *bufferedResponseWriter) Write(b []byte) (int, error) {
	if !bw.wroteHeader {
		bw.wroteHeader = true
	}
	return bw.body.Write(b)
}

func (bw *bufferedResponseWriter) WriteHeader(code int) {
	if bw.wroteHeader {
		return
	}
	bw.wroteHeader = true
	bw.code = code
}

func (bw *bufferedResponseWriter) Flush() {
	// no-op: the buffer is flushed when written to the real writer.
}

// StatusCode implements luautil.ResponseBuffer.
func (bw *bufferedResponseWriter) StatusCode() int {
	return bw.code
}

// SetStatusCode implements luautil.ResponseBuffer.
func (bw *bufferedResponseWriter) SetStatusCode(code int) {
	bw.code = code
}

// ReadBody implements luautil.ResponseBuffer.
func (bw *bufferedResponseWriter) ReadBody() string {
	return bw.body.String()
}

// SetBody implements luautil.ResponseBuffer.
func (bw *bufferedResponseWriter) SetBody(s string) {
	bw.body.Reset()
	io.WriteString(bw.body, s)
}

// --- LuaScriptByRoute manager ---

// LuaScriptByRoute manages per-route Lua scripts.
type LuaScriptByRoute struct {
	byroute.Manager[*LuaScript]
}

// NewLuaScriptByRoute creates a new per-route Lua script manager.
func NewLuaScriptByRoute() *LuaScriptByRoute {
	return &LuaScriptByRoute{}
}

// AddRoute adds a Lua script for a route.
func (m *LuaScriptByRoute) AddRoute(routeID string, cfg config.LuaConfig) error {
	ls, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, ls)
	return nil
}

// GetScript returns the Lua script for a route.
func (m *LuaScriptByRoute) GetScript(routeID string) *LuaScript {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route Lua script stats.
func (m *LuaScriptByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(ls *LuaScript) interface{} {
		return ls.Stats()
	})
}
