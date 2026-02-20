package wasm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware"
)

// WasmPlugin represents a single compiled WASM plugin with an instance pool.
type WasmPlugin struct {
	name     string
	phase    string
	cfg      config.WasmPluginConfig
	pool     *InstancePool
	timeout  time.Duration
	compiled wazero.CompiledModule

	requestInvocations  atomic.Int64
	responseInvocations atomic.Int64
	errors              atomic.Int64
	timeouts            atomic.Int64
	totalLatencyNs      atomic.Int64
}

// NewPlugin compiles a .wasm file, validates exports, and creates a pool.
func NewPlugin(ctx context.Context, rt wazero.Runtime, cfg config.WasmPluginConfig) (*WasmPlugin, error) {
	wasmBytes, err := os.ReadFile(cfg.Path)
	if err != nil {
		return nil, err
	}

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, err
	}

	phase := cfg.Phase
	if phase == "" {
		phase = "both"
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Millisecond
	}
	poolSize := cfg.PoolSize
	if poolSize <= 0 {
		poolSize = 4
	}

	pool, err := NewInstancePool(ctx, rt, compiled, poolSize)
	if err != nil {
		return nil, err
	}

	return &WasmPlugin{
		name:     cfg.Name,
		phase:    phase,
		cfg:      cfg,
		pool:     pool,
		timeout:  timeout,
		compiled: compiled,
	}, nil
}

// hasExport checks if the compiled module exports a function with the given name.
func hasExport(compiled wazero.CompiledModule, name string) bool {
	for _, exp := range compiled.ExportedFunctions() {
		if exp.ExportNames()[0] == name {
			return true
		}
	}
	return false
}

// callGuest allocates memory in the guest, writes data, calls the exported function,
// and deallocates. Returns the action code.
func (p *WasmPlugin) callGuest(ctx context.Context, mod api.Module, fnName string, data []byte) (int32, error) {
	allocate := mod.ExportedFunction("allocate")
	deallocate := mod.ExportedFunction("deallocate")
	fn := mod.ExportedFunction(fnName)
	if fn == nil {
		return ActionContinue, nil
	}

	var ptr uint64
	var err error

	if allocate != nil && len(data) > 0 {
		results, callErr := allocate.Call(ctx, uint64(len(data)))
		if callErr != nil {
			return ActionContinue, callErr
		}
		if len(results) == 0 || results[0] == 0 {
			return ActionContinue, nil
		}
		ptr = results[0]

		if !mod.Memory().Write(uint32(ptr), data) {
			return ActionContinue, nil
		}
	}

	results, err := fn.Call(ctx, ptr, uint64(len(data)))

	// Deallocate guest memory
	if deallocate != nil && ptr != 0 {
		deallocate.Call(ctx, ptr, uint64(len(data)))
	}

	if err != nil {
		return ActionContinue, err
	}
	if len(results) == 0 {
		return ActionContinue, nil
	}
	return int32(results[0]), nil
}

// RequestMiddleware returns a middleware for the request phase.
func (p *WasmPlugin) RequestMiddleware() middleware.Middleware {
	if p.phase != "request" && p.phase != "both" {
		return nil
	}
	if !hasExport(p.compiled, "on_request") {
		return nil
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			logger := logging.Global()

			ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
			defer cancel()

			mod, err := p.pool.Borrow(ctx)
			if err != nil {
				p.errors.Add(1)
				logger.Error("wasm plugin borrow failed", zap.String("plugin", p.name), zap.Error(err))
				http.Error(w, "wasm plugin error", http.StatusBadGateway)
				return
			}
			defer p.pool.Return(r.Context(), mod)

			// Read request body
			var reqBody []byte
			if r.Body != nil {
				reqBody, err = io.ReadAll(r.Body)
				if err != nil {
					p.errors.Add(1)
					http.Error(w, "failed to read request body", http.StatusBadGateway)
					return
				}
				r.Body.Close()
			}

			// Build host state
			reqHeaders := r.Header.Clone()
			hs := &hostState{
				req:          r,
				reqHeaders:   reqHeaders,
				reqBody:      reqBody,
				pluginConfig: p.cfg.Config,
				routeID:      "", // set from context if available
				scheme:       schemeFromRequest(r),
				logger:       logger,
			}
			ctx = contextWithHostState(ctx, hs)

			// Serialize request context
			rc := RequestContext{
				Method:   r.Method,
				Path:     r.URL.Path,
				Host:     r.Host,
				Scheme:   hs.scheme,
				RouteID:  hs.routeID,
				BodySize: len(reqBody),
				Headers:  flattenHeaders(reqHeaders),
				Config:   p.cfg.Config,
			}
			ctxJSON, _ := json.Marshal(rc)

			p.requestInvocations.Add(1)
			action, err := p.callGuest(ctx, mod, "on_request", ctxJSON)
			p.totalLatencyNs.Add(int64(time.Since(start)))

			if err != nil {
				if ctx.Err() != nil {
					p.timeouts.Add(1)
					http.Error(w, "wasm plugin timeout", http.StatusGatewayTimeout)
					return
				}
				p.errors.Add(1)
				logger.Error("wasm plugin on_request failed", zap.String("plugin", p.name), zap.Error(err))
				http.Error(w, "wasm plugin error", http.StatusBadGateway)
				return
			}

			// Handle early response
			if action == ActionSendResponse && hs.earlyResponse != nil {
				w.WriteHeader(hs.earlyResponse.StatusCode)
				if len(hs.earlyResponse.Body) > 0 {
					w.Write(hs.earlyResponse.Body)
				}
				return
			}

			// Apply header mutations back to request
			r.Header = hs.reqHeaders
			// Apply body mutations
			r.Body = io.NopCloser(bytes.NewReader(hs.reqBody))
			r.ContentLength = int64(len(hs.reqBody))

			next.ServeHTTP(w, r)
		})
	}
}

