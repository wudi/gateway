# Extensibility (Custom Plugins & Middleware)

The gateway can be imported as a Go library to build custom binaries with custom features and middleware.

## Public Packages

| Package | Import Path | Description |
|---------|-------------|-------------|
| `gateway` | `github.com/wudi/gateway/gateway` | Builder API, Feature/Middleware interfaces, extension helpers |
| `config` | `github.com/wudi/gateway/config` | Configuration types (`Config`, `RouteConfig`, `Loader`) |
| `variables` | `github.com/wudi/gateway/variables` | Request context, variables, identity |

All other packages (`internal/`) remain private.

## Builder API

```go
import gw "github.com/wudi/gateway/gateway"

cfg, _ := gw.LoadConfig("gateway.yaml")

server, err := gw.New(cfg).
    WithConfigPath("gateway.yaml").    // for hot reload
    WithDefaults().                     // register all built-in features
    AddFeature(myFeature).             // custom Feature implementation
    AddMiddleware(gw.MiddlewareSlot{   // custom per-route middleware
        Name:  "my_middleware",
        After: gw.MWAuth,
        Build: func(routeID string, cfg gw.RouteConfig) gw.Middleware {
            return func(next http.Handler) http.Handler { /* ... */ }
        },
    }).
    Build()

server.Run()
```

### `GatewayBuilder` Methods

- `New(cfg *Config) *GatewayBuilder` — create a builder from config
- `WithConfigPath(path string) *GatewayBuilder` — set config file path for reload
- `WithDefaults() *GatewayBuilder` — register all built-in features
- `WithFeatures(ff ...Feature) *GatewayBuilder` — register multiple features
- `AddFeature(f Feature) *GatewayBuilder` — register one feature
- `AddMiddleware(slot MiddlewareSlot) *GatewayBuilder` — add per-route middleware
- `AddGlobalMiddleware(slot GlobalMiddlewareSlot) *GatewayBuilder` — add global middleware
- `Build() (*Server, error)` — validate and build

### `Server` Methods

- `Run() error` — start and block until shutdown
- `Start() error` — start without blocking
- `Shutdown(timeout time.Duration) error` — graceful shutdown
- `Handler() http.Handler` — root HTTP handler (for testing/embedding)
- `Drain()` / `IsDraining() bool` — connection draining
- `ReloadConfig() ReloadResult` — hot config reload

## Custom Middleware

Middleware is positioned relative to named built-in middleware using `After`/`Before` anchors.

```go
gw.MiddlewareSlot{
    Name:   "tenant_check",
    After:  gw.MWAuth,           // after authentication
    Before: gw.MWRequestRules,   // before request rules (optional)
    Build: func(routeID string, cfg gw.RouteConfig) gw.Middleware {
        // Return nil to skip this middleware for a route
        return func(next http.Handler) http.Handler {
            return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                next.ServeHTTP(w, r)
            })
        }
    },
}
```

### Positioning Rules

- **Only `After`**: insert immediately after that anchor
- **Only `Before`**: insert immediately before that anchor
- **Both**: insert between the two anchors (`After` must precede `Before`)
- **Neither**: append to the end of the chain
- Multiple custom middleware with the same anchor are ordered by registration order
- Invalid anchor names fail at `Build()` time

### Well-Known Anchors (Per-Route)

Constants are defined in `gateway/anchors.go`. Key anchors:

| Constant | Name | Phase |
|----------|------|-------|
| `MWMetrics` | `metrics` | Observability |
| `MWIPFilter` | `ip_filter` | Ingress |
| `MWCORS` | `cors` | Headers |
| `MWRateLimit` | `rate_limit` | Traffic Control |
| `MWAuth` | `auth` | Authentication |
| `MWRequestRules` | `request_rules` | Request Processing |
| `MWValidation` | `validation` | Body |
| `MWWebSocket` | `websocket` | Protocol |
| `MWCache` | `cache` | Caching |
| `MWCircuitBreaker` | `circuit_breaker` | Resilience |
| `MWCompression` | `compression` | Response |
| `MWRequestTransform` | `request_transform` | Transform |
| `MWResponseTransform` | `response_transform` | Transform |

### Well-Known Anchors (Global)

| Constant | Name |
|----------|------|
| `MWGlobalRecovery` | `recovery` |
| `MWGlobalRealIP` | `real_ip` |
| `MWGlobalRequestID` | `request_id` |
| `MWGlobalTracing` | `tracing` |
| `MWGlobalLogging` | `logging` |

## Custom Features

Implement the `Feature` interface:

```go
type Feature interface {
    Name() string
    Setup(routeID string, cfg RouteConfig) error
    RouteIDs() []string
}
```

Optional interfaces:

- `AdminStatsProvider` — expose stats at an admin endpoint
- `MiddlewareProvider` — contribute middleware slots
- `GlobalMiddlewareProvider` — contribute global middleware slots
- `ConfigValidator` — validate extension config at `Build()` time
- `Reconfigurable` — support hot reload via `Reconfigure(cfg *Config) error`

## Plugin Configuration (Extensions)

Plugins define their own config in the gateway YAML under `extensions`:

```yaml
extensions:
  my_plugin:
    header: "X-Custom"
    required: true

routes:
  - id: api
    path: /api
    backends:
      - url: http://localhost:9000
    extensions:
      my_plugin:
        allowed_values: ["a", "b"]
```

The `Extensions` field on both `Config` and `RouteConfig` is `map[string]yaml.RawMessage`. Plugins decode their config using the typed helper:

```go
type MyPluginConfig struct {
    Header   string `yaml:"header"`
    Required bool   `yaml:"required"`
}

gc, err := gw.ParseExtension[MyPluginConfig](cfg.Extensions, "my_plugin")
```

Helper functions:

- `ParseExtension[T](extensions, name) (T, error)` — unmarshal extension config
- `HasExtension(extensions, name) bool` — check if extension exists
