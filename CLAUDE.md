# CLAUDE.md

## Build & Test

- Build: `go build -o ./build/runway ./cmd/runway/`
- Test all: `go test ./...`
- Test specific package: `go test ./internal/retry/ -v`
- Integration tests (tagged): `go test -tags integration ./test/`

## Project Structure

- `cmd/runway/` — entry point
- `internal/config/` — YAML config structs and loader with validation
- `internal/router/` — path-based HTTP routing
- `internal/proxy/` — HTTP reverse proxy with retry support
- `internal/runway/` — main orchestration: serveHTTP flow, middleware chain, admin API
- `internal/retry/` — retry policy with exponential backoff
- `internal/circuitbreaker/` — circuit breaker via sony/gobreaker v2 TwoStepCircuitBreaker
- `internal/cache/` — LRU in-memory cache with per-route handlers
- `internal/websocket/` — WebSocket proxy via HTTP hijack
- `internal/middleware/` — recovery, request ID, logging, auth, rate limiting
- `internal/loadbalancer/` — round-robin with health-aware backend selection
- `internal/health/` — active backend health checking
- `internal/listener/` — HTTP/TCP/UDP listener management
- `internal/registry/` — service discovery (consul, etcd, kubernetes, memory)
- `internal/rules/` — rules engine via expr-lang/expr with Cloudflare-style expressions
- `internal/trafficshape/` — traffic shaping: throttle (x/time/rate), bandwidth limiting, priority admission

## Design Policy

### Documentation is mandatory

Every code change that adds, modifies, or removes a feature must include corresponding updates to `/docs/`. This applies to new config fields, new middleware, new admin endpoints, changed validation rules, changed defaults, and changed behavior. The relevant topic file (e.g., `security.md`, `resilience.md`) must be updated with accurate descriptions and working YAML examples. If a new config field is added, it must also appear in `docs/configuration-reference.md`. If an admin endpoint is added or changed, `docs/admin-api.md` must be updated. Treat documentation as part of the implementation — a feature is not complete until its docs are accurate and current.

### Prefer open-source libraries

Implement using open-source libraries whenever possible; minimize reinventing the wheel.

### No backward compatibility shims

This project is pre-release. Do not add backward-compatibility fallbacks, legacy aliases, deprecated fields, or migration code. When a design is superseded, remove the old code entirely. For example, `ServerConfig` was removed in favor of `Listeners` — there is no `Server` field, no "if no listeners, fall back to server config" code path, and no `RUNWAY_PORT` env var. If a config shape changes, update all call sites and tests to use the new shape directly.

## Architecture Rules

### Per-route handler objects must be created once, not per-request

`RouteProxy` caches its `http.Handler` and `retry.Policy` in `NewRouteProxy()`. Never call `proxy.Handler()` on every request — it creates new objects each time, losing accumulated state like metrics. Any per-route stateful object (retry policy, circuit breaker, cache handler) must be created once during route initialization in `addRoute()` and stored on the appropriate manager (`BreakerByRoute`, `CacheByRoute`, etc.) or on `RouteProxy` itself.

### Metrics must be reachable from admin API getters

If a feature tracks metrics (retry counts, cache hits, circuit breaker state), verify the admin API getter actually returns those metrics. Don't create a separate metrics map in `Runway` and forget to populate it. Instead, pull metrics from the objects that own them (e.g., `RouteProxy.GetRetryMetrics()` reads from the stored `retry.Policy.Metrics`).

### ResponseWriter wrapping is conditional

Only wrap `http.ResponseWriter` with `statusRecorder` or `CachingResponseWriter` when the route actually uses the feature that needs it (circuit breaker or caching). Unconditional wrapping adds overhead to every request and can leak feature-specific headers (like `X-Cache`) onto routes that don't use caching.

### Health checker makes background requests to backends

The health checker probes each backend at its `/health` path. Test backends that use a generic `http.HandlerFunc` handling all paths will see these health check requests. When counting backend calls in tests, either filter by path (`r.URL.Path != "/health"`) or compare relative counts (before/after) rather than absolute counts.

### Circuit breaker uses gobreaker v2

The circuit breaker is backed by `sony/gobreaker/v2` `TwoStepCircuitBreaker`. `Allow()` returns `(func(error), error)` — call the callback with `nil` for success or a non-nil error for failure. Half-open request limiting and transition counting are handled by gobreaker internally. The state string for half-open is `"half-open"` (hyphen, from gobreaker), not `"half_open"`.

