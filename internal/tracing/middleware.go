package tracing

import (
	"net/http"

	"github.com/wudi/runway/internal/middleware"
	"go.opentelemetry.io/otel/trace"
)

// SpanMiddleware wraps a middleware with a named span.
// This is opt-in — use in buildRouteHandler for expensive middleware steps.
func SpanMiddleware(tracer *Tracer, name string, mw middleware.Middleware) middleware.Middleware {
	if tracer == nil || !tracer.enabled {
		return mw
	}
	return func(next http.Handler) http.Handler {
		inner := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.tracer.Start(r.Context(), name,
				trace.WithSpanKind(trace.SpanKindInternal),
			)
			defer span.End()
			inner.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
