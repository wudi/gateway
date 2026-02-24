package luautil

import (
	"net/http"

	lua "github.com/yuin/gopher-lua"
)

// ResponseBuffer is the interface for response objects that support
// reading and modifying status, headers, and body before flushing.
type ResponseBuffer interface {
	Header() http.Header
	StatusCode() int
	SetStatusCode(int)
	ReadBody() string
	SetBody(string)
}

// NewResponseUserData creates a Lua userdata for response access.
// The underlying value must implement ResponseBuffer.
func NewResponseUserData(L *lua.LState, rb ResponseBuffer) *lua.LUserData {
	ud := L.NewUserData()
	ud.Value = rb

	mt := L.NewTable()
	index := L.NewTable()

	L.SetField(index, "get_header", L.NewFunction(respGetHeader))
	L.SetField(index, "set_header", L.NewFunction(respSetHeader))
	L.SetField(index, "del_header", L.NewFunction(respDelHeader))
	L.SetField(index, "status", L.NewFunction(respStatus))
	L.SetField(index, "set_status", L.NewFunction(respSetStatus))
	L.SetField(index, "body", L.NewFunction(respBody))
	L.SetField(index, "set_body", L.NewFunction(respSetBody))

	L.SetField(mt, "__index", index)
	L.SetMetatable(ud, mt)
	return ud
}

func checkResponseBuffer(L *lua.LState) ResponseBuffer {
	ud := L.CheckUserData(1)
	if rb, ok := ud.Value.(ResponseBuffer); ok {
		return rb
	}
	L.ArgError(1, "response buffer expected")
	return nil
}

func respGetHeader(L *lua.LState) int {
	rb := checkResponseBuffer(L)
	name := L.CheckString(2)
	L.Push(lua.LString(rb.Header().Get(name)))
	return 1
}

func respSetHeader(L *lua.LState) int {
	rb := checkResponseBuffer(L)
	name := L.CheckString(2)
	value := L.CheckString(3)
	rb.Header().Set(name, value)
	return 0
}

func respDelHeader(L *lua.LState) int {
	rb := checkResponseBuffer(L)
	name := L.CheckString(2)
	rb.Header().Del(name)
	return 0
}

func respStatus(L *lua.LState) int {
	rb := checkResponseBuffer(L)
	L.Push(lua.LNumber(rb.StatusCode()))
	return 1
}

func respSetStatus(L *lua.LState) int {
	rb := checkResponseBuffer(L)
	code := L.CheckInt(2)
	rb.SetStatusCode(code)
	return 0
}

func respBody(L *lua.LState) int {
	rb := checkResponseBuffer(L)
	L.Push(lua.LString(rb.ReadBody()))
	return 1
}

func respSetBody(L *lua.LState) int {
	rb := checkResponseBuffer(L)
	body := L.CheckString(2)
	rb.SetBody(body)
	return 0
}
