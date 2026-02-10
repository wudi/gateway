package thrift

import (
	"fmt"
	"sync"

	"github.com/cloudwego/thriftgo/parser"
	"github.com/cloudwego/thriftgo/semantic"
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
