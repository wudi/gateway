package middleware

import "net/http"

// Middleware is a function that wraps an http.Handler
type Middleware func(http.Handler) http.Handler

// Chain represents a chain of middlewares
type Chain struct {
	middlewares []Middleware
}

// NewChain creates a new middleware chain
func NewChain(middlewares ...Middleware) *Chain {
	return &Chain{
		middlewares: middlewares,
	}
}

// Then chains the middlewares and returns the final handler
func (c *Chain) Then(h http.Handler) http.Handler {
	if h == nil {
		h = http.DefaultServeMux
	}

	// Apply middlewares in reverse order so first middleware is outermost
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		h = c.middlewares[i](h)
	}

	return h
}

// ThenFunc chains the middlewares with an http.HandlerFunc
func (c *Chain) ThenFunc(fn http.HandlerFunc) http.Handler {
	if fn == nil {
		return c.Then(nil)
	}
	return c.Then(fn)
}

// Append adds middlewares to the chain and returns a new chain
func (c *Chain) Append(middlewares ...Middleware) *Chain {
	newMiddlewares := make([]Middleware, 0, len(c.middlewares)+len(middlewares))
	newMiddlewares = append(newMiddlewares, c.middlewares...)
	newMiddlewares = append(newMiddlewares, middlewares...)
	return &Chain{middlewares: newMiddlewares}
}

// Prepend adds middlewares to the beginning of the chain
func (c *Chain) Prepend(middlewares ...Middleware) *Chain {
	newMiddlewares := make([]Middleware, 0, len(c.middlewares)+len(middlewares))
	newMiddlewares = append(newMiddlewares, middlewares...)
	newMiddlewares = append(newMiddlewares, c.middlewares...)
	return &Chain{middlewares: newMiddlewares}
}

// Extend extends the chain with another chain
func (c *Chain) Extend(other *Chain) *Chain {
	return c.Append(other.middlewares...)
}

// Len returns the number of middlewares in the chain
func (c *Chain) Len() int {
	return len(c.middlewares)
}

// Builder helps build middleware chains dynamically
type Builder struct {
	middlewares []Middleware
}

// NewBuilder creates a new middleware builder
func NewBuilder() *Builder {
	return &Builder{
		middlewares: make([]Middleware, 0),
	}
}

// Use adds a middleware to the builder
func (b *Builder) Use(m Middleware) *Builder {
	b.middlewares = append(b.middlewares, m)
	return b
}

// UseIf adds a middleware conditionally
func (b *Builder) UseIf(condition bool, m Middleware) *Builder {
	if condition {
		b.middlewares = append(b.middlewares, m)
	}
	return b
}

// Build creates a Chain from the builder
func (b *Builder) Build() *Chain {
	return NewChain(b.middlewares...)
}

// Handler wraps the given handler with all middlewares
func (b *Builder) Handler(h http.Handler) http.Handler {
	return b.Build().Then(h)
}

// HandlerFunc wraps the given handler function with all middlewares
func (b *Builder) HandlerFunc(fn http.HandlerFunc) http.Handler {
	return b.Build().ThenFunc(fn)
}

// WrapFunc converts a middleware-style function to a Middleware
func WrapFunc(fn func(w http.ResponseWriter, r *http.Request, next http.Handler)) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fn(w, r, next)
		})
	}
}
