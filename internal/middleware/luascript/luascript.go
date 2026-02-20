package luascript

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
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
		proto, err := compileScript(cfg.RequestScript, "request")
		if err != nil {
			return nil, err
		}
		ls.requestProto = proto
	}

	if cfg.ResponseScript != "" {
		proto, err := compileScript(cfg.ResponseScript, "response")
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
			return L
		},
	}

	return ls, nil
}

// compileScript parses and compiles a Lua source string into a FunctionProto.
func compileScript(source, name string) (*lua.FunctionProto, error) {
	chunk, err := parse.Parse(strings.NewReader(source), name)
	if err != nil {
		return nil, err
	}
	proto, err := lua.Compile(chunk, name)
	if err != nil {
		return nil, err
	}
	return proto, nil
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
func (ls *LuaScript) RequestMiddleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		if ls.requestProto == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			L := ls.getLuaState()
			defer ls.putLuaState(L)

			reqUD := ls.newRequestUserData(L, r)
			fn := L.NewFunctionFromProto(ls.requestProto)

			L.SetGlobal("req", reqUD)

			ls.requestsRun.Add(1)
			if err := L.CallByParam(lua.P{
				Fn:      fn,
				NRet:    0,
				Protect: true,
			}); err != nil {
				ls.errors.Add(1)
				http.Error(w, "lua request script error", http.StatusInternalServerError)
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

			respUD := ls.newResponseUserData(L, bw)
			fn := L.NewFunctionFromProto(ls.responseProto)

			L.SetGlobal("resp", respUD)

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

// --- Request userdata ---

func (ls *LuaScript) newRequestUserData(L *lua.LState, r *http.Request) *lua.LUserData {
	ud := L.NewUserData()
	ud.Value = r

	mt := L.NewTable()
	index := L.NewTable()

	L.SetField(index, "get_header", L.NewFunction(reqGetHeader))
	L.SetField(index, "set_header", L.NewFunction(reqSetHeader))
	L.SetField(index, "path", L.NewFunction(reqPath))
	L.SetField(index, "method", L.NewFunction(reqMethod))
	L.SetField(index, "query_param", L.NewFunction(reqQueryParam))

	L.SetField(mt, "__index", index)
	L.SetMetatable(ud, mt)
	return ud
}

func checkRequest(L *lua.LState) *http.Request {
	ud := L.CheckUserData(1)
	if r, ok := ud.Value.(*http.Request); ok {
		return r
	}
	L.ArgError(1, "request expected")
	return nil
}

func reqGetHeader(L *lua.LState) int {
	r := checkRequest(L)
	name := L.CheckString(2)
	L.Push(lua.LString(r.Header.Get(name)))
	return 1
}

func reqSetHeader(L *lua.LState) int {
	r := checkRequest(L)
	name := L.CheckString(2)
	value := L.CheckString(3)
	r.Header.Set(name, value)
	return 0
}

func reqPath(L *lua.LState) int {
	r := checkRequest(L)
	L.Push(lua.LString(r.URL.Path))
	return 1
}

func reqMethod(L *lua.LState) int {
	r := checkRequest(L)
	L.Push(lua.LString(r.Method))
	return 1
}

func reqQueryParam(L *lua.LState) int {
	r := checkRequest(L)
	name := L.CheckString(2)
	L.Push(lua.LString(r.URL.Query().Get(name)))
	return 1
}

// --- Response userdata ---

func (ls *LuaScript) newResponseUserData(L *lua.LState, bw *bufferedResponseWriter) *lua.LUserData {
	ud := L.NewUserData()
	ud.Value = bw

	mt := L.NewTable()
	index := L.NewTable()

	L.SetField(index, "get_header", L.NewFunction(respGetHeader))
	L.SetField(index, "set_header", L.NewFunction(respSetHeader))
	L.SetField(index, "status", L.NewFunction(respStatus))
	L.SetField(index, "body", L.NewFunction(respBody))
	L.SetField(index, "set_body", L.NewFunction(respSetBody))

	L.SetField(mt, "__index", index)
	L.SetMetatable(ud, mt)
	return ud
}

func checkBufferedWriter(L *lua.LState) *bufferedResponseWriter {
	ud := L.CheckUserData(1)
	if bw, ok := ud.Value.(*bufferedResponseWriter); ok {
		return bw
	}
	L.ArgError(1, "buffered response writer expected")
	return nil
}

func respGetHeader(L *lua.LState) int {
	bw := checkBufferedWriter(L)
	name := L.CheckString(2)
	L.Push(lua.LString(bw.header.Get(name)))
	return 1
}

func respSetHeader(L *lua.LState) int {
	bw := checkBufferedWriter(L)
	name := L.CheckString(2)
	value := L.CheckString(3)
	bw.header.Set(name, value)
	return 0
}

func respStatus(L *lua.LState) int {
	bw := checkBufferedWriter(L)
	L.Push(lua.LNumber(bw.code))
	return 1
}

func respBody(L *lua.LState) int {
	bw := checkBufferedWriter(L)
	L.Push(lua.LString(bw.body.String()))
	return 1
}

func respSetBody(L *lua.LState) int {
	bw := checkBufferedWriter(L)
	body := L.CheckString(2)
	bw.body.Reset()
	bw.body.WriteString(body)
	return 0
}

// --- Buffered response writer ---

// bufferedResponseWriter captures the response so Lua can inspect and modify it.
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

func (bw *bufferedResponseWriter) ReadBody() string {
	return bw.body.String()
}

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
