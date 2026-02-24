package luautil

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestCompileScript_Valid(t *testing.T) {
	proto, err := CompileScript(`return 1 + 1`, "test")
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}
	if proto == nil {
		t.Fatal("expected non-nil proto")
	}

	// Run the compiled script
	L := lua.NewState()
	defer L.Close()

	fn := L.NewFunctionFromProto(proto)
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}
	result := L.Get(-1)
	if result.(lua.LNumber) != 2 {
		t.Errorf("expected 2, got %v", result)
	}
}

func TestCompileScript_Invalid(t *testing.T) {
	_, err := CompileScript(`this is not valid lua @@@`, "bad")
	if err == nil {
		t.Error("expected compile error for invalid script")
	}
}

func TestCompileScript_SyntaxError(t *testing.T) {
	_, err := CompileScript(`if true`, "incomplete")
	if err == nil {
		t.Error("expected error for incomplete syntax")
	}
}