// ResponseMiddleware returns a middleware for the response phase.
func (p *WasmPlugin) ResponseMiddleware() middleware.Middleware {
	if p.phase != "response" && p.phase != "both" {
		return nil
	}
	if !hasExport(p.compiled, "on_response") {
		return nil
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Buffer the response
			bw := &bufferedResponseWriter{
				header: make(http.Header),
				body:   &bytes.Buffer{},
				code:   http.StatusOK,
			}
			next.ServeHTTP(bw, r)

			start := time.Now()
			logger := logging.Global()

			ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
			defer cancel()

			mod, err := p.pool.Borrow(ctx)
			if err != nil {
				p.errors.Add(1)
				logger.Error("wasm plugin borrow failed", zap.String("plugin", p.name), zap.Error(err))
				// Flush original response
				flushBuffered(w, bw)
				return
			}
			defer p.pool.Return(r.Context(), mod)

			// Build host state
			respHeaders := bw.header.Clone()
			respBody := bw.body.Bytes()
			hs := &hostState{
				req:          r,
				reqHeaders:   r.Header,
				respHeaders:  respHeaders,
				respBody:     respBody,
				pluginConfig: p.cfg.Config,
				routeID:      "",
				scheme:       schemeFromRequest(r),
				logger:       logger,
			}
			ctx = contextWithHostState(ctx, hs)

			// Serialize response context
			respCtx := ResponseContext{
				StatusCode: bw.code,
				BodySize:   len(respBody),
				RouteID:    hs.routeID,
				Headers:    flattenHeaders(respHeaders),
				Config:     p.cfg.Config,
			}
			ctxJSON, _ := json.Marshal(respCtx)

			p.responseInvocations.Add(1)
			action, err := p.callGuest(ctx, mod, "on_response", ctxJSON)
			p.totalLatencyNs.Add(int64(time.Since(start)))

			if err != nil {
				if ctx.Err() != nil {
					p.timeouts.Add(1)
				} else {
					p.errors.Add(1)
				}
				logger.Error("wasm plugin on_response failed", zap.String("plugin", p.name), zap.Error(err))
				// Flush original response on error
				flushBuffered(w, bw)
				return
			}

			// Handle early response override
			if action == ActionSendResponse && hs.earlyResponse != nil {
				w.WriteHeader(hs.earlyResponse.StatusCode)
				if len(hs.earlyResponse.Body) > 0 {
					w.Write(hs.earlyResponse.Body)
				}
				return
			}

			// Apply header mutations
			for k := range w.Header() {
				w.Header().Del(k)
			}
			for k, vals := range hs.respHeaders {
				for _, v := range vals {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(bw.code)
			w.Write(hs.respBody)
		})
	}
}

// Close closes the plugin's instance pool.
func (p *WasmPlugin) Close(ctx context.Context) {
	if p.pool != nil {
		p.pool.Close(ctx)
	}
}

// Stats returns plugin execution statistics.
func (p *WasmPlugin) Stats() map[string]any {
	stats := map[string]any{
		"name":                 p.name,
		"phase":                p.phase,
		"request_invocations":  p.requestInvocations.Load(),
		"response_invocations": p.responseInvocations.Load(),
		"errors":               p.errors.Load(),
		"timeouts":             p.timeouts.Load(),
		"total_latency_ns":     p.totalLatencyNs.Load(),
	}
	if p.pool != nil {
		stats["pool"] = p.pool.Stats()
	}
	return stats
}

// --- WasmPluginChain ---

// WasmPluginChain is an ordered slice of plugins for a route.
type WasmPluginChain struct {
	plugins []*WasmPlugin
}

