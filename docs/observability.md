# Observability

The gateway provides structured logging, Prometheus metrics, and OpenTelemetry distributed tracing for production monitoring.

## Structured Logging

Logging uses [zap](https://github.com/uber-go/zap) for high-performance structured output. The log format uses variable substitution for access log style output:

```yaml
logging:
  level: "info"        # debug, info, warn, error
  output: "stdout"     # stdout, stderr, or file path
  format: '$remote_addr - [$time_iso8601] "$request_method $request_uri" $status $body_bytes_sent "$http_user_agent" $response_time'
```

All [variables](transformations.md#variables) are available in the format string (`$remote_addr`, `$status`, `$upstream_response_time`, etc.).

### Log Levels

| Level | Description |
|-------|-------------|
| `debug` | Verbose debugging information |
| `info` | Normal operational events (default) |
| `warn` | Potentially harmful situations |
| `error` | Error conditions |

## Prometheus Metrics

Enable the Prometheus metrics endpoint on the admin API:

```yaml
admin:
  enabled: true
  port: 8081
  metrics:
    enabled: true
    path: "/metrics"     # default
```

Scrape metrics at `http://localhost:8081/metrics`:

```bash
# Fetch all Prometheus metrics
curl http://localhost:8081/metrics

# Filter for specific metric families
curl -s http://localhost:8081/metrics | grep gateway_request
```

### Collected Metrics

The gateway exports metrics for:
- Request counts and latencies (per route)
- Backend health and response times
- Circuit breaker state transitions
- Cache hit/miss ratios
- Rate limiter rejections
- Retry attempts and budget exhaustion
- WAF blocks and detections
- Traffic split distribution

## Distributed Tracing

OpenTelemetry tracing with OTLP export:

```yaml
tracing:
  enabled: true
  exporter: "otlp"
  endpoint: "otel-collector:4317"
  service_name: "api-gateway"
  sample_rate: 0.1          # sample 10% of requests
  insecure: true             # use insecure gRPC (for local collectors)
  headers:                   # extra headers for OTLP exporter
    Authorization: "Bearer ${OTEL_TOKEN}"
```

### Trace Propagation

The gateway propagates trace context using W3C Trace Context headers (`traceparent`, `tracestate`). Incoming trace headers are forwarded to backends, and new spans are created for each request through the gateway.

### Verifying Tracing

Check tracing status via the admin API:

```bash
curl http://localhost:8081/tracing
```

### Request ID

Every request gets a unique `X-Request-ID` header. If the client provides one, it is preserved. Otherwise, the gateway generates a new UUID. The request ID is available as `$request_id` in log format strings, header transforms, and rule expressions.

```bash
# The gateway returns X-Request-ID in responses
curl -v http://localhost:8080/api/test 2>&1 | grep X-Request-ID

# Send a custom request ID
curl -H "X-Request-ID: my-trace-id" http://localhost:8080/api/test
```

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `logging.level` | string | `debug`, `info`, `warn`, `error` |
| `logging.output` | string | `stdout`, `stderr`, or file path |
| `logging.format` | string | Access log format with `$variable` substitution |
| `admin.metrics.enabled` | bool | Enable Prometheus metrics |
| `admin.metrics.path` | string | Metrics endpoint path (default `/metrics`) |
| `tracing.exporter` | string | `otlp` |
| `tracing.endpoint` | string | OTLP collector endpoint |
| `tracing.sample_rate` | float | Sampling rate 0.0-1.0 |
| `tracing.service_name` | string | Service name in traces |

See [Configuration Reference](configuration-reference.md#observability) for all fields.