### WebSocket/TCP test backends must properly parse HTTP

When writing test backends that accept raw TCP connections for WebSocket upgrade testing, use `http.ReadRequest(bufio.NewReader(conn))` to fully consume the HTTP request including all headers. Using raw `conn.Read()` leaves unparsed bytes in the kernel buffer that corrupt subsequent data reads.

### Retry and hedging are mutually exclusive

A route can use either traditional retries (`max_retries > 0`) or hedging (`hedging.enabled: true`), but not both. This is enforced by config validation. Hedging sends speculative duplicate requests to different backends after a delay, while retries sequentially re-attempt the same backend on failure. Retry budget (`budget.ratio`) can be combined with retries but not with hedging.

### Rules engine uses expr-lang/expr with buffering response writer

The rules engine (`internal/rules/`) compiles expressions at startup via `expr.Compile()` with `expr.Env()` and `expr.AsBool()`. Expressions use Cloudflare-style dot notation (`http.request.method`, `ip.src`, etc.) mapped via `expr:"tag"` struct tags. The `RulesResponseWriter` buffers both status and body until `Flush()` is called — this is necessary because the proxy writes the full response (headers, status, body) in a single `ServeHTTP` call, and response rules must evaluate after the proxy completes but before the response is sent to the client. Response phase currently only supports `set_headers` (no terminating actions).

## serveHTTP Flow

The request handling order in `runway.go:serveHTTP()` is:

1. Route matching
1.1. IP filter
1.5. CORS preflight
2. Variable context setup
2.5. Rate limiting (reject with 429)
2.6. Throttle — delay/queue via x/time/rate token bucket (returns 503 on timeout)
3. Authentication
3.1. Priority admission control — shared semaphore with heap queue (returns 503 on timeout)
3.5. Request rules — global then per-route (terminates on block/redirect/custom_response)
3.7. Fault injection — inject delays/aborts for chaos testing (returns configured status on abort)
4. Get route proxy
4.5. Body size limit
4.6. Bandwidth limiting — wrap request body + response writer with rate-limited I/O
4.7. Request validation
5. WebSocket upgrade check (bypasses cache/circuit breaker, returns early)
6. Cache HIT check (returns early if hit)
6.5. Request coalescing — singleflight dedup of concurrent identical cache misses (shares response, timeout fallback)
7. Circuit breaker check (returns 503 if open)
7.5. Adaptive concurrency limiting — AIMD-based concurrency control (returns 503 when at limit)
8. Conditional ResponseWriter wrapping
8.5. RulesResponseWriter wrapping (if response rules exist)
9. Request transformations
10. Proxy request (retry policy applied inside proxy layer)
10.1. Response body transform
10.2. Response rules — global then per-route (via RulesResponseWriter, then flush)
10.5. Mirror
11. Record circuit breaker outcome
12. Store cacheable response
13. Metrics

