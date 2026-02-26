package debug

import (
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/wudi/runway/config"
)

var startTime = time.Now()

// Handler serves debug endpoint sub-paths.
type Handler struct {
	path   string
	config *config.Config
}

// New creates a debug handler from config.
func New(cfg config.DebugEndpointConfig, appConfig *config.Config) *Handler {
	path := cfg.Path
	if path == "" {
		path = "/__debug"
	}
	return &Handler{
		path:   strings.TrimRight(path, "/"),
		config: appConfig,
	}
}

// Path returns the configured debug endpoint path.
func (h *Handler) Path() string {
	return h.path
}

// Matches returns true if the request path starts with the debug prefix.
func (h *Handler) Matches(path string) bool {
	return path == h.path || strings.HasPrefix(path, h.path+"/")
}

// ServeHTTP handles debug endpoint requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, h.path)
	sub = strings.TrimPrefix(sub, "/")

	switch sub {
	case "", "request":
		h.handleRequest(w, r)
	case "config":
		h.handleConfig(w, r)
	case "runtime":
		h.handleRuntime(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleRequest echoes the incoming request details.
func (h *Handler) handleRequest(w http.ResponseWriter, r *http.Request) {
	headers := make(map[string][]string, len(r.Header))
	for k, v := range r.Header {
		headers[k] = v
	}

	query := make(map[string][]string, len(r.URL.Query()))
	for k, v := range r.URL.Query() {
		query[k] = v
	}

	var body string
	if r.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
		body = string(b)
	}

	result := map[string]interface{}{
		"method":         r.Method,
		"url":            r.URL.String(),
		"proto":          r.Proto,
		"host":           r.Host,
		"remote_addr":    r.RemoteAddr,
		"content_length": r.ContentLength,
		"headers":        headers,
		"query":          query,
		"timestamp":      time.Now().UTC().Format(time.RFC3339Nano),
	}
	if body != "" {
		result["body"] = body
	}
	if r.TLS != nil {
		result["tls"] = map[string]interface{}{
			"version":     r.TLS.Version,
			"server_name": r.TLS.ServerName,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleConfig returns a sanitized config summary.
func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	routes := make([]map[string]interface{}, 0, len(h.config.Routes))
	for _, route := range h.config.Routes {
		features := []string{}
		if route.Auth.Required {
			features = append(features, "auth")
		}
		if route.RateLimit.Enabled {
			features = append(features, "rate_limit")
		}
		if route.Cache.Enabled {
			features = append(features, "cache")
		}
		if route.CircuitBreaker.Enabled {
			features = append(features, "circuit_breaker")
		}
		if route.CORS.Enabled {
			features = append(features, "cors")
		}
		if route.SpikeArrest.Enabled {
			features = append(features, "spike_arrest")
		}
		if route.ContentReplacer.Enabled {
			features = append(features, "content_replacer")
		}

		upstreams := len(route.Backends)
		routes = append(routes, map[string]interface{}{
			"id":        route.ID,
			"path":      route.Path,
			"methods":   route.Methods,
			"features":  features,
			"upstreams": upstreams,
		})
	}

	listeners := make([]map[string]interface{}, 0, len(h.config.Listeners))
	for _, l := range h.config.Listeners {
		listeners = append(listeners, map[string]interface{}{
			"address":  l.Address,
			"protocol": l.Protocol,
		})
	}

	result := map[string]interface{}{
		"routes":    routes,
		"listeners": listeners,
	}
	if h.config.Registry.Type != "" {
		result["registry"] = h.config.Registry.Type
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleRuntime returns Go runtime stats.
func (h *Handler) handleRuntime(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	result := map[string]interface{}{
		"goroutines":    runtime.NumGoroutine(),
		"cpus":          runtime.NumCPU(),
		"go_version":    runtime.Version(),
		"uptime_seconds": time.Since(startTime).Seconds(),
		"memory": map[string]interface{}{
			"alloc_bytes":       mem.Alloc,
			"total_alloc_bytes": mem.TotalAlloc,
			"sys_bytes":         mem.Sys,
			"heap_alloc_bytes":  mem.HeapAlloc,
			"heap_inuse_bytes":  mem.HeapInuse,
			"heap_objects":      mem.HeapObjects,
		},
		"gc": map[string]interface{}{
			"num_gc":          mem.NumGC,
			"pause_total_ns":  mem.PauseTotalNs,
			"last_pause_ns":   mem.PauseNs[(mem.NumGC+255)%256],
			"next_gc_bytes":   mem.NextGC,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// Stats returns debug endpoint info.
func (h *Handler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"enabled": true,
		"path":    h.path,
	}
}
