package tracing

import (
	"context"
	"net/http"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Tracer provides distributed tracing functionality via OpenTelemetry
type Tracer struct {
	enabled    bool
	provider   *sdktrace.TracerProvider
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
}

// New creates a new Tracer from config
func New(cfg config.TracingConfig) (*Tracer, error) {
	t := &Tracer{
		enabled: cfg.Enabled,
	}

	if !cfg.Enabled {
		return t, nil
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "api-gateway"
	}
	sampleRate := cfg.SampleRate
	if sampleRate <= 0 {
		sampleRate = 1.0
	}

	ctx := context.Background()

	// Set up OTLP exporter
	opts := []otlptracegrpc.Option{}
	if cfg.Endpoint != "" {
		opts = append(opts, otlptracegrpc.WithEndpoint(cfg.Endpoint))
	}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	t.provider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(sampleRate)),
	)

	otel.SetTracerProvider(t.provider)
	t.propagator = propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
	otel.SetTextMapPropagator(t.propagator)

	t.tracer = t.provider.Tracer("gateway")

	return t, nil
}

// IsEnabled returns whether tracing is enabled
func (t *Tracer) IsEnabled() bool {
	return t.enabled
}

// Middleware returns a middleware that creates root spans per request
func (t *Tracer) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !t.enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Extract incoming trace context
			ctx := t.propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			// Start a span
			ctx, span := t.tracer.Start(ctx, r.Method+" "+r.URL.Path,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPRequestMethodKey.String(r.Method),
					semconv.URLPath(r.URL.Path),
					semconv.ServerAddress(r.Host),
					semconv.UserAgentOriginal(r.UserAgent()),
				),
			)
			defer span.End()

			// Inject trace ID into response header
			if span.SpanContext().HasTraceID() {
				w.Header().Set("X-Trace-ID", span.SpanContext().TraceID().String())
			}

			// Wrap writer to capture status
			tw := &tracingWriter{ResponseWriter: w, statusCode: 200}
			next.ServeHTTP(tw, r.WithContext(ctx))

			span.SetAttributes(attribute.Int("http.response.status_code", tw.statusCode))
			if tw.statusCode >= 500 {
				span.SetStatus(2, http.StatusText(tw.statusCode)) // codes.Error = 2
			}
		})
	}
}

// StartSpan creates a child span in the given context
func (t *Tracer) StartSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	if !t.enabled {
		return ctx, trace.SpanFromContext(ctx)
	}
	return t.tracer.Start(ctx, name)
}

// InjectHeaders injects trace context headers into an outgoing request.
// It uses the OTEL propagator to inject from the source request's context,
// and also copies traceparent/tracestate headers directly as a fallback.
func InjectHeaders(src, dst *http.Request) {
	prop := otel.GetTextMapPropagator()
	prop.Inject(src.Context(), propagation.HeaderCarrier(dst.Header))

	// Direct header copy as fallback for non-OTEL contexts
	if dst.Header.Get("traceparent") == "" {
		if tp := src.Header.Get("traceparent"); tp != "" {
			dst.Header.Set("traceparent", tp)
		}
	}
	if dst.Header.Get("tracestate") == "" {
		if ts := src.Header.Get("tracestate"); ts != "" {
			dst.Header.Set("tracestate", ts)
		}
	}
}

// Close shuts down the tracer
func (t *Tracer) Close() error {
	if t.provider != nil {
		return t.provider.Shutdown(context.Background())
	}
	return nil
}

// Status returns the tracing status for admin API
func (t *Tracer) Status() map[string]interface{} {
	return map[string]interface{}{
		"enabled": t.enabled,
	}
}

// tracingWriter wraps ResponseWriter to capture status code
type tracingWriter struct {
	http.ResponseWriter
	statusCode int
}

func (tw *tracingWriter) WriteHeader(code int) {
	tw.statusCode = code
	tw.ResponseWriter.WriteHeader(code)
}

func (tw *tracingWriter) Flush() {
	if f, ok := tw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