Do not reorder these steps. Throttle must be after rate limiting (rejected requests never enter the queue). Priority must be after auth (so `Identity.ClientID` is available for level determination). Bandwidth must be after body limit and before validation/websocket. WebSocket must be before cache/circuit breaker. Cache check must be before coalescing and circuit breaker (a cache hit avoids touching the backend entirely). Coalescing must be after cache (only cache misses are coalesced) and before circuit breaker (coalesced requests share circuit breaker outcomes). Circuit breaker recording must happen after the proxy call completes. Request rules must be after auth (so `auth.*` fields are populated). Response rules must be before circuit breaker outcome recording and cache store. Adaptive concurrency must be after circuit breaker (when circuit breaker is open, requests don't reach the limiter) and before compression (measured latency should include the proxy round-trip).

## Admin UI (`ui/`)

### Overview

The admin UI is a React SPA served at `/ui/` on the admin port (8081), gated by `admin.ui.enabled: true`. It provides an attention-driven, route-centric management dashboard using a Linear.app-inspired dark design system.

### Tech Stack

React 18, TypeScript, Vite, Tailwind CSS 3, TanStack React Query v5, react-router-dom v6, lucide-react, clsx + tailwind-merge. No component library — all components are hand-built with Tailwind.

### Design System

Dark mode only. Design tokens defined as CSS custom properties in `src/index.css`, extended as Tailwind theme colors:

- Backgrounds: `bg-primary` (#0A0A0A), `bg-secondary` (#141414), `bg-elevated` (#1C1C1C)
- Borders: `border` (#2A2A2A) — always 1px, no shadows
- Text: `text-primary` (#ECECEC), `text-secondary` (#888888), `text-tertiary` (#555555)
- Corners: `rounded-lg` for cards/panels, `rounded-md` for buttons/inputs
- Modals: glassmorphism (`backdrop-blur-xl bg-white/5 border border-[#2A2A2A]`)
- Transitions: `transition-colors duration-150` on all interactive elements
- Numbers: `font-variant-numeric: tabular-nums` for all numeric table columns

### Design Principles

1. **Route-centric, not feature-centric.** The route is the primary entity. Per-route features (circuit breakers, cache, retries, rate limits) are shown in the Route detail panel, not on separate pages.
2. **Problems first.** Surface degraded state before healthy state. Zero problems = green banner, not empty tables.
3. **Dense tables over card grids.** Operators want scan-friendly rows. Use `StatRow` (single dense row) not `StatCard` grids.
4. **Progressive disclosure.** Route list → slide-over detail panel. No full-page navigations that lose list context.
5. **Keyboard-first.** Cmd+K search, `j`/`k` row navigation, `Enter` to expand, `Esc` to close.

### Navigation (7 pages)

| Path | Page | Purpose |
|------|------|---------|
| `/ui` | Status | Attention-driven dashboard: alerts banner, problems table, system summary row, recent events |
| `/ui/routes` | Routes | Route list (60%) + slide-over detail panel (40%) showing all per-route features |
| `/ui/infrastructure` | Infrastructure | Tabbed: Backends, Listeners, Certificates, Upstreams, Cluster |
| `/ui/traffic` | Traffic Control | Collapsible sections: Rate Limits, Throttle & Shaping, Load Shedding, Adaptive Concurrency |
| `/ui/deployments` | Deployments | Sections: Active Canaries, Blue-Green, A/B Tests, Traffic Splits |
| `/ui/security` | Security | Sections: WAF, Rules Engine, Auth stats, Bot Detection, cert expiry alerts |
| `/ui/operations` | Operations | Config reload + history, Drain toggle, Maintenance toggles, API Keys, Traffic Replay |

### Route Detail Panel

The Routes page uses a split layout: route list on the left, slide-over detail panel on the right. The detail panel shows **only configured features** for the selected route (no "not configured" placeholders). Feature data comes from the `/dashboard` endpoint, partitioned client-side by route ID.

Feature badges in the list view use 2-letter codes (CB, RT, CA, RL, WS, TH) colored by state (green/amber/red). Below 1024px, the detail panel becomes a full-width overlay.

### Data Fetching

- TanStack React Query with `refetchInterval` from `PollingContext` (user-configurable: 2s/5s/10s/30s/off)
- Status page polls `/dashboard` + `/health` every 5s; detail pages poll every 10s; route detail panel polls every 5s while open
- `fetchJSON<T>(path)` uses same origin in production; Vite proxy to `:8081` in dev
- POST mutations use `useMutation` with query invalidation; non-destructive mutations use optimistic updates

### Layout Stability

- Never reorder/remove table rows during a poll. New rows append; removed rows grey out for one cycle before removal.
- Numeric columns use `tabular-nums` to prevent width jitter.
- Status indicators use fixed-width colored dots, not variable-width text.
- Relative timestamps update client-side between polls.

### Interaction Patterns

- **Destructive actions** (drain, cache purge, CB force-open): ConfirmModal with glassmorphism → spinner → inline success/error banner (not toasts). High-impact actions require typing the route name.
- **Non-destructive mutations** (CB reset, maintenance toggle): fire immediately, optimistic UI, brief checkmark fade.

### Empty States

1. Feature not configured: section header with "(not configured)" muted, collapsed. No empty tables.
2. Feature configured, zero data: show table with zero values (zeros are meaningful).
3. Entire page empty: single-line message with docs link.

### Go Embedding

`ui/embed.go` uses `//go:embed dist/*` to bundle built assets. `server.go:adminHandler()` serves them at `/ui/` with SPA fallback (all `/ui/*` paths serve `index.html`). Gated by `admin.ui.enabled`. Vite builds with `base: "/ui/"`.

### Build

- Dev: `cd ui && npm run dev` (Vite on :5173, proxies API to :8081)
- Prod: `cd ui && npm run build` then `go build -o ./build/runway ./cmd/runway/` (embeds `ui/dist/`)

### Responsive

- Sidebar: icon-only below 1200px, hamburger below 768px
- Route detail panel: full-width overlay below 1024px
- Tables: hide non-critical columns at narrow widths; horizontal scroll below 768px
