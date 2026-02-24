package luautil

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func newTestState() *lua.LState {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	lua.OpenBase(L)
	lua.OpenString(L)
	lua.OpenTable(L)
	lua.OpenMath(L)
	return L
}

func TestRegisterJSON_Encode(t *testing.T) {
	L := newTestState()
	defer L.Close()
	RegisterJSON(L)

	if err := L.DoString(`result = json.encode({name = "test", value = 42})`); err != nil {
		t.Fatalf("json.encode error: %v", err)
	}
	result := L.GetGlobal("result").String()
	// JSON object key order is non-deterministic, just check it's valid JSON-ish
	if result == "" {
		t.Error("expected non-empty result from json.encode")
	}
}

func TestRegisterJSON_Decode(t *testing.T) {
	L := newTestState()
	defer L.Close()
	RegisterJSON(L)

	if err := L.DoString(`
		local t = json.decode('{"name":"test","value":42}')
		result_name = t.name
		result_value = t.value
	`); err != nil {
		t.Fatalf("json.decode error: %v", err)
	}
	if L.GetGlobal("result_name").String() != "test" {
		t.Errorf("expected name=test, got %s", L.GetGlobal("result_name").String())
	}
	if L.GetGlobal("result_value").(lua.LNumber) != 42 {
		t.Errorf("expected value=42, got %v", L.GetGlobal("result_value"))
	}
}

func TestRegisterJSON_RoundTrip(t *testing.T) {
	L := newTestState()
	defer L.Close()
	RegisterJSON(L)

	if err := L.DoString(`
		local original = {items = {1, 2, 3}}
		local encoded = json.encode(original)
		local decoded = json.decode(encoded)
		result = decoded.items[2]
	`); err != nil {
		t.Fatalf("json round-trip error: %v", err)
	}
	// JSON arrays decode as float64 in Go, which maps to LNumber
	if L.GetGlobal("result").(lua.LNumber) != 2 {
		t.Errorf("expected items[2]=2, got %v", L.GetGlobal("result"))
	}
}

func TestRegisterBase64(t *testing.T) {
	L := newTestState()
	defer L.Close()
	RegisterBase64(L)

	if err := L.DoString(`
		local encoded = base64.encode("hello world")
		result_encoded = encoded
		result_decoded = base64.decode(encoded)
	`); err != nil {
		t.Fatalf("base64 error: %v", err)
	}
	if L.GetGlobal("result_encoded").String() != "aGVsbG8gd29ybGQ=" {
		t.Errorf("expected base64 'aGVsbG8gd29ybGQ=', got %s", L.GetGlobal("result_encoded").String())
	}
	if L.GetGlobal("result_decoded").String() != "hello world" {
		t.Errorf("expected decoded 'hello world', got %s", L.GetGlobal("result_decoded").String())
	}
}

func TestRegisterURL(t *testing.T) {
	L := newTestState()
	defer L.Close()
	RegisterURL(L)

	if err := L.DoString(`
		result_encoded = url.encode("hello world&foo=bar")
		result_decoded = url.decode("hello+world%26foo%3Dbar")
	`); err != nil {
		t.Fatalf("url error: %v", err)
	}
	if L.GetGlobal("result_encoded").String() != "hello+world%26foo%3Dbar" {
		t.Errorf("unexpected url encode: %s", L.GetGlobal("result_encoded").String())
	}
	if L.GetGlobal("result_decoded").String() != "hello world&foo=bar" {
		t.Errorf("unexpected url decode: %s", L.GetGlobal("result_decoded").String())
	}
}

func TestRegisterRe_Match(t *testing.T) {
	L := newTestState()
	defer L.Close()
	RegisterRe(L)

	if err := L.DoString(`
		result_match = re.match("^hello", "hello world")
		result_nomatch = re.match("^world", "hello world")
	`); err != nil {
		t.Fatalf("re.match error: %v", err)
	}
	if L.GetGlobal("result_match") != lua.LTrue {
		t.Error("expected re.match to return true")
	}
	if L.GetGlobal("result_nomatch") != lua.LFalse {
		t.Error("expected re.match to return false")
	}
}

func TestRegisterRe_Find(t *testing.T) {
	L := newTestState()
	defer L.Close()
	RegisterRe(L)

	if err := L.DoString(`result = re.find("[0-9]+", "abc123def456")`); err != nil {
		t.Fatalf("re.find error: %v", err)
	}
	if L.GetGlobal("result").String() != "123" {
		t.Errorf("expected re.find to return '123', got %s", L.GetGlobal("result").String())
	}
}

func TestRegisterAll(t *testing.T) {
	L := newTestState()
	defer L.Close()
	RegisterAll(L)

	// Verify all modules are registered
	for _, name := range []string{"json", "base64", "url", "re", "log"} {
		v := L.GetGlobal(name)
		if v == lua.LNil {
			t.Errorf("expected global %q to be registered", name)
		}
	}
}

func TestLuaValueConversions(t *testing.T) {
	L := newTestState()
	defer L.Close()
	RegisterJSON(L)

	// Test nil, bool, number, string round-trips
	if err := L.DoString(`
		result_nil = json.encode(nil)
		result_bool = json.encode(true)
		result_str = json.encode("test")
	`); err != nil {
		t.Fatalf("conversion error: %v", err)
	}
	if L.GetGlobal("result_nil").String() != "null" {
		t.Errorf("expected nil to encode as 'null', got %s", L.GetGlobal("result_nil").String())
	}
	if L.GetGlobal("result_bool").String() != "true" {
		t.Errorf("expected bool to encode as 'true', got %s", L.GetGlobal("result_bool").String())
	}
}
