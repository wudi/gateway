# CLAUDE.md

## Build & Test

- Build: `go build ./cmd/gateway/`
- Test all: `go test ./...`
- Test specific package: `go test ./internal/retry/ -v`
- Integration tests (tagged): `go test -tags integration ./test/`
- The `go vet` warning in `internal/registry/kubernetes/kubernetes.go:275` (unreachable code) is pre-existing and unrelated to gateway features.

## Project Structure

- `cmd/gateway/` — entry point
- `internal/config/` — YAML config structs and loader with validation
- `internal/router/` — path-based HTTP routing
- `internal/proxy/` — HTTP reverse proxy with retry support
- `internal/gateway/` — main orchestration: serveHTTP flow, middleware chain, admin API
- `internal/retry/` — retry policy with exponential backoff
- `internal/circuitbreaker/` — circuit breaker via sony/gobreaker v2 TwoStepCircuitBreaker
- `internal/cache/` — LRU in-memory cache with per-route handlers
- `internal/websocket/` — WebSocket proxy via HTTP hijack
- `internal/middleware/` — recovery, request ID, logging, auth, rate limiting
- `internal/loadbalancer/` — round-robin with health-aware backend selection
- `internal/health/` — active backend health checking
- `internal/listener/` — HTTP/TCP/UDP listener management
- `internal/registry/` — service discovery (consul, etcd, kubernetes, memory)

## Design Policy

### Prefer open-source libraries

Implement using open-source libraries whenever possible; minimize reinventing the wheel.

### No backward compatibility shims

This project is pre-release. Do not add backward-compatibility fallbacks, legacy aliases, deprecated fields, or migration code. When a design is superseded, remove the old code entirely. For example, `ServerConfig` was removed in favor of `Listeners` — there is no `Server` field, no "if no listeners, fall back to server config" code path, and no `GATEWAY_PORT` env var. If a config shape changes, update all call sites and tests to use the new shape directly.

## Architecture Rules

### Per-route handler objects must be created once, not per-request

`RouteProxy` caches its `http.Handler` and `retry.Policy` in `NewRouteProxy()`. Never call `proxy.Handler()` on every request — it creates new objects each time, losing accumulated state like metrics. Any per-route stateful object (retry policy, circuit breaker, cache handler) must be created once during route initialization in `addRoute()` and stored on the appropriate manager (`BreakerByRoute`, `CacheByRoute`, etc.) or on `RouteProxy` itself.

### Metrics must be reachable from admin API getters

If a feature tracks metrics (retry counts, cache hits, circuit breaker state), verify the admin API getter actually returns those metrics. Don't create a separate metrics map in `Gateway` and forget to populate it. Instead, pull metrics from the objects that own them (e.g., `RouteProxy.GetRetryMetrics()` reads from the stored `retry.Policy.Metrics`).

### ResponseWriter wrapping is conditional

Only wrap `http.ResponseWriter` with `statusRecorder` or `CachingResponseWriter` when the route actually uses the feature that needs it (circuit breaker or caching). Unconditional wrapping adds overhead to every request and can leak feature-specific headers (like `X-Cache`) onto routes that don't use caching.

### Health checker makes background requests to backends

The health checker probes each backend at its `/health` path. Test backends that use a generic `http.HandlerFunc` handling all paths will see these health check requests. When counting backend calls in tests, either filter by path (`r.URL.Path != "/health"`) or compare relative counts (before/after) rather than absolute counts.

### Circuit breaker uses gobreaker v2

The circuit breaker is backed by `sony/gobreaker/v2` `TwoStepCircuitBreaker`. `Allow()` returns `(func(error), error)` — call the callback with `nil` for success or a non-nil error for failure. Half-open request limiting and transition counting are handled by gobreaker internally. The state string for half-open is `"half-open"` (hyphen, from gobreaker), not `"half_open"`.

### WebSocket/TCP test backends must properly parse HTTP

When writing test backends that accept raw TCP connections for WebSocket upgrade testing, use `http.ReadRequest(bufio.NewReader(conn))` to fully consume the HTTP request including all headers. Using raw `conn.Read()` leaves unparsed bytes in the kernel buffer that corrupt subsequent data reads.

## serveHTTP Flow

The request handling order in `gateway.go:serveHTTP()` is:

1. Route matching
2. Variable context setup
3. Authentication
4. Get route proxy
5. WebSocket upgrade check (bypasses cache/circuit breaker, returns early)
6. Cache HIT check (returns early if hit)
7. Circuit breaker check (returns 503 if open)
8. Conditional ResponseWriter wrapping
9. Request transformations
10. Proxy request (retry policy applied inside proxy layer)
11. Record circuit breaker outcome
12. Store cacheable response

Do not reorder these steps. WebSocket must be before cache/circuit breaker. Cache check must be before circuit breaker (a cache hit avoids touching the backend entirely). Circuit breaker recording must happen after the proxy call completes.
