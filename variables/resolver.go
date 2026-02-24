package variables

import (
	"sync"
)

// VariableFunc is a function that returns a variable value
type VariableFunc func(ctx *Context) string

// Resolver resolves variables in templates
type Resolver struct {
	builtin    *BuiltinVariables
	custom     map[string]VariableFunc
	customMu   sync.RWMutex
	parser     *Parser
}

// NewResolver creates a new variable resolver
func NewResolver() *Resolver {
	return &Resolver{
		builtin: NewBuiltinVariables(),
		custom:  make(map[string]VariableFunc),
		parser:  NewParser(),
	}
}

// Resolve interpolates variables in a template string
func (r *Resolver) Resolve(template string, ctx *Context) string {
	return r.parser.Replace(template, func(name string) string {
		val, _ := r.Get(name, ctx)
		return val
	})
}

// Get returns a single variable value
func (r *Resolver) Get(name string, ctx *Context) (string, bool) {
	// Check custom variables first (allows overriding builtins)
	r.customMu.RLock()
	if fn, ok := r.custom[name]; ok {
		r.customMu.RUnlock()
		return fn(ctx), true
	}
	r.customMu.RUnlock()

	// Check context custom values
	if ctx != nil {
		if val, ok := ctx.GetCustom(name); ok {
			return val, true
		}
	}

	// Check built-in variables
	return r.builtin.Get(name, ctx)
}

// RegisterCustom adds a custom variable
func (r *Resolver) RegisterCustom(name string, fn VariableFunc) {
	r.customMu.Lock()
	r.custom[name] = fn
	r.customMu.Unlock()
}

// UnregisterCustom removes a custom variable
func (r *Resolver) UnregisterCustom(name string) {
	r.customMu.Lock()
	delete(r.custom, name)
	r.customMu.Unlock()
}

// HasVariables checks if a template contains variables
func (r *Resolver) HasVariables(template string) bool {
	return r.parser.HasVariables(template)
}

// ExtractVariables returns all variable names from a template
func (r *Resolver) ExtractVariables(template string) []string {
	return r.parser.Extract(template)
}

// ResolveMap resolves variables in all values of a string map
func (r *Resolver) ResolveMap(m map[string]string, ctx *Context) map[string]string {
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = r.Resolve(v, ctx)
	}
	return result
}

// ResolveSlice resolves variables in all elements of a string slice
func (r *Resolver) ResolveSlice(s []string, ctx *Context) []string {
	result := make([]string, len(s))
	for i, v := range s {
		result[i] = r.Resolve(v, ctx)
	}
	return result
}

// PrecompileTemplate parses a template for faster repeated resolution
func (r *Resolver) PrecompileTemplate(template string) *CompiledTemplate {
	return &CompiledTemplate{
		template: ParseTemplate(template),
		resolver: r,
	}
}

// CompiledTemplate is a pre-parsed template for efficient resolution
type CompiledTemplate struct {
	template *Template
	resolver *Resolver
}

// Resolve renders the compiled template with the given context
func (ct *CompiledTemplate) Resolve(ctx *Context) string {
	return ct.template.Render(func(name string) string {
		val, _ := ct.resolver.Get(name, ctx)
		return val
	})
}

// HasVariables returns true if the template contains variables
func (ct *CompiledTemplate) HasVariables() bool {
	return ct.template.HasVars
}

// DefaultResolver is the default global resolver
var DefaultResolver = NewResolver()

// Resolve is a convenience function using the default resolver
func Resolve(template string, ctx *Context) string {
	return DefaultResolver.Resolve(template, ctx)
}

// Get is a convenience function using the default resolver
func Get(name string, ctx *Context) (string, bool) {
	return DefaultResolver.Get(name, ctx)
}

// RegisterCustom registers a custom variable with the default resolver
func RegisterCustom(name string, fn VariableFunc) {
	DefaultResolver.RegisterCustom(name, fn)
}
