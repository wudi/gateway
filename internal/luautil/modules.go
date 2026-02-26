package luautil

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"

	lua "github.com/yuin/gopher-lua"

	"github.com/wudi/runway/internal/logging"
	"go.uber.org/zap"
)

// RegisterAll registers all utility modules on the given Lua state.
func RegisterAll(L *lua.LState) {
	RegisterJSON(L)
	RegisterBase64(L)
	RegisterURL(L)
	RegisterRe(L)
	RegisterLog(L)
}

// RegisterJSON registers the json module with encode and decode functions.
func RegisterJSON(L *lua.LState) {
	mod := L.NewTable()
	L.SetField(mod, "encode", L.NewFunction(jsonEncode))
	L.SetField(mod, "decode", L.NewFunction(jsonDecode))
	L.SetGlobal("json", mod)
}

func jsonEncode(L *lua.LState) int {
	v := L.CheckAny(1)
	data, err := json.Marshal(luaValueToInterface(v))
	if err != nil {
		L.ArgError(1, "json encode: "+err.Error())
		return 0
	}
	L.Push(lua.LString(string(data)))
	return 1
}

func jsonDecode(L *lua.LState) int {
	s := L.CheckString(1)
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		L.ArgError(1, "json decode: "+err.Error())
		return 0
	}
	L.Push(interfaceToLuaValue(L, v))
	return 1
}

// luaValueToInterface converts a Lua value to a Go interface{}.
func luaValueToInterface(v lua.LValue) interface{} {
	switch t := v.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(t)
	case lua.LNumber:
		return float64(t)
	case lua.LString:
		return string(t)
	case *lua.LTable:
		// Check if it's an array (sequential integer keys starting at 1).
		maxn := t.MaxN()
		if maxn > 0 {
			arr := make([]interface{}, 0, maxn)
			for i := 1; i <= maxn; i++ {
				arr = append(arr, luaValueToInterface(t.RawGetInt(i)))
			}
			return arr
		}
		// Otherwise treat as object.
		m := make(map[string]interface{})
		t.ForEach(func(k, val lua.LValue) {
			if ks, ok := k.(lua.LString); ok {
				m[string(ks)] = luaValueToInterface(val)
			}
		})
		return m
	default:
		return v.String()
	}
}

// interfaceToLuaValue converts a Go interface{} to a Lua value.
func interfaceToLuaValue(L *lua.LState, v interface{}) lua.LValue {
	switch t := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(t)
	case float64:
		return lua.LNumber(t)
	case string:
		return lua.LString(t)
	case []interface{}:
		tbl := L.NewTable()
		for _, item := range t {
			tbl.Append(interfaceToLuaValue(L, item))
		}
		return tbl
	case map[string]interface{}:
		tbl := L.NewTable()
		for k, val := range t {
			L.SetField(tbl, k, interfaceToLuaValue(L, val))
		}
		return tbl
	default:
		return lua.LString("")
	}
}

// RegisterBase64 registers the base64 module with encode and decode functions.
func RegisterBase64(L *lua.LState) {
	mod := L.NewTable()
	L.SetField(mod, "encode", L.NewFunction(base64Encode))
	L.SetField(mod, "decode", L.NewFunction(base64Decode))
	L.SetGlobal("base64", mod)
}

func base64Encode(L *lua.LState) int {
	s := L.CheckString(1)
	L.Push(lua.LString(base64.StdEncoding.EncodeToString([]byte(s))))
	return 1
}

func base64Decode(L *lua.LState) int {
	s := L.CheckString(1)
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		L.ArgError(1, "base64 decode: "+err.Error())
		return 0
	}
	L.Push(lua.LString(string(data)))
	return 1
}

// RegisterURL registers the url module with encode and decode functions.
func RegisterURL(L *lua.LState) {
	mod := L.NewTable()
	L.SetField(mod, "encode", L.NewFunction(urlEncode))
	L.SetField(mod, "decode", L.NewFunction(urlDecode))
	L.SetGlobal("url", mod)
}

func urlEncode(L *lua.LState) int {
	s := L.CheckString(1)
	L.Push(lua.LString(url.QueryEscape(s)))
	return 1
}

func urlDecode(L *lua.LState) int {
	s := L.CheckString(1)
	decoded, err := url.QueryUnescape(s)
	if err != nil {
		L.ArgError(1, "url decode: "+err.Error())
		return 0
	}
	L.Push(lua.LString(decoded))
	return 1
}

// RegisterRe registers the re module with match and find functions.
func RegisterRe(L *lua.LState) {
	mod := L.NewTable()
	L.SetField(mod, "match", L.NewFunction(reMatch))
	L.SetField(mod, "find", L.NewFunction(reFind))
	L.SetGlobal("re", mod)
}

func reMatch(L *lua.LState) int {
	pattern := L.CheckString(1)
	s := L.CheckString(2)
	matched, err := regexp.MatchString(pattern, s)
	if err != nil {
		L.ArgError(1, "re match: "+err.Error())
		return 0
	}
	L.Push(lua.LBool(matched))
	return 1
}

func reFind(L *lua.LState) int {
	pattern := L.CheckString(1)
	s := L.CheckString(2)
	re, err := regexp.Compile(pattern)
	if err != nil {
		L.ArgError(1, "re find: "+err.Error())
		return 0
	}
	found := re.FindString(s)
	L.Push(lua.LString(found))
	return 1
}

// RegisterLog registers the log module with info, warn, and error functions.
func RegisterLog(L *lua.LState) {
	mod := L.NewTable()
	L.SetField(mod, "info", L.NewFunction(logInfo))
	L.SetField(mod, "warn", L.NewFunction(logWarn))
	L.SetField(mod, "error", L.NewFunction(logError))
	L.SetGlobal("log", mod)
}

func logInfo(L *lua.LState) int {
	msg := L.CheckString(1)
	logging.Info("lua_log", zap.String("message", msg))
	return 0
}

func logWarn(L *lua.LState) int {
	msg := L.CheckString(1)
	logging.Warn("lua_log", zap.String("message", msg))
	return 0
}

func logError(L *lua.LState) int {
	msg := L.CheckString(1)
	logging.Error("lua_log", zap.String("message", msg))
	return 0
}
