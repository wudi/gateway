---
title: "WASM Plugins"
sidebar_position: 17
---

Execute custom request/response filters written as WebAssembly modules. This enables polyglot extensibility (Rust, C, Go/TinyGo, AssemblyScript, Zig) with sandboxed execution.

The gateway uses [Wazero](https://github.com/tetratelabs/wazero), a zero-dependency pure-Go WASM runtime. Plugins communicate with the gateway through a custom ABI (Application Binary Interface) of host functions and guest exports.

## Configuration

### Global Settings

```yaml
wasm:
  runtime_mode: compiler       # "compiler" (default, AOT) or "interpreter"
  max_memory_pages: 256        # per-instance memory limit (pages x 64KB = 16MB default)
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `runtime_mode` | string | `compiler` | WASM execution mode. `compiler` uses AOT compilation (faster, amd64/arm64 only). `interpreter` is slower but works on all platforms. |
| `max_memory_pages` | int | `256` | Maximum memory pages per WASM instance (1 page = 64KB). Default 256 = 16MB. |

### Per-Route Plugins

```yaml
routes:
  - id: my-api
    path: /api
    backends:
      - url: http://backend:8080
    wasm_plugins:
      - enabled: true
        name: auth-enricher
        path: /etc/gateway/plugins/auth.wasm
        phase: request
        config:
          api_key: "xxx"
        timeout: 5ms
        pool_size: 10
      - enabled: true
        name: response-filter
        path: /etc/gateway/plugins/filter.wasm
        phase: response
        timeout: 5ms
        pool_size: 4
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable this plugin |
| `name` | string | | Human-readable name for metrics and admin API |
| `path` | string | | Path to the `.wasm` file (required) |
| `phase` | string | `both` | Execution phase: `request`, `response`, or `both` |
| `config` | map[string]string | | Arbitrary key-value config passed to guest via `host_get_property("config.key")` |
| `timeout` | duration | `5ms` | Per-invocation execution timeout |
| `pool_size` | int | `4` | Number of pre-instantiated module instances |

Multiple plugins per route execute sequentially: request phase in array order, response phase in reverse order (onion model).

**Validation:** `path` is required and must point to an existing file. `phase` must be `request`, `response`, or `both`. `timeout` and `pool_size` must be non-negative. Mutually exclusive with `passthrough`.

## ABI Contract

### Guest Exports (required)

| Export | Signature | Purpose |
|--------|-----------|---------|
| `allocate` | `(size i32) -> ptr i32` | Allocate guest memory for host to write into |
| `deallocate` | `(ptr i32, size i32)` | Free previously allocated guest memory |
| `on_request` | `(ctx_ptr i32, ctx_len i32) -> action i32` | Request phase filter |
| `on_response` | `(ctx_ptr i32, ctx_len i32) -> action i32` | Response phase filter |

`on_request` and `on_response` are optional — only called when the plugin's `phase` includes that phase.

### Action Return Values

| Value | Name | Description |
|-------|------|-------------|
| `0` | Continue | Proceed to next plugin / next middleware |
| `1` | Pause | Reserved for future body buffering |
| `2` | SendResponse | Guest called `host_send_response`; terminate the request |

### Host Functions (env module)

| Function | Signature | Purpose |
|----------|-----------|---------|
| `host_log` | `(level i32, msg_ptr i32, msg_len i32)` | Log a message (0=trace, 1=debug, 2=info, 3=warn, 4=error) |
| `host_get_header` | `(map_type i32, key_ptr i32, key_len i32, val_ptr i32, val_cap i32) -> i32` | Read a header value; returns bytes written or -1 |
| `host_set_header` | `(map_type i32, key_ptr i32, key_len i32, val_ptr i32, val_len i32)` | Set/replace a header |
| `host_remove_header` | `(map_type i32, key_ptr i32, key_len i32)` | Remove a header |
| `host_get_body` | `(buf_ptr i32, buf_cap i32) -> i32` | Read body into guest buffer; returns length |
| `host_set_body` | `(buf_ptr i32, buf_len i32)` | Replace body |
| `host_get_property` | `(key_ptr i32, key_len i32, val_ptr i32, val_cap i32) -> i32` | Read a property value |
| `host_send_response` | `(status i32, body_ptr i32, body_len i32)` | Send an early response and terminate the chain |

**`map_type` values:** `0` = request headers, `1` = response headers.

### Properties

Properties available via `host_get_property`:

| Key | Description |
|-----|-------------|
| `method` | HTTP method (GET, POST, etc.) |
| `path` | Request path |
| `query` | Raw query string |
| `host` | Request host |
| `scheme` | `http` or `https` |
| `route_id` | The matched route ID |
| `client_ip` | Client remote address |
| `config.<key>` | Plugin config value (from `config` map in YAML) |

### Context Passing

When `on_request` or `on_response` is called, the gateway serializes a JSON context object into guest memory via `allocate`. The guest can parse this for a read-only overview, then use host functions for mutations.

**Request context:**
```json
{
  "method": "GET",
  "path": "/api/users",
  "host": "example.com",
  "scheme": "https",
  "route_id": "my-api",
  "body_size": 0,
  "headers": {"Content-Type": "application/json"},
  "config": {"api_key": "xxx"}
}
```

**Response context:**
```json
{
  "status_code": 200,
  "body_size": 1234,
  "route_id": "my-api",
  "headers": {"Content-Type": "application/json"},
  "config": {"api_key": "xxx"}
}
```

## Writing a Plugin (Rust Example)

```rust
use std::alloc::{alloc, dealloc, Layout};

// Host functions
extern "C" {
    fn host_log(level: i32, msg_ptr: *const u8, msg_len: i32);
    fn host_set_header(map_type: i32, key_ptr: *const u8, key_len: i32, val_ptr: *const u8, val_len: i32);
    fn host_get_property(key_ptr: *const u8, key_len: i32, val_ptr: *mut u8, val_cap: i32) -> i32;
}

#[no_mangle]
pub extern "C" fn allocate(size: i32) -> *mut u8 {
    let layout = Layout::from_size_align(size as usize, 1).unwrap();
    unsafe { alloc(layout) }
}

#[no_mangle]
pub extern "C" fn deallocate(ptr: *mut u8, size: i32) {
    let layout = Layout::from_size_align(size as usize, 1).unwrap();
    unsafe { dealloc(ptr, layout) }
}

#[no_mangle]
pub extern "C" fn on_request(_ctx_ptr: i32, _ctx_len: i32) -> i32 {
    // Add a custom header
    let key = b"X-Wasm-Plugin";
    let val = b"auth-enricher";
    unsafe {
        host_set_header(0, key.as_ptr(), key.len() as i32, val.as_ptr(), val.len() as i32);
    }

    // Read a config property
    let prop_key = b"config.api_key";
    let mut buf = [0u8; 256];
    let n = unsafe {
        host_get_property(prop_key.as_ptr(), prop_key.len() as i32, buf.as_mut_ptr(), buf.len() as i32)
    };
    if n > 0 {
        let msg = b"Got API key from config";
        unsafe { host_log(2, msg.as_ptr(), msg.len() as i32) };
    }

    0 // ActionContinue
}
```

Build with: `cargo build --target wasm32-unknown-unknown --release`

## Memory Management

- Each WASM instance has its own linear memory, limited by `max_memory_pages`.
- The gateway writes context data to guest memory via the `allocate` export and frees it via `deallocate` after the function call.
- All host function memory accesses are bounds-checked.
- Instance pools prevent frequent instantiation overhead. Instances are reused across requests.

## Instance Pool

Each plugin maintains a pool of pre-instantiated WASM modules:

- **Pool size** is set via `pool_size` (default 4).
- On borrow, if the pool is empty, a new instance is created on-the-fly (no request rejection).
- On return, if the pool is full, the excess instance is closed.
- Pool misses are tracked in the admin stats.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| `.wasm` file not found | Error at route setup (also caught by config validation) |
| Compilation failure / missing imports | Error at route setup, route fails to initialize |
| Guest `allocate` returns 0 (OOM) | Log error, skip plugin (continue to next) |
| Guest trap (panic/unreachable) | Catch trap, log, increment errors, return 502 |
| Execution timeout | Context cancellation, increment timeouts, return 504 |
| Invalid action return | Treat as Continue (0), log warning |
| Pool exhausted | Instantiate on-the-fly (log, increment pool_misses) |

## Middleware Chain Position

```
... -> 7.75 mockMW -> 7.8 luaRequestMW -> 7.85 wasmRequestMW -> 8 bodyLimitMW -> ...
... -> 17 responseBodyTransformMW -> 17.05 wasmResponseMW -> 17.1 luaResponseMW -> ...
```

WASM runs after Lua in request phase and before Lua in response phase (onion ordering between the two script engines).

## Admin API

```
GET /wasm-plugins
```

Returns per-route WASM plugin statistics.

**Response (200 OK):**
```json
{
  "my-api": [
    {
      "name": "auth-enricher",
      "phase": "request",
      "request_invocations": 15000,
      "response_invocations": 0,
      "errors": 2,
      "timeouts": 0,
      "total_latency_ns": 45000000,
      "pool": {
        "borrows": 15000,
        "returns": 15000,
        "pool_misses": 3,
        "pool_size": 10
      }
    }
  ]
}
```

## Limitations

- No shared state between WASM instances — each instance has its own memory.
- The custom ABI is simpler than proxy-wasm; it does not support streaming, timers, or gRPC metadata.
- Body access reads the entire body into memory; very large bodies may be impractical.
- WASM compilation happens at route initialization, not at runtime.
