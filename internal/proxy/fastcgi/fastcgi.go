package fastcgi

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/yookoala/gofast"
)

// Handler proxies HTTP requests to a FastCGI backend.
type Handler struct {
	routeID      string
	address      string
	network      string
	documentRoot string
	scriptName   string
	index        string
	handler      http.Handler
	totalReqs    atomic.Int64
	totalErrors  atomic.Int64
}

// New creates a FastCGI handler from config.
func New(routeID string, cfg config.FastCGIConfig) (*Handler, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("fastcgi: address is required")
	}
	if cfg.DocumentRoot == "" {
		return nil, fmt.Errorf("fastcgi: document_root is required")
	}

	network := cfg.Network
	if network == "" {
		network = detectNetwork(cfg.Address)
	}

	connTimeout := cfg.ConnTimeout
	if connTimeout == 0 {
		connTimeout = 5 * time.Second
	}
	readTimeout := cfg.ReadTimeout
	if readTimeout == 0 {
		readTimeout = 30 * time.Second
	}
	poolSize := cfg.PoolSize
	if poolSize == 0 {
		poolSize = 8
	}
	index := cfg.Index
	if index == "" {
		index = "index.php"
	}

	connFactory := gofast.SimpleConnFactory(network, cfg.Address)
	pool := gofast.NewClientPool(
		gofast.SimpleClientFactory(connFactory),
		uint(poolSize),
		connTimeout,
	)

	// Build middleware chain for session handling.
	var endpointMW gofast.Middleware
	if cfg.ScriptName != "" {
		// Single-entry-point mode (Laravel/Symfony).
		endpointMW = gofast.NewFileEndpoint(cfg.DocumentRoot + cfg.ScriptName)
	} else {
		// Filesystem mode (classic PHP).
		endpointMW = gofast.NewPHPFS(cfg.DocumentRoot)
	}

	// Extra params middleware injects DOCUMENT_ROOT and user-defined params.
	extraParams := buildExtraParamsMiddleware(cfg.DocumentRoot, cfg.Params)

	sess := gofast.Chain(
		endpointMW,
		gofast.BasicParamsMap,
		gofast.MapHeader,
		extraParams,
	)(gofast.BasicSession)

	h := gofast.NewHandler(sess, pool.CreateClient)

	return &Handler{
		routeID:      routeID,
		address:      cfg.Address,
		network:      network,
		documentRoot: cfg.DocumentRoot,
		scriptName:   cfg.ScriptName,
		index:        index,
		handler:      h,
	}, nil
}

// ServeHTTP proxies the request to the FastCGI backend.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.totalReqs.Add(1)
	h.handler.ServeHTTP(w, r)
}

// Stats returns handler statistics.
func (h *Handler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"address":       h.address,
		"network":       h.network,
		"document_root": h.documentRoot,
		"script_name":   h.scriptName,
		"total_requests": h.totalReqs.Load(),
		"total_errors":   h.totalErrors.Load(),
	}
}

// detectNetwork guesses the network type from the address string.
func detectNetwork(addr string) string {
	if strings.HasPrefix(addr, "/") || strings.HasSuffix(addr, ".sock") {
		return "unix"
	}
	return "tcp"
}

// buildExtraParamsMiddleware returns a gofast.Middleware that injects additional
// CGI parameters: DOCUMENT_ROOT, REDIRECT_STATUS, GATEWAY_INTERFACE, and any
// user-defined params.
func buildExtraParamsMiddleware(docRoot string, params map[string]string) gofast.Middleware {
	return func(inner gofast.SessionHandler) gofast.SessionHandler {
		return func(client gofast.Client, req *gofast.Request) (*gofast.ResponsePipe, error) {
			if req.Params == nil {
				req.Params = make(map[string]string)
			}
			req.Params["DOCUMENT_ROOT"] = docRoot
			req.Params["REDIRECT_STATUS"] = "200"
			req.Params["GATEWAY_INTERFACE"] = "CGI/1.1"
			for k, v := range params {
				req.Params[k] = v
			}
			return inner(client, req)
		}
	}
}

// FastCGIByRoute manages per-route FastCGI handlers.
type FastCGIByRoute struct {
	byroute.Manager[*Handler]
}

// NewFastCGIByRoute creates a new per-route FastCGI manager.
func NewFastCGIByRoute() *FastCGIByRoute {
	return &FastCGIByRoute{}
}

// AddRoute creates and registers a FastCGI handler for a route.
func (m *FastCGIByRoute) AddRoute(routeID string, cfg config.FastCGIConfig) error {
	h, err := New(routeID, cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, h)
	return nil
}

// GetHandler returns the FastCGI handler for a route, or nil.
func (m *FastCGIByRoute) GetHandler(routeID string) *Handler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route FastCGI stats.
func (m *FastCGIByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *Handler) interface{} { return h.Stats() })
}