// RequestMiddleware chains plugins in array order (each wraps next).
func (c *WasmPluginChain) RequestMiddleware() middleware.Middleware {
	var mws []middleware.Middleware
	for _, p := range c.plugins {
		if mw := p.RequestMiddleware(); mw != nil {
			mws = append(mws, mw)
		}
	}
	if len(mws) == 0 {
		return nil
	}
	return func(next http.Handler) http.Handler {
		h := next
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}

// ResponseMiddleware chains plugins in reverse order (onion model).
func (c *WasmPluginChain) ResponseMiddleware() middleware.Middleware {
	var mws []middleware.Middleware
	for i := len(c.plugins) - 1; i >= 0; i-- {
		if mw := c.plugins[i].ResponseMiddleware(); mw != nil {
			mws = append(mws, mw)
		}
	}
	if len(mws) == 0 {
		return nil
	}
	return func(next http.Handler) http.Handler {
		h := next
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}

// Close closes all plugins in the chain.
func (c *WasmPluginChain) Close(ctx context.Context) {
	for _, p := range c.plugins {
		p.Close(ctx)
	}
}

// Stats returns per-plugin stats.
func (c *WasmPluginChain) Stats() []map[string]any {
	var stats []map[string]any
	for _, p := range c.plugins {
		stats = append(stats, p.Stats())
	}
	return stats
}

// --- WasmByRoute manager ---

// WasmByRoute manages per-route WASM plugin chains.
type WasmByRoute struct {
	byroute.Manager[*WasmPluginChain]
	runtime wazero.Runtime
	envMod  wazero.CompiledModule
	wasmCfg config.WasmConfig
}

// NewWasmByRoute creates a new per-route WASM plugin manager.
func NewWasmByRoute(cfg config.WasmConfig) *WasmByRoute {
	return &WasmByRoute{wasmCfg: cfg}
}

// ensureRuntime lazily initializes the shared wazero runtime.
func (m *WasmByRoute) ensureRuntime(ctx context.Context) error {
	if m.runtime != nil {
		return nil
	}

	var rtCfg wazero.RuntimeConfig
	if m.wasmCfg.RuntimeMode == "interpreter" {
		rtCfg = wazero.NewRuntimeConfigInterpreter()
	} else {
		rtCfg = wazero.NewRuntimeConfigCompiler()
	}

	maxPages := m.wasmCfg.MaxMemoryPages
	if maxPages <= 0 {
		maxPages = 256 // 16MB
	}
	rtCfg = rtCfg.WithMemoryLimitPages(uint32(maxPages))

	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)
	envMod, err := registerHostFunctions(rt)
	if err != nil {
		rt.Close(ctx)
		return err
	}

	// Instantiate the env module so guests can import it
	if _, err := rt.InstantiateModule(ctx, envMod, wazero.NewModuleConfig().WithName("env")); err != nil {
		rt.Close(ctx)
		return err
	}

	m.runtime = rt
	m.envMod = envMod
	return nil
}

// AddRoute compiles and pools each plugin in the config slice, creating a chain.
func (m *WasmByRoute) AddRoute(routeID string, cfgs []config.WasmPluginConfig) error {
	ctx := context.Background()
	if err := m.ensureRuntime(ctx); err != nil {
		return err
	}

	var plugins []*WasmPlugin
	for _, cfg := range cfgs {
		if !cfg.Enabled {
			continue
		}
		p, err := NewPlugin(ctx, m.runtime, cfg)
		if err != nil {
			// Clean up already-created plugins
			for _, pp := range plugins {
				pp.Close(ctx)
			}
			return err
		}
		plugins = append(plugins, p)
	}

	if len(plugins) == 0 {
		return nil
	}

	m.Add(routeID, &WasmPluginChain{plugins: plugins})
	return nil
}

// GetChain returns the WASM plugin chain for a route.
func (m *WasmByRoute) GetChain(routeID string) *WasmPluginChain {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route WASM plugin stats.
func (m *WasmByRoute) Stats() map[string]any {
	return byroute.CollectStats(&m.Manager, func(c *WasmPluginChain) any {
		return c.Stats()
	})
}

// Close closes the shared runtime and all plugin chains.
func (m *WasmByRoute) Close(ctx context.Context) {
	m.Range(func(id string, c *WasmPluginChain) bool {
		c.Close(ctx)
		return true
	})
	if m.runtime != nil {
		m.runtime.Close(ctx)
	}
}

// --- Helpers ---

func schemeFromRequest(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func flattenHeaders(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, vals := range h {
		if len(vals) > 0 {
			m[k] = vals[0]
		}
	}
	return m
}

// bufferedResponseWriter captures the response so the WASM plugin can inspect/modify it.
type bufferedResponseWriter struct {
	header      http.Header
	body        *bytes.Buffer
	code        int
	wroteHeader bool
}

func (bw *bufferedResponseWriter) Header() http.Header {
	return bw.header
}

func (bw *bufferedResponseWriter) Write(b []byte) (int, error) {
	if !bw.wroteHeader {
		bw.wroteHeader = true
	}
	return bw.body.Write(b)
}

func (bw *bufferedResponseWriter) WriteHeader(code int) {
	if bw.wroteHeader {
		return
	}
	bw.wroteHeader = true
	bw.code = code
}

func (bw *bufferedResponseWriter) Flush() {}

func flushBuffered(w http.ResponseWriter, bw *bufferedResponseWriter) {
	for k, vals := range bw.header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(bw.code)
	w.Write(bw.body.Bytes())
}
