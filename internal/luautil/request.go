package luautil

import (
	"bytes"
	"io"
	"net/http"

	lua "github.com/yuin/gopher-lua"
)

// NewRequestUserData creates a Lua userdata for HTTP request access.
func NewRequestUserData(L *lua.LState, r *http.Request) *lua.LUserData {
	ud := L.NewUserData()
	ud.Value = r

	mt := L.NewTable()
	index := L.NewTable()

	L.SetField(index, "get_header", L.NewFunction(reqGetHeader))
	L.SetField(index, "set_header", L.NewFunction(reqSetHeader))
	L.SetField(index, "del_header", L.NewFunction(reqDelHeader))
	L.SetField(index, "path", L.NewFunction(reqPath))
	L.SetField(index, "method", L.NewFunction(reqMethod))
	L.SetField(index, "query_param", L.NewFunction(reqQueryParam))
	L.SetField(index, "host", L.NewFunction(reqHost))
	L.SetField(index, "scheme", L.NewFunction(reqScheme))
	L.SetField(index, "remote_addr", L.NewFunction(reqRemoteAddr))
	L.SetField(index, "body", L.NewFunction(reqBody))
	L.SetField(index, "set_body", L.NewFunction(reqSetBody))
	L.SetField(index, "cookie", L.NewFunction(reqCookie))
	L.SetField(index, "set_path", L.NewFunction(reqSetPath))
	L.SetField(index, "set_query", L.NewFunction(reqSetQuery))

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

func reqDelHeader(L *lua.LState) int {
	r := checkRequest(L)
	name := L.CheckString(2)
	r.Header.Del(name)
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

func reqHost(L *lua.LState) int {
	r := checkRequest(L)
	L.Push(lua.LString(r.Host))
	return 1
}

func reqScheme(L *lua.LState) int {
	r := checkRequest(L)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	L.Push(lua.LString(scheme))
	return 1
}

func reqRemoteAddr(L *lua.LState) int {
	r := checkRequest(L)
	L.Push(lua.LString(r.RemoteAddr))
	return 1
}

func reqBody(L *lua.LState) int {
	r := checkRequest(L)
	if r.Body == nil {
		L.Push(lua.LString(""))
		return 1
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		L.Push(lua.LString(""))
		return 1
	}
	// Replace the body so it can be read again downstream.
	r.Body = io.NopCloser(bytes.NewReader(data))
	L.Push(lua.LString(string(data)))
	return 1
}

func reqSetBody(L *lua.LState) int {
	r := checkRequest(L)
	body := L.CheckString(2)
	r.Body = io.NopCloser(bytes.NewReader([]byte(body)))
	r.ContentLength = int64(len(body))
	return 0
}

func reqCookie(L *lua.LState) int {
	r := checkRequest(L)
	name := L.CheckString(2)
	c, err := r.Cookie(name)
	if err != nil {
		L.Push(lua.LString(""))
		return 1
	}
	L.Push(lua.LString(c.Value))
	return 1
}

func reqSetPath(L *lua.LState) int {
	r := checkRequest(L)
	path := L.CheckString(2)
	r.URL.Path = path
	return 0
}

func reqSetQuery(L *lua.LState) int {
	r := checkRequest(L)
	query := L.CheckString(2)
	r.URL.RawQuery = query
	return 0
}
