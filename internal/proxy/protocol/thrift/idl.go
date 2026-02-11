package thrift

import (
	"fmt"
	"sync"

	"github.com/cloudwego/thriftgo/parser"
	"github.com/cloudwego/thriftgo/semantic"
	"github.com/wudi/gateway/internal/config"
)

// schemaCache caches parsed IDL schemas keyed by "idlFile:serviceName".
type schemaCache struct {
	mu      sync.RWMutex
	schemas map[string]*serviceSchema
}

func newSchemaCache() *schemaCache {
	return &schemaCache{
		schemas: make(map[string]*serviceSchema),
	}
}

// serviceSchema holds the parsed schema for a single Thrift service.
type serviceSchema struct {
	ast      *parser.Thrift
	service  *parser.Service
	methods  map[string]*methodSchema
	structs  map[string]*parser.StructLike
	enums    map[string]*parser.Enum
	typedefs map[string]*parser.Type
}

// methodSchema holds parsed metadata for a single Thrift method.
type methodSchema struct {
	function   *parser.Function
	args       []*parser.Field
	returnType *parser.Type
	throws     []*parser.Field
	oneway     bool
	void       bool
}

// getServiceSchema returns a cached or freshly parsed service schema.
func (c *schemaCache) getServiceSchema(idlFile, serviceName string) (*serviceSchema, error) {
	key := idlFile + ":" + serviceName

	c.mu.RLock()
	if s, ok := c.schemas[key]; ok {
		c.mu.RUnlock()
		return s, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock.
	if s, ok := c.schemas[key]; ok {
		return s, nil
	}

	s, err := parseServiceSchema(idlFile, serviceName)
	if err != nil {
		return nil, err
	}
	c.schemas[key] = s
	return s, nil
}

// getServiceSchemaFromContent parses IDL from a string (used in tests).
func (c *schemaCache) getServiceSchemaFromContent(filename, content, serviceName string) (*serviceSchema, error) {
	ast, err := parser.ParseString(filename, content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse thrift IDL: %w", err)
	}
	return buildServiceSchema(ast, serviceName)
}

// parseServiceSchema parses an IDL file and extracts the named service.
func parseServiceSchema(idlFile, serviceName string) (*serviceSchema, error) {
	ast, err := parser.ParseFile(idlFile, nil, true)
	if err != nil {
		return nil, fmt.Errorf("failed to parse thrift IDL %s: %w", idlFile, err)
	}
	return buildServiceSchema(ast, serviceName)
}

// buildServiceSchema constructs a serviceSchema from a parsed AST.
func buildServiceSchema(ast *parser.Thrift, serviceName string) (*serviceSchema, error) {
	// Resolve symbols (fills in Type.Category on all types).
	if err := semantic.ResolveSymbols(ast); err != nil {
		return nil, fmt.Errorf("failed to resolve thrift symbols: %w", err)
	}

	// Find the target service.
	var svc *parser.Service
	for _, s := range ast.Services {
		if s.Name == serviceName {
			svc = s
			break
		}
	}
	if svc == nil {
		return nil, fmt.Errorf("service %q not found in IDL", serviceName)
	}

	// Build method lookup.
	methods := make(map[string]*methodSchema, len(svc.Functions))
	for _, f := range svc.Functions {
		methods[f.Name] = &methodSchema{
			function:   f,
			args:       f.Arguments,
			returnType: f.FunctionType,
			throws:     f.Throws,
			oneway:     f.Oneway,
			void:       f.Void,
		}
	}

	// Collect all struct-like types (structs, unions, exceptions) and enums
	// from the AST and its includes.
	structs := make(map[string]*parser.StructLike)
	enums := make(map[string]*parser.Enum)
	typedefs := make(map[string]*parser.Type)

	collectTypes(ast, structs, enums, typedefs)

	return &serviceSchema{
		ast:      ast,
		service:  svc,
		methods:  methods,
		structs:  structs,
		enums:    enums,
		typedefs: typedefs,
	}, nil
}

// typeStringToCategory maps YAML type strings to parser.Category values.
var typeStringToCategory = map[string]parser.Category{
	"bool":   parser.Category_Bool,
	"byte":   parser.Category_Byte,
	"i16":    parser.Category_I16,
	"i32":    parser.Category_I32,
	"i64":    parser.Category_I64,
	"double": parser.Category_Double,
	"string": parser.Category_String,
	"binary": parser.Category_Binary,
	"struct": parser.Category_Struct,
	"list":   parser.Category_List,
	"set":    parser.Category_Set,
	"map":    parser.Category_Map,
}

// buildServiceSchemaFromConfig constructs a serviceSchema from inline config
// instead of a parsed IDL file. It produces the same serviceSchema structure
// used by the IDL path, so downstream codec/invoker code is unchanged.
func buildServiceSchemaFromConfig(cfg config.ThriftTranslateConfig) (*serviceSchema, error) {
	// Build enums.
	enums := make(map[string]*parser.Enum, len(cfg.Enums))
	for name, vals := range cfg.Enums {
		values := make([]*parser.EnumValue, 0, len(vals))
		for vname, vval := range vals {
			values = append(values, &parser.EnumValue{
				Name:  vname,
				Value: int64(vval),
			})
		}
		enums[name] = &parser.Enum{
			Name:   name,
			Values: values,
		}
	}

	// Build structs.
	structs := make(map[string]*parser.StructLike, len(cfg.Structs))
	for name, fields := range cfg.Structs {
		pfields := make([]*parser.Field, len(fields))
		for i, fd := range fields {
			pt, err := fieldDefToParserType(fd, cfg.Structs, cfg.Enums)
			if err != nil {
				return nil, fmt.Errorf("struct %s field %s: %w", name, fd.Name, err)
			}
			pfields[i] = &parser.Field{
				ID:   fd.ID,
				Name: fd.Name,
				Type: pt,
			}
		}
		structs[name] = &parser.StructLike{
			Category: "struct",
			Name:     name,
			Fields:   pfields,
		}
	}

	// Build methods.
	methods := make(map[string]*methodSchema, len(cfg.Methods))
	functions := make([]*parser.Function, 0, len(cfg.Methods))

	for mname, mdef := range cfg.Methods {
		// Build args.
		args := make([]*parser.Field, len(mdef.Args))
		for i, fd := range mdef.Args {
			pt, err := fieldDefToParserType(fd, cfg.Structs, cfg.Enums)
			if err != nil {
				return nil, fmt.Errorf("method %s arg %s: %w", mname, fd.Name, err)
			}
			args[i] = &parser.Field{
				ID:   fd.ID,
				Name: fd.Name,
				Type: pt,
			}
		}

		// Build return type and exceptions from result fields.
		var returnType *parser.Type
		var throws []*parser.Field
		isVoid := mdef.Void || mdef.Oneway

		for _, rd := range mdef.Result {
			pt, err := fieldDefToParserType(rd, cfg.Structs, cfg.Enums)
			if err != nil {
				return nil, fmt.Errorf("method %s result %s: %w", mname, rd.Name, err)
			}
			if rd.ID == 0 {
				// Field 0 = success return type.
				returnType = pt
				isVoid = false
			} else {
				// Fields 1+ = exceptions.
				throws = append(throws, &parser.Field{
					ID:   rd.ID,
					Name: rd.Name,
					Type: pt,
				})
			}
		}

		if returnType == nil && !isVoid {
			// No result fields and not explicitly void/oneway — treat as void.
			isVoid = true
		}

		fn := &parser.Function{
			Name:         mname,
			Oneway:       mdef.Oneway,
			Void:         isVoid,
			FunctionType: returnType,
			Arguments:    args,
			Throws:       throws,
		}
		functions = append(functions, fn)

		methods[mname] = &methodSchema{
			function:   fn,
			args:       args,
			returnType: returnType,
			throws:     throws,
			oneway:     mdef.Oneway,
			void:       isVoid,
		}
	}

	svc := &parser.Service{
		Name:      cfg.Service,
		Functions: functions,
	}

	return &serviceSchema{
		service:  svc,
		methods:  methods,
		structs:  structs,
		enums:    enums,
		typedefs: make(map[string]*parser.Type),
	}, nil
}

// fieldDefToParserType converts a config.ThriftFieldDef to a *parser.Type.
func fieldDefToParserType(fd config.ThriftFieldDef, structDefs map[string][]config.ThriftFieldDef, enumDefs map[string]map[string]int) (*parser.Type, error) {
	// Check if type is an enum name.
	if enumDefs != nil {
		if _, ok := enumDefs[fd.Type]; ok {
			return &parser.Type{
				Name:     fd.Type,
				Category: parser.Category_Enum,
			}, nil
		}
	}

	cat, ok := typeStringToCategory[fd.Type]
	if !ok {
		return nil, fmt.Errorf("invalid type %q", fd.Type)
	}

	pt := &parser.Type{
		Name:     fd.Type,
		Category: cat,
	}

	switch fd.Type {
	case "struct":
		pt.Name = fd.Struct
	case "list", "set":
		elemType, err := elemStringToParserType(fd.Elem, structDefs, enumDefs)
		if err != nil {
			return nil, fmt.Errorf("elem type: %w", err)
		}
		pt.ValueType = elemType
	case "map":
		keyType, err := elemStringToParserType(fd.Key, structDefs, enumDefs)
		if err != nil {
			return nil, fmt.Errorf("key type: %w", err)
		}
		valType, err := elemStringToParserType(fd.Value, structDefs, enumDefs)
		if err != nil {
			return nil, fmt.Errorf("value type: %w", err)
		}
		pt.KeyType = keyType
		pt.ValueType = valType
	}

	return pt, nil
}

// elemStringToParserType converts a simple type string (used in elem/key/value)
// to a *parser.Type. Supports scalar types, enum names, and struct names.
func elemStringToParserType(typeStr string, structDefs map[string][]config.ThriftFieldDef, enumDefs map[string]map[string]int) (*parser.Type, error) {
	// Check for enum.
	if enumDefs != nil {
		if _, ok := enumDefs[typeStr]; ok {
			return &parser.Type{
				Name:     typeStr,
				Category: parser.Category_Enum,
			}, nil
		}
	}
	// Check for struct.
	if structDefs != nil {
		if _, ok := structDefs[typeStr]; ok {
			return &parser.Type{
				Name:     typeStr,
				Category: parser.Category_Struct,
			}, nil
		}
	}
	// Check scalar types.
	if cat, ok := typeStringToCategory[typeStr]; ok {
		return &parser.Type{
			Name:     typeStr,
			Category: cat,
		}, nil
	}
	return nil, fmt.Errorf("unknown type %q", typeStr)
}

// collectTypes recursively collects struct-like types, enums, and typedefs.
func collectTypes(ast *parser.Thrift, structs map[string]*parser.StructLike, enums map[string]*parser.Enum, typedefs map[string]*parser.Type) {
	for _, s := range ast.Structs {
		structs[s.Name] = s
	}
	for _, s := range ast.Unions {
		structs[s.Name] = s
	}
	for _, s := range ast.Exceptions {
		structs[s.Name] = s
	}
	for _, e := range ast.Enums {
		enums[e.Name] = e
	}
	for _, td := range ast.Typedefs {
		typedefs[td.Alias] = td.Type
	}

	// Recurse into includes.
	for _, inc := range ast.Includes {
		if inc.Reference != nil {
			collectTypes(inc.Reference, structs, enums, typedefs)
		}
	}
}
