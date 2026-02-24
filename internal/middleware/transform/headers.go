package transform

import (
	"net/http"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/variables"
)

// HeaderTransformer transforms request/response headers
type HeaderTransformer struct {
	resolver *variables.Resolver
}

// NewHeaderTransformer creates a new header transformer
func NewHeaderTransformer() *HeaderTransformer {
	return &HeaderTransformer{
		resolver: variables.NewResolver(),
	}
}

// TransformRequest applies header transformations to a request
func (t *HeaderTransformer) TransformRequest(r *http.Request, transform config.HeaderTransform, varCtx *variables.Context) {
	// Add headers
	for name, value := range transform.Add {
		resolved := t.resolver.Resolve(value, varCtx)
		r.Header.Add(name, resolved)
	}

	// Set headers (overwrites existing)
	for name, value := range transform.Set {
		resolved := t.resolver.Resolve(value, varCtx)
		r.Header.Set(name, resolved)
	}

	// Remove headers
	for _, name := range transform.Remove {
		r.Header.Del(name)
	}
}

// TransformResponse applies header transformations to a response
func (t *HeaderTransformer) TransformResponse(w http.ResponseWriter, transform config.HeaderTransform, varCtx *variables.Context) {
	headers := w.Header()

	// Add headers
	for name, value := range transform.Add {
		resolved := t.resolver.Resolve(value, varCtx)
		headers.Add(name, resolved)
	}

	// Set headers (overwrites existing)
	for name, value := range transform.Set {
		resolved := t.resolver.Resolve(value, varCtx)
		headers.Set(name, resolved)
	}

	// Remove headers
	for _, name := range transform.Remove {
		headers.Del(name)
	}
}

// RequestTransformMiddleware creates a middleware for request header transformation
func RequestTransformMiddleware(transform config.HeaderTransform) middleware.Middleware {
	transformer := NewHeaderTransformer()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)
			transformer.TransformRequest(r, transform, varCtx)
			next.ServeHTTP(w, r)
		})
	}
}

// ResponseTransformMiddleware creates a middleware for response header transformation
func ResponseTransformMiddleware(transform config.HeaderTransform) middleware.Middleware {
	transformer := NewHeaderTransformer()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Create a wrapper to intercept the response
			tw := &transformResponseWriter{
				ResponseWriter: w,
				transform:      transform,
				transformer:    transformer,
				request:        r,
			}

			next.ServeHTTP(tw, r)

			// Apply transformations before any writes if not already done
			if !tw.headerWritten {
				tw.applyTransforms()
			}
		})
	}
}

// transformResponseWriter wraps http.ResponseWriter for response transformation
type transformResponseWriter struct {
	http.ResponseWriter
	transform     config.HeaderTransform
	transformer   *HeaderTransformer
	request       *http.Request
	headerWritten bool
}

func (w *transformResponseWriter) applyTransforms() {
	if w.headerWritten {
		return
	}
	w.headerWritten = true

	varCtx := variables.GetFromRequest(w.request)
	w.transformer.TransformResponse(w.ResponseWriter, w.transform, varCtx)
}

func (w *transformResponseWriter) WriteHeader(statusCode int) {
	w.applyTransforms()
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *transformResponseWriter) Write(b []byte) (int, error) {
	w.applyTransforms()
	return w.ResponseWriter.Write(b)
}

// Flush implements http.Flusher
func (w *transformResponseWriter) Flush() {
	w.applyTransforms()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// PrecompiledTransform holds pre-compiled header transformations for efficiency
type PrecompiledTransform struct {
	add    map[string]*variables.CompiledTemplate
	set    map[string]*variables.CompiledTemplate
	remove []string
}

// NewPrecompiledTransform creates a pre-compiled header transform
func NewPrecompiledTransform(transform config.HeaderTransform) *PrecompiledTransform {
	resolver := variables.NewResolver()

	pt := &PrecompiledTransform{
		add:    make(map[string]*variables.CompiledTemplate),
		set:    make(map[string]*variables.CompiledTemplate),
		remove: transform.Remove,
	}

	for name, value := range transform.Add {
		pt.add[name] = resolver.PrecompileTemplate(value)
	}

	for name, value := range transform.Set {
		pt.set[name] = resolver.PrecompileTemplate(value)
	}

	return pt
}

// ApplyToRequest applies pre-compiled transforms to a request
func (pt *PrecompiledTransform) ApplyToRequest(r *http.Request, varCtx *variables.Context) {
	for name, template := range pt.add {
		r.Header.Add(name, template.Resolve(varCtx))
	}

	for name, template := range pt.set {
		r.Header.Set(name, template.Resolve(varCtx))
	}

	for _, name := range pt.remove {
		r.Header.Del(name)
	}
}

// ApplyToResponse applies pre-compiled transforms to a response
func (pt *PrecompiledTransform) ApplyToResponse(w http.ResponseWriter, varCtx *variables.Context) {
	headers := w.Header()

	for name, template := range pt.add {
		headers.Add(name, template.Resolve(varCtx))
	}

	for name, template := range pt.set {
		headers.Set(name, template.Resolve(varCtx))
	}

	for _, name := range pt.remove {
		headers.Del(name)
	}
}
