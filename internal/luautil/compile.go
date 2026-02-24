package luautil

import (
	"strings"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

// CompileScript parses and compiles a Lua source string into a FunctionProto.
func CompileScript(source, name string) (*lua.FunctionProto, error) {
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
