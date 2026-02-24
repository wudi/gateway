package goplugin

import (
	"bytes"
	"io"
	"net/http"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-plugin"
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware"
	"go.uber.org/zap"
)

// GoPlugin represents a running Go plugin instance.
type GoPlugin struct {
	name    string
	phase   string
	timeout time.Duration
	impl    GatewayPlugin
	client  *plugin.Client
	served  atomic.Int64
}

// newGoPlugin starts a plugin process and initializes it.
func newGoPlugin(cfg config.GoPluginRouteConfig, handshakeKey string) (*GoPlugin, error) {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Millisecond
	}
	phase := cfg.Phase
	if phase == "" {
		phase = "both"
	}

	handshake := MakeHandshake(handshakeKey)

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: handshake,
		Plugins:         MakePluginMap(),
		Cmd:             exec.Command(cfg.Path),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolNetRPC},
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, err
	}

	raw, err := rpcClient.Dispense("gateway")
	if err != nil {
		client.Kill()
		return nil, err
	}

	impl := raw.(GatewayPlugin)
	if err := impl.Init(cfg.Config); err != nil {
		client.Kill()
		return nil, err
	}

	return &GoPlugin{
		name:    cfg.Name,
		phase:   phase,
		timeout: timeout,
		impl:    impl,
		client:  client,
	}, nil
}

// RequestMiddleware returns a middleware that calls the plugin's OnRequest.
func (p *GoPlugin) RequestMiddleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p.served.Add(1)

			var bodyBytes []byte
			if r.Body != nil {
				bodyBytes, _ = io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}

			req := PluginRequest{
				Method:  r.Method,
				Path:    r.URL.Path,
				Headers: extractHeaders(r.Header),
				Body:    bodyBytes,
			}

			resp := p.impl.OnRequest(req)
			if resp.Action == "send_response" {
				for k, v := range resp.Headers {
					w.Header().Set(k, v)
				}
				status := resp.StatusCode
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				w.Write(resp.Body)
				return
			}

			// Apply modified headers
			for k, v := range resp.Headers {
				r.Header.Set(k, v)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ResponseMiddleware returns a middleware that calls the plugin's OnResponse.
func (p *GoPlugin) ResponseMiddleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &responseRecorder{ResponseWriter: w, statusCode: 200}
			next.ServeHTTP(rec, r)

			var bodyBytes []byte
			if r.Body != nil {
				bodyBytes, _ = io.ReadAll(r.Body)
			}

			req := PluginRequest{
				Method:  r.Method,
				Path:    r.URL.Path,
				Headers: extractHeaders(r.Header),
				Body:    bodyBytes,
			}

			resp := p.impl.OnResponse(req, rec.statusCode, extractHeaders(rec.Header()), rec.body.Bytes())

			if resp.Action == "send_response" {
				for k, v := range resp.Headers {
					w.Header().Set(k, v)
				}
				status := resp.StatusCode
				if status == 0 {
					status = rec.statusCode
				}
				w.WriteHeader(status)
				w.Write(resp.Body)
				return
			}

			// Apply modified headers to response
			for k, v := range resp.Headers {
				w.Header().Set(k, v)
			}

			// Write the original response
			if !rec.headerWritten {
				w.WriteHeader(rec.statusCode)
			}
			w.Write(rec.body.Bytes())
		})
	}
}

// Kill stops the plugin process.
func (p *GoPlugin) Kill() {
	if p.client != nil {
		p.client.Kill()
	}
}

// Served returns the number of requests processed by this plugin.
func (p *GoPlugin) Served() int64 {
	return p.served.Load()
}

// responseRecorder captures the response for post-processing.
type responseRecorder struct {
	http.ResponseWriter
	statusCode    int
	body          bytes.Buffer
	headerWritten bool
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.headerWritten = true
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

// GoPluginChain holds the plugin chain for a route.
type GoPluginChain struct {
	requestPlugins  []*GoPlugin
	responsePlugins []*GoPlugin
	allPlugins      []*GoPlugin
}

// RequestMiddleware returns a combined middleware for all request-phase plugins.
func (c *GoPluginChain) RequestMiddleware() middleware.Middleware {
	if len(c.requestPlugins) == 0 {
		return nil
	}
	return func(next http.Handler) http.Handler {
		handler := next
		// Apply in reverse order so first plugin runs first
		for i := len(c.requestPlugins) - 1; i >= 0; i-- {
			handler = c.requestPlugins[i].RequestMiddleware()(handler)
		}
		return handler
	}
}

// ResponseMiddleware returns a combined middleware for all response-phase plugins.
func (c *GoPluginChain) ResponseMiddleware() middleware.Middleware {
	if len(c.responsePlugins) == 0 {
		return nil
	}
	return func(next http.Handler) http.Handler {
		handler := next
		for i := len(c.responsePlugins) - 1; i >= 0; i-- {
			handler = c.responsePlugins[i].ResponseMiddleware()(handler)
		}
		return handler
	}
}

// Kill stops all plugins in the chain.
func (c *GoPluginChain) Kill() {
	for _, p := range c.allPlugins {
		p.Kill()
	}
}

// Stats returns per-plugin stats.
func (c *GoPluginChain) Stats() map[string]interface{} {
	result := make(map[string]interface{})
	for _, p := range c.allPlugins {
		result[p.name] = map[string]interface{}{
			"phase":  p.phase,
			"served": p.Served(),
		}
	}
	return result
}

// GoPluginByRoute manages per-route Go plugin chains.
type GoPluginByRoute struct {
	byroute.Manager[*GoPluginChain]
	handshakeKey string
	mu           sync.Mutex
}

// NewGoPluginByRoute creates a new per-route Go plugin manager.
func NewGoPluginByRoute(handshakeKey string) *GoPluginByRoute {
	return &GoPluginByRoute{handshakeKey: handshakeKey}
}

// AddRoute starts plugins and adds them for a route.
func (m *GoPluginByRoute) AddRoute(routeID string, plugins []config.GoPluginRouteConfig) error {
	chain := &GoPluginChain{}

	for _, cfg := range plugins {
		if !cfg.Enabled {
			continue
		}

		p, err := newGoPlugin(cfg, m.handshakeKey)
		if err != nil {
			// Clean up already-started plugins
			chain.Kill()
			logging.Error("Failed to start Go plugin",
				zap.String("route", routeID),
				zap.String("plugin", cfg.Name),
				zap.Error(err),
			)
			return err
		}

		chain.allPlugins = append(chain.allPlugins, p)
		switch p.phase {
		case "request":
			chain.requestPlugins = append(chain.requestPlugins, p)
		case "response":
			chain.responsePlugins = append(chain.responsePlugins, p)
		default: // "both"
			chain.requestPlugins = append(chain.requestPlugins, p)
			chain.responsePlugins = append(chain.responsePlugins, p)
		}
	}

	m.Add(routeID, chain)
	return nil
}

// GetChain returns the plugin chain for a route.
func (m *GoPluginByRoute) GetChain(routeID string) *GoPluginChain {
	v, _ := m.Get(routeID)
	return v
}

// Close kills all plugin processes.
func (m *GoPluginByRoute) Close() {
	m.Range(func(routeID string, chain *GoPluginChain) bool {
		chain.Kill()
		return true
	})
}

// Stats returns per-route plugin stats.
func (m *GoPluginByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(c *GoPluginChain) interface{} {
		return c.Stats()
	})
}
