# Go Plugins

The gateway supports extensible Go plugins via [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin). Plugins run as separate processes communicating over net/rpc, providing process isolation, crash safety, and version tolerance — unlike Go's native `plugin` package which requires exact Go version matches.

## Configuration

### Global Settings

```yaml
go_plugins:
  plugin_dir: ./plugins
  handshake_key: "gateway-v1"
  kill_timeout: 5s
```

### Per-Route Plugins

```yaml
routes:
  - id: example
    path: /api/*
    backends:
      - url: http://backend:8080
    go_plugins:
      - enabled: true
        name: my-auth
        path: ./plugins/my-auth
        phase: request
        timeout: 10ms
        config:
          api_key: "secret"
      - enabled: true
        name: response-transform
        path: ./plugins/response-transform
        phase: response
        timeout: 20ms
```

## Config Fields

### Global (`go_plugins`)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `plugin_dir` | string | `""` | Base directory for plugin binaries |
| `handshake_key` | string | `"gateway-v1"` | Shared handshake key for plugin authentication |
| `kill_timeout` | duration | `5s` | Timeout for graceful plugin shutdown |

### Per-Route (`routes[].go_plugins[]`)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable this plugin |
| `name` | string | required | Unique name for the plugin (used in metrics/admin) |
| `path` | string | required | Path to the plugin binary |
| `phase` | string | `"both"` | When to run: `request`, `response`, or `both` |
| `timeout` | duration | `10ms` | Per-invocation timeout |
| `config` | map | `{}` | Arbitrary key-value config passed to `Init()` |

## How It Works

Each plugin binary is started as a child process by the gateway using hashicorp/go-plugin's managed lifecycle. Communication uses net/rpc over a local socket.

### Plugin Lifecycle

1. **Startup**: When a route is initialized, the gateway starts each enabled plugin binary as a subprocess.
2. **Handshake**: The gateway and plugin exchange a handshake using the configured `handshake_key` to verify compatibility.
3. **Init**: The gateway calls `Init(config)` with the plugin's `config` map.
4. **Request Processing**: For each request, the gateway calls `OnRequest` (request phase) and/or `OnResponse` (response phase) depending on the plugin's `phase` setting.
5. **Shutdown**: When the gateway shuts down or reloads, plugin processes are killed.

### Plugin Phases

- **`request`** (step 7.9): Called after mock responses, before body limits. The plugin receives the incoming request and can modify headers or short-circuit with a custom response.
- **`response`** (step 17.08): Called after response body transforms. The plugin receives the backend response and can modify headers/body or replace the response entirely.
- **`both`**: The plugin runs in both phases.

### Plugin Actions

Each plugin call returns a `PluginResponse` with an `action` field:

- `"continue"`: Pass through to the next handler. Any headers in the response are applied to the request (request phase) or response (response phase).
- `"send_response"`: Short-circuit the chain and return the plugin's response directly to the client. The `status_code`, `headers`, and `body` fields are used.

## Writing a Plugin

Plugins must implement the `GatewayPlugin` interface and serve themselves using hashicorp/go-plugin:

```go
package main

import (
    "github.com/hashicorp/go-plugin"
    gp "github.com/wudi/gateway/internal/middleware/goplugin"
)

type MyPlugin struct {
    apiKey string
}

func (p *MyPlugin) Init(config map[string]string) error {
    p.apiKey = config["api_key"]
    return nil
}

func (p *MyPlugin) OnRequest(req gp.PluginRequest) gp.PluginResponse {
    if req.Headers["Authorization"] != "Bearer "+p.apiKey {
        return gp.PluginResponse{
            Action:     "send_response",
            StatusCode: 401,
            Headers:    map[string]string{"Content-Type": "application/json"},
            Body:       []byte(`{"error":"unauthorized"}`),
        }
    }
    return gp.PluginResponse{Action: "continue"}
}

func (p *MyPlugin) OnResponse(req gp.PluginRequest, statusCode int, respHeaders map[string]string, respBody []byte) gp.PluginResponse {
    return gp.PluginResponse{Action: "continue"}
}

func main() {
    plugin.Serve(&plugin.ServeConfig{
        HandshakeConfig: gp.MakeHandshake("gateway-v1"),
        Plugins:         gp.MakePluginMap(),
    })
}
```

Build the plugin as a standalone binary:

```bash
go build -o ./plugins/my-auth ./cmd/plugins/my-auth/
```

## Validation

- `name` is required for each plugin
- `path` is required for each plugin
- `phase` must be `request`, `response`, or `both`
- Plugin names must be unique within a route
- Cannot be combined with `echo: true` on the same route

## Admin API

**`GET /go-plugins`** returns per-route plugin stats:

```json
{
  "example": {
    "my-auth": {
      "phase": "request",
      "served": 1520
    },
    "response-transform": {
      "phase": "response",
      "served": 1518
    }
  }
}
```

## Middleware Chain Position

| Phase | Step | Position |
|-------|------|----------|
| Request | 7.9 | After WASM plugins (7.85), before body limit (8) |
| Response | 17.08 | After WASM response (17.05), before response validation (17.5) |

## See Also

- [WASM Plugins](wasm-plugins.md) — Sandboxed WASM-based plugins
- [Configuration Reference](configuration-reference.md#go-plugins-global) — Config field reference
- [Admin API Reference](admin-api.md#go-plugins) — Admin endpoint reference
