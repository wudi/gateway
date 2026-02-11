package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/listener"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/proxy/tcp"
	"github.com/wudi/gateway/internal/proxy/udp"
	"go.uber.org/zap"
)

// Server wraps the gateway with HTTP server functionality
type Server struct {
	gateway       *Gateway
	manager       *listener.Manager
	adminServer   *http.Server
	config        *config.Config
	configPath    string
	tcpProxy      *tcp.Proxy
	udpProxy      *udp.Proxy
	startTime     time.Time
	reloadHistory []ReloadResult
	draining      atomic.Bool
	drainStart    atomic.Int64 // unix nano timestamp when drain started
}

// NewServer creates a new gateway server.
// configPath is the path to the YAML config file (used for reload).
func NewServer(cfg *config.Config, configPath string) (*Server, error) {
	gw, err := New(cfg)
	if err != nil {
		return nil, err
	}

	s := &Server{
		gateway:    gw,
		manager:    listener.NewManager(),
		config:     cfg,
		configPath: configPath,
		startTime:  time.Now(),
	}

	// Initialize TCP/UDP proxies if needed
	if err := s.initL4Proxies(); err != nil {
		return nil, fmt.Errorf("failed to initialize L4 proxies: %w", err)
	}

	// Initialize listeners
	if err := s.initListeners(); err != nil {
		return nil, fmt.Errorf("failed to initialize listeners: %w", err)
	}

	// Configure admin server if enabled
	if cfg.Admin.Enabled {
		s.adminServer = &http.Server{
			Addr:         fmt.Sprintf(":%d", cfg.Admin.Port),
			Handler:      s.adminHandler(),
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
	}

	return s, nil
}

// initL4Proxies initializes TCP and UDP proxies if routes are configured
func (s *Server) initL4Proxies() error {
	if len(s.config.TCPRoutes) > 0 {
		s.tcpProxy = tcp.NewProxy(tcp.Config{})
		for _, routeCfg := range s.config.TCPRoutes {
			if err := s.tcpProxy.AddRoute(routeCfg); err != nil {
				return fmt.Errorf("failed to add TCP route %s: %w", routeCfg.ID, err)
			}
		}
		logging.Info("Initialized TCP proxy", zap.Int("routes", len(s.config.TCPRoutes)))
	}

	if len(s.config.UDPRoutes) > 0 {
		s.udpProxy = udp.NewProxy(udp.Config{})
		for _, routeCfg := range s.config.UDPRoutes {
			if err := s.udpProxy.AddRoute(routeCfg); err != nil {
				return fmt.Errorf("failed to add UDP route %s: %w", routeCfg.ID, err)
			}
		}
		logging.Info("Initialized UDP proxy", zap.Int("routes", len(s.config.UDPRoutes)))
	}

	return nil
}

// initListeners initializes all listeners from configuration
func (s *Server) initListeners() error {
	cfg := s.config

	// Create listeners from config
	for _, listenerCfg := range cfg.Listeners {
		var l listener.Listener
		var err error

		switch listenerCfg.Protocol {
		case config.ProtocolHTTP:
			l, err = listener.NewHTTPListener(listener.HTTPListenerConfig{
				ID:                listenerCfg.ID,
				Address:           listenerCfg.Address,
				Handler:           s.gateway.Handler(),
				TLS:               listenerCfg.TLS,
				ReadTimeout:       listenerCfg.HTTP.ReadTimeout,
				WriteTimeout:      listenerCfg.HTTP.WriteTimeout,
				IdleTimeout:       listenerCfg.HTTP.IdleTimeout,
				MaxHeaderBytes:    listenerCfg.HTTP.MaxHeaderBytes,
				ReadHeaderTimeout: listenerCfg.HTTP.ReadHeaderTimeout,
				EnableHTTP3:       listenerCfg.HTTP.EnableHTTP3,
			})

		case config.ProtocolTCP:
			if s.tcpProxy == nil {
				return fmt.Errorf("TCP proxy not initialized for listener %s", listenerCfg.ID)
			}
			l, err = listener.NewTCPListener(listener.TCPListenerConfig{
				ID:          listenerCfg.ID,
				Address:     listenerCfg.Address,
				Proxy:       s.tcpProxy,
				TLS:         listenerCfg.TLS,
				SNIRouting:  listenerCfg.TCP.SNIRouting,
				IdleTimeout: listenerCfg.TCP.IdleTimeout,
			})

		case config.ProtocolUDP:
			if s.udpProxy == nil {
				return fmt.Errorf("UDP proxy not initialized for listener %s", listenerCfg.ID)
			}
			l, err = listener.NewUDPListener(listener.UDPListenerConfig{
				ID:      listenerCfg.ID,
				Address: listenerCfg.Address,
				Proxy:   s.udpProxy,
				UDP:     listenerCfg.UDP,
			})

		default:
			return fmt.Errorf("unknown protocol for listener %s: %s", listenerCfg.ID, listenerCfg.Protocol)
		}

		if err != nil {
			return fmt.Errorf("failed to create listener %s: %w", listenerCfg.ID, err)
		}

		if err := s.manager.Add(l); err != nil {
			return fmt.Errorf("failed to add listener %s: %w", listenerCfg.ID, err)
		}
	}

	return nil
}

// Start starts the gateway servers
func (s *Server) Start() error {
	ctx := context.Background()
	errCh := make(chan error, 2)

	// Start all listeners
	go func() {
		if err := s.manager.StartAll(ctx); err != nil {
			errCh <- fmt.Errorf("listener manager error: %w", err)
		}
	}()

	// Start admin server if enabled
	if s.adminServer != nil {
		go func() {
			logging.Info("Starting admin server", zap.Int("port", s.config.Admin.Port))
			if err := s.adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("admin server error: %w", err)
			}
		}()
	}

	// Wait for error or continue
	select {
	case err := <-errCh:
		return err
	case <-time.After(100 * time.Millisecond):
		// Give servers a moment to start
	}

	return nil
}

// Run starts the server and handles graceful shutdown.
// SIGHUP triggers a config reload; SIGINT/SIGTERM triggers shutdown.
func (s *Server) Run() error {
	// Start servers
	if err := s.Start(); err != nil {
		return err
	}

	// Wait for signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	for sig := range quit {
		switch sig {
		case syscall.SIGHUP:
			result := s.ReloadConfig()
			if result.Success {
				logging.Info("Config reloaded successfully",
					zap.Int("changes", len(result.Changes)),
				)
			} else {
				logging.Error("Config reload failed",
					zap.String("error", result.Error),
				)
			}
		default:
			logging.Info("Shutting down gracefully...")
			timeout := s.config.Shutdown.Timeout
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			return s.Shutdown(timeout)
		}
	}

	return nil
}

// Shutdown gracefully shuts down the servers
func (s *Server) Shutdown(timeout time.Duration) error {
	// Mark as draining — readiness checks will return 503
	s.draining.Store(true)
	s.drainStart.Store(time.Now().UnixNano())
	logging.Info("Server entering drain mode")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Drain delay: wait before stopping listeners so load balancers
	// (e.g. Kubernetes) can remove this instance from service endpoints.
	drainDelay := s.config.Shutdown.DrainDelay
	if drainDelay > 0 {
		logging.Info("Waiting for drain delay", zap.Duration("delay", drainDelay))
		select {
		case <-time.After(drainDelay):
		case <-ctx.Done():
			logging.Warn("Drain delay interrupted by shutdown timeout")
		}
	}

	// Shutdown admin server first
	if s.adminServer != nil {
		if err := s.adminServer.Shutdown(ctx); err != nil {
			logging.Error("Admin server shutdown error", zap.Error(err))
		}
	}

	// Shutdown all listeners
	if err := s.manager.StopAll(ctx); err != nil {
		logging.Error("Listener manager shutdown error", zap.Error(err))
	}

	// Close L4 proxies
	if s.tcpProxy != nil {
		s.tcpProxy.Close()
	}
	if s.udpProxy != nil {
		s.udpProxy.Close()
	}

	// Close gateway
	if err := s.gateway.Close(); err != nil {
		logging.Error("Gateway close error", zap.Error(err))
		return err
	}

	logging.Info("Server shutdown complete")
	return nil
}

// Drain initiates the drain process without shutting down the server.
// Readiness checks will return 503 once draining is active.
func (s *Server) Drain() {
	s.draining.Store(true)
	s.drainStart.Store(time.Now().UnixNano())
	logging.Info("Server drain initiated via admin API")
}

// IsDraining returns whether the server is in drain mode.
func (s *Server) IsDraining() bool {
	return s.draining.Load()
}

// ReloadConfig loads a new config from the config path and performs a hot reload.
func (s *Server) ReloadConfig() ReloadResult {
	if s.configPath == "" {
		return ReloadResult{
			Timestamp: time.Now(),
			Error:     "no config path configured",
		}
	}

	loader := config.NewLoader()
	newCfg, err := loader.Load(s.configPath)
	if err != nil {
		result := ReloadResult{
			Timestamp: time.Now(),
			Error:     fmt.Sprintf("config load failed: %v", err),
		}
		s.reloadHistory = appendReloadHistory(s.reloadHistory, result)
		return result
	}

	result := s.gateway.Reload(newCfg)

	// Reconcile listeners (new/removed/TLS changes)
	if result.Success {
		s.reconcileListeners(newCfg)
		s.config = newCfg
	}

	s.reloadHistory = appendReloadHistory(s.reloadHistory, result)
	return result
}

// reconcileListeners adjusts listeners after a config reload.
// It stops removed listeners, starts new ones, and reloads TLS certs on existing ones.
func (s *Server) reconcileListeners(newCfg *config.Config) {
	oldIDs := make(map[string]bool)
	for _, l := range s.config.Listeners {
		oldIDs[l.ID] = true
	}
	newIDs := make(map[string]bool)
	for _, l := range newCfg.Listeners {
		newIDs[l.ID] = true
	}

	// Stop removed listeners
	for id := range oldIDs {
		if !newIDs[id] {
			if l, ok := s.manager.Get(id); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := l.Stop(ctx); err != nil {
					logging.Error("Failed to stop removed listener", zap.String("id", id), zap.Error(err))
				}
				cancel()
				s.manager.Remove(id)
				logging.Info("Removed listener", zap.String("id", id))
			}
		}
	}

	// Start new listeners
	for _, listenerCfg := range newCfg.Listeners {
		if oldIDs[listenerCfg.ID] {
			continue // existing listener, skip
		}
		if listenerCfg.Protocol != config.ProtocolHTTP {
			continue // only handle HTTP listeners during reload
		}
		l, err := listener.NewHTTPListener(listener.HTTPListenerConfig{
			ID:                listenerCfg.ID,
			Address:           listenerCfg.Address,
			Handler:           s.gateway.Handler(),
			TLS:               listenerCfg.TLS,
			ReadTimeout:       listenerCfg.HTTP.ReadTimeout,
			WriteTimeout:      listenerCfg.HTTP.WriteTimeout,
			IdleTimeout:       listenerCfg.HTTP.IdleTimeout,
			MaxHeaderBytes:    listenerCfg.HTTP.MaxHeaderBytes,
			ReadHeaderTimeout: listenerCfg.HTTP.ReadHeaderTimeout,
			EnableHTTP3:       listenerCfg.HTTP.EnableHTTP3,
		})
		if err != nil {
			logging.Error("Failed to create new listener", zap.String("id", listenerCfg.ID), zap.Error(err))
			continue
		}
		if err := s.manager.Add(l); err != nil {
			logging.Error("Failed to add new listener", zap.String("id", listenerCfg.ID), zap.Error(err))
			continue
		}
		if err := l.Start(context.Background()); err != nil {
			logging.Error("Failed to start new listener", zap.String("id", listenerCfg.ID), zap.Error(err))
			continue
		}
		logging.Info("Started new listener", zap.String("id", listenerCfg.ID), zap.String("address", listenerCfg.Address))
	}
}

// appendReloadHistory appends a result and keeps last 50 entries.
func appendReloadHistory(history []ReloadResult, result ReloadResult) []ReloadResult {
	history = append(history, result)
	if len(history) > 50 {
		history = history[len(history)-50:]
	}
	return history
}

// adminHandler creates the admin API handler
func (s *Server) adminHandler() http.Handler {
	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/healthz", s.handleHealth)

	// Ready check endpoint
	mux.HandleFunc("/ready", s.handleReady)
	mux.HandleFunc("/readyz", s.handleReady)

	// Stats endpoint
	mux.HandleFunc("/stats", s.handleStats)

	// Routes endpoint
	mux.HandleFunc("/routes", s.handleRoutes)

	// Registry status
	mux.HandleFunc("/registry", s.handleRegistry)

	// Backends health
	mux.HandleFunc("/backends", s.handleBackends)

	// Listeners endpoint
	mux.HandleFunc("/listeners", s.handleListeners)

	// Circuit breaker status
	mux.HandleFunc("/circuit-breakers", s.handleCircuitBreakers)

	// Cache stats
	mux.HandleFunc("/cache", s.handleCache)

	// Retry metrics
	mux.HandleFunc("/retries", s.handleRetries)

	// Feature 5: Prometheus metrics endpoint
	metricsPath := "/metrics"
	if s.config.Admin.Metrics.Path != "" {
		metricsPath = s.config.Admin.Metrics.Path
	}
	if s.config.Admin.Metrics.Enabled {
		mux.HandleFunc(metricsPath, s.handleMetrics)
	}

	// Rules engine status
	mux.HandleFunc("/rules", s.handleRules)

	// Protocol translators status
	mux.HandleFunc("/protocol-translators", s.handleProtocolTranslators)

	// Traffic shaping stats
	mux.HandleFunc("/traffic-shaping", s.handleTrafficShaping)

	// Mirror stats
	mux.HandleFunc("/mirrors", s.handleMirrors)

	// Traffic splits (A/B testing / canary)
	mux.HandleFunc("/traffic-splits", s.handleTrafficSplits)

	// Tracing status
	mux.HandleFunc("/tracing", s.handleTracing)

	// Rate limits status
	mux.HandleFunc("/rate-limits", s.handleRateLimits)

	// WAF status
	mux.HandleFunc("/waf", s.handleWAF)

	// Reload endpoints
	mux.HandleFunc("/reload", s.handleReload)
	mux.HandleFunc("/reload/status", s.handleReloadStatus)

	// Load balancers
	mux.HandleFunc("/load-balancers", s.handleLoadBalancers)

	// GraphQL stats
	mux.HandleFunc("/graphql", s.handleGraphQL)

	// Coalesce stats
	mux.HandleFunc("/coalesce", s.handleCoalesce)

	// Adaptive concurrency
	mux.HandleFunc("/adaptive-concurrency", s.handleAdaptiveConcurrency)

	// External auth stats
	mux.HandleFunc("/ext-auth", s.handleExtAuth)

	// Versioning stats
	mux.HandleFunc("/versioning", s.handleVersioning)

	// Access log configs
	mux.HandleFunc("/access-log", s.handleAccessLog)

	// OpenAPI validation stats
	mux.HandleFunc("/openapi", s.handleOpenAPI)

	// Timeout policies
	mux.HandleFunc("/timeouts", s.handleTimeouts)

	// Error pages
	mux.HandleFunc("/error-pages", s.handleErrorPages)

	// Nonces (replay prevention)
	mux.HandleFunc("/nonces", s.handleNonces)

	// CSRF protection
	mux.HandleFunc("/csrf", s.handleCSRF)

	// Outlier detection
	mux.HandleFunc("/outlier-detection", s.handleOutlierDetection)

	// Geo filtering
	mux.HandleFunc("/geo", s.handleGeo)

	// Idempotency keys
	mux.HandleFunc("/idempotency", s.handleIdempotency)

	// Backend signing
	mux.HandleFunc("/signing", s.handleSigning)

	// Compression
	mux.HandleFunc("/compression", s.handleCompression)

	// Request decompression
	mux.HandleFunc("/decompression", s.handleDecompression)

	// Response limits
	mux.HandleFunc("/response-limits", s.handleResponseLimits)

	// Security headers
	mux.HandleFunc("/security-headers", s.handleSecurityHeaders)

	// Maintenance mode
	mux.HandleFunc("/maintenance", s.handleMaintenance)
	mux.HandleFunc("/maintenance/", s.handleMaintenanceAction)

	// Trusted proxies / real IP
	mux.HandleFunc("/trusted-proxies", s.handleTrustedProxies)

	// Drain / graceful shutdown
	mux.HandleFunc("/drain", s.handleDrain)

	// Webhooks
	mux.HandleFunc("/webhooks", s.handleWebhooks)

	// Transport pool
	mux.HandleFunc("/transport", s.handleTransport)

	// Upstreams
	mux.HandleFunc("/upstreams", s.handleUpstreams)

	// Canary deployments
	mux.HandleFunc("/canary", s.handleCanary)
	mux.HandleFunc("/canary/", s.handleCanaryAction)

	// Aggregated dashboard
	mux.HandleFunc("/dashboard", s.handleDashboard)

	// Feature 14: API Key management endpoints
	if s.gateway.GetAPIKeyAuth() != nil {
		mux.HandleFunc("/admin/keys", s.handleAdminKeys)
	}

	return mux
}

// handleHealth handles health check requests with dependency checks
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	checks := make(map[string]interface{})
	allHealthy := true

	// Backend health check
	stats := s.gateway.GetStats()
	backendsOK := stats.HealthyRoutes > 0 || stats.Routes == 0
	checks["backends"] = map[string]interface{}{
		"status":         boolStatus(backendsOK),
		"total_routes":   stats.Routes,
		"healthy_routes": stats.HealthyRoutes,
	}
	if !backendsOK {
		allHealthy = false
	}

	// Redis health check (if configured)
	if s.gateway.redisClient != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		err := s.gateway.redisClient.Ping(ctx).Err()
		redisOK := err == nil
		redisStatus := map[string]interface{}{
			"status": boolStatus(redisOK),
		}
		if err != nil {
			redisStatus["error"] = err.Error()
			allHealthy = false
		}
		checks["redis"] = redisStatus
	}

	// Tracer health
	if s.gateway.tracer != nil {
		checks["tracing"] = map[string]interface{}{
			"status": "ok",
		}
	}

	status := http.StatusOK
	statusStr := "ok"
	if !allHealthy {
		status = http.StatusServiceUnavailable
		statusStr = "degraded"
	}

	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    statusStr,
		"timestamp": time.Now().Format(time.RFC3339),
		"uptime":    time.Since(s.startTime).String(),
		"checks":    checks,
	})
}

// handleReady handles readiness check requests with configurable checks
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	stats := s.gateway.GetStats()
	readyCfg := s.config.Admin.Readiness

	w.Header().Set("Content-Type", "application/json")

	ready := true
	reasons := []string{}

	// Check draining state — draining instances are not ready
	if s.draining.Load() {
		ready = false
		reasons = append(reasons, "server is draining")
	}

	// Check healthy backends threshold
	minHealthy := readyCfg.MinHealthyBackends
	if minHealthy <= 0 {
		minHealthy = 1
	}
	if stats.Routes > 0 && stats.HealthyRoutes < minHealthy {
		ready = false
		reasons = append(reasons, fmt.Sprintf("need %d healthy routes, have %d", minHealthy, stats.HealthyRoutes))
	}

	// Check Redis if required
	if readyCfg.RequireRedis && s.gateway.redisClient != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.gateway.redisClient.Ping(ctx).Err(); err != nil {
			ready = false
			reasons = append(reasons, "redis unavailable: "+err.Error())
		}
	}

	response := map[string]interface{}{
		"routes":         stats.Routes,
		"healthy_routes": stats.HealthyRoutes,
		"listeners":      s.manager.Count(),
	}

	if ready {
		w.WriteHeader(http.StatusOK)
		response["status"] = "ready"
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		response["status"] = "not_ready"
		response["reasons"] = reasons
	}

	json.NewEncoder(w).Encode(response)
}

// handleStats handles stats requests
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	stats := s.gateway.GetStats()

	// Add listener count
	response := map[string]interface{}{
		"routes":         stats.Routes,
		"healthy_routes": stats.HealthyRoutes,
		"backends":       stats.Backends,
		"listeners":      s.manager.Count(),
	}

	// Add TCP/UDP stats if available
	if s.tcpProxy != nil {
		response["tcp_routes"] = len(s.config.TCPRoutes)
	}
	if s.udpProxy != nil {
		response["udp_routes"] = len(s.config.UDPRoutes)
		response["udp_sessions"] = s.udpProxy.SessionCount()
	}

	json.NewEncoder(w).Encode(response)
}

// handleRoutes handles routes listing
func (s *Server) handleRoutes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	routes := s.gateway.GetRouter().GetRoutes()

	type routeInfo struct {
		ID         string   `json:"id"`
		Path       string   `json:"path"`
		PathPrefix bool     `json:"path_prefix"`
		Backends   int      `json:"backends"`
		Methods    []string `json:"methods,omitempty"`
		Domains    []string `json:"domains,omitempty"`
		Headers    int      `json:"header_matchers,omitempty"`
		Query      int      `json:"query_matchers,omitempty"`
		Echo       bool     `json:"echo,omitempty"`
	}

	result := make([]routeInfo, 0, len(routes))
	for _, route := range routes {
		info := routeInfo{
			ID:         route.ID,
			Path:       route.Path,
			PathPrefix: route.PathPrefix,
			Backends:   len(route.Backends),
			Echo:       route.Echo,
			Domains:    route.MatchCfg.Domains,
			Headers:    len(route.MatchCfg.Headers),
			Query:      len(route.MatchCfg.Query),
		}

		if route.Methods != nil {
			for method := range route.Methods {
				info.Methods = append(info.Methods, method)
			}
		}

		result = append(result, info)
	}

	json.NewEncoder(w).Encode(result)
}

// handleRegistry handles registry status
func (s *Server) handleRegistry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	json.NewEncoder(w).Encode(map[string]interface{}{
		"type": s.config.Registry.Type,
	})
}

// handleBackends handles backend health status
func (s *Server) handleBackends(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	checker := s.gateway.GetHealthChecker()
	results := checker.GetAllStatus()

	type backendCheckConfig struct {
		Method         string   `json:"method"`
		Path           string   `json:"path"`
		Interval       string   `json:"interval"`
		Timeout        string   `json:"timeout"`
		HealthyAfter   int      `json:"healthy_after"`
		UnhealthyAfter int      `json:"unhealthy_after"`
		ExpectedStatus []string `json:"expected_status"`
	}

	type backendStatus struct {
		URL       string             `json:"url"`
		Status    string             `json:"status"`
		Latency   string             `json:"latency,omitempty"`
		LastCheck string             `json:"last_check,omitempty"`
		Error     string             `json:"error,omitempty"`
		Config    backendCheckConfig `json:"config"`
	}

	backends := make([]backendStatus, 0, len(results))
	for _, result := range results {
		bs := backendStatus{
			URL:       result.URL,
			Status:    string(result.Status),
			Latency:   result.Latency.String(),
			LastCheck: result.Timestamp.Format(time.RFC3339),
		}
		if result.Error != nil {
			bs.Error = result.Error.Error()
		}
		if bcfg, ok := checker.GetBackendConfig(result.URL); ok {
			bs.Config.Method = bcfg.Method
			bs.Config.Path = bcfg.HealthPath
			bs.Config.Interval = bcfg.Interval.String()
			bs.Config.Timeout = bcfg.Timeout.String()
			bs.Config.HealthyAfter = bcfg.HealthyAfter
			bs.Config.UnhealthyAfter = bcfg.UnhealthyAfter
			for _, sr := range bcfg.ExpectedStatus {
				if sr.Lo == sr.Hi {
					bs.Config.ExpectedStatus = append(bs.Config.ExpectedStatus, fmt.Sprintf("%d", sr.Lo))
				} else {
					bs.Config.ExpectedStatus = append(bs.Config.ExpectedStatus, fmt.Sprintf("%d-%d", sr.Lo, sr.Hi))
				}
			}
		}
		backends = append(backends, bs)
	}

	json.NewEncoder(w).Encode(backends)
}

// handleListeners handles listeners listing
func (s *Server) handleListeners(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	listenerIDs := s.manager.List()

	type listenerInfo struct {
		ID       string `json:"id"`
		Protocol string `json:"protocol"`
		Address  string `json:"address"`
		HTTP3    bool   `json:"http3,omitempty"`
	}

	result := make([]listenerInfo, 0, len(listenerIDs))
	for _, id := range listenerIDs {
		if l, ok := s.manager.Get(id); ok {
			info := listenerInfo{
				ID:       l.ID(),
				Protocol: l.Protocol(),
				Address:  l.Addr(),
			}
			if hl, ok := l.(*listener.HTTPListener); ok {
				info.HTTP3 = hl.HTTP3Enabled()
			}
			result = append(result, info)
		}
	}

	json.NewEncoder(w).Encode(result)
}

// handleCircuitBreakers handles circuit breaker status requests
func (s *Server) handleCircuitBreakers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	snapshots := s.gateway.GetCircuitBreakers().Snapshots()
	json.NewEncoder(w).Encode(snapshots)
}

// handleCache handles cache stats requests
func (s *Server) handleCache(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.gateway.GetCaches().Stats()
	json.NewEncoder(w).Encode(stats)
}

// handleRetries handles retry metrics requests
func (s *Server) handleRetries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	metrics := s.gateway.GetRetryMetrics()
	result := make(map[string]interface{}, len(metrics))
	for routeID, m := range metrics {
		result[routeID] = m.Snapshot()
	}
	json.NewEncoder(w).Encode(result)
}

// handleRules handles rules engine status requests
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	result := make(map[string]interface{})

	// Global rules
	if globalEngine := s.gateway.GetGlobalRules(); globalEngine != nil {
		result["global"] = map[string]interface{}{
			"request_rules":  globalEngine.RequestRuleInfos(),
			"response_rules": globalEngine.ResponseRuleInfos(),
			"metrics":        globalEngine.GetMetrics(),
		}
	}

	// Per-route rules
	routeStats := s.gateway.GetRouteRules().Stats()
	if len(routeStats) > 0 {
		result["routes"] = routeStats
	}

	json.NewEncoder(w).Encode(result)
}

// handleMetrics handles Prometheus metrics requests (Feature 5)
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.gateway.GetMetricsCollector().Handler().ServeHTTP(w, r)
}

// handleAdminKeys handles API key management requests (Feature 14)
func (s *Server) handleAdminKeys(w http.ResponseWriter, r *http.Request) {
	s.gateway.GetAPIKeyAuth().HandleAdminKeys(w, r)
}

// handleTrafficShaping handles traffic shaping stats requests
func (s *Server) handleTrafficShaping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	result := make(map[string]interface{})

	if throttleStats := s.gateway.GetThrottlers().Stats(); len(throttleStats) > 0 {
		result["throttle"] = throttleStats
	}
	if bwStats := s.gateway.GetBandwidthLimiters().Stats(); len(bwStats) > 0 {
		result["bandwidth"] = bwStats
	}
	if pa := s.gateway.GetPriorityAdmitter(); pa != nil {
		result["priority"] = pa.Snapshot()
	}
	if fiStats := s.gateway.GetFaultInjectors().Stats(); len(fiStats) > 0 {
		result["fault_injection"] = fiStats
	}
	if acStats := s.gateway.GetAdaptiveLimiters().Stats(); len(acStats) > 0 {
		result["adaptive_concurrency"] = acStats
	}

	json.NewEncoder(w).Encode(result)
}

// handleMirrors handles mirror stats requests
func (s *Server) handleMirrors(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.gateway.GetMirrors().Stats()
	json.NewEncoder(w).Encode(stats)
}

// handleTrafficSplits handles traffic split / A/B testing stats requests
func (s *Server) handleTrafficSplits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.gateway.GetTrafficSplitStats()
	json.NewEncoder(w).Encode(stats)
}

// handleRateLimits handles rate limiter status requests
func (s *Server) handleRateLimits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	rl := s.gateway.GetRateLimiters()
	routeIDs := rl.RouteIDs()
	result := make(map[string]interface{})
	for _, id := range routeIDs {
		info := map[string]interface{}{}
		if dl := rl.GetDistributedLimiter(id); dl != nil {
			info["mode"] = "distributed"
			info["algorithm"] = "sliding_window"
		} else if sw := rl.GetSlidingWindowLimiter(id); sw != nil {
			info["mode"] = "local"
			info["algorithm"] = "sliding_window"
		} else {
			info["mode"] = "local"
			info["algorithm"] = "token_bucket"
		}
		result[id] = info
	}
	json.NewEncoder(w).Encode(result)
}

// handleTracing handles tracing status requests
func (s *Server) handleTracing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	tracer := s.gateway.GetTracer()
	if tracer != nil {
		json.NewEncoder(w).Encode(tracer.Status())
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{"enabled": false})
	}
}

// handleDashboard returns aggregated stats from all feature managers
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	stats := s.gateway.GetStats()
	dashboard := map[string]interface{}{
		"uptime":    time.Since(s.startTime).String(),
		"timestamp": time.Now().Format(time.RFC3339),
		"routes": map[string]interface{}{
			"total":   stats.Routes,
			"healthy": stats.HealthyRoutes,
		},
		"listeners": s.manager.Count(),
	}

	// Drain status
	if s.draining.Load() {
		drainInfo := map[string]interface{}{"draining": true}
		if ts := s.drainStart.Load(); ts > 0 {
			startTime := time.Unix(0, ts)
			drainInfo["drain_start"] = startTime.Format(time.RFC3339)
			drainInfo["drain_duration"] = time.Since(startTime).String()
		}
		dashboard["drain"] = drainInfo
	}

	// Aggregate feature stats
	featureStats := s.gateway.FeatureStats()
	if len(featureStats) > 0 {
		dashboard["features"] = featureStats
	}

	// Circuit breakers
	if snapshots := s.gateway.GetCircuitBreakers().Snapshots(); len(snapshots) > 0 {
		dashboard["circuit_breakers"] = snapshots
	}

	// Cache stats
	if cacheStats := s.gateway.GetCaches().Stats(); len(cacheStats) > 0 {
		dashboard["cache"] = cacheStats
	}

	// Retry metrics
	retryMetrics := s.gateway.GetRetryMetrics()
	if len(retryMetrics) > 0 {
		retries := make(map[string]interface{}, len(retryMetrics))
		for id, m := range retryMetrics {
			retries[id] = m.Snapshot()
		}
		dashboard["retries"] = retries
	}

	// Traffic splits
	if splits := s.gateway.GetTrafficSplitStats(); len(splits) > 0 {
		dashboard["traffic_splits"] = splits
	}

	// WAF
	if wafStats := s.gateway.GetWAFHandlers().Stats(); len(wafStats) > 0 {
		dashboard["waf"] = wafStats
	}

	// Coalesce
	if coalesceStats := s.gateway.GetCoalescers().Stats(); len(coalesceStats) > 0 {
		dashboard["coalesce"] = coalesceStats
	}

	// Adaptive concurrency
	if acStats := s.gateway.GetAdaptiveLimiters().Stats(); len(acStats) > 0 {
		dashboard["adaptive_concurrency"] = acStats
	}

	// Canary deployments
	if canaryStats := s.gateway.GetCanaryControllers().Stats(); len(canaryStats) > 0 {
		dashboard["canary"] = canaryStats
	}

	// Ext auth
	if extAuthStats := s.gateway.GetExtAuths().Stats(); len(extAuthStats) > 0 {
		dashboard["ext_auth"] = extAuthStats
	}

	// Versioning
	if versioningStats := s.gateway.GetVersioners().Stats(); len(versioningStats) > 0 {
		dashboard["versioning"] = versioningStats
	}

	// Access log
	if alStats := s.gateway.GetAccessLogConfigs().Stats(); len(alStats) > 0 {
		dashboard["access_log"] = alStats
	}

	// OpenAPI validation
	if openapiStats := s.gateway.GetOpenAPIValidators().Stats(); len(openapiStats) > 0 {
		dashboard["openapi"] = openapiStats
	}

	// Timeout policies
	if timeoutStats := s.gateway.GetTimeoutConfigs().Stats(); len(timeoutStats) > 0 {
		dashboard["timeouts"] = timeoutStats
	}

	// Error pages
	if epStats := s.gateway.GetErrorPages().Stats(); len(epStats) > 0 {
		dashboard["error_pages"] = epStats
	}

	// Nonces (replay prevention)
	if nonceStats := s.gateway.GetNonceCheckers().Stats(); len(nonceStats) > 0 {
		dashboard["nonces"] = nonceStats
	}

	// CSRF protection
	if csrfStats := s.gateway.GetCSRFProtectors().Stats(); len(csrfStats) > 0 {
		dashboard["csrf"] = csrfStats
	}

	// Outlier detection
	if odStats := s.gateway.GetOutlierDetectors().Stats(); len(odStats) > 0 {
		dashboard["outlier_detection"] = odStats
	}

	// Idempotency
	if idemStats := s.gateway.GetIdempotencyHandlers().Stats(); len(idemStats) > 0 {
		dashboard["idempotency"] = idemStats
	}

	// Backend signing
	if sigStats := s.gateway.GetBackendSigners().Stats(); len(sigStats) > 0 {
		dashboard["backend_signing"] = sigStats
	}

	// Compression
	if compStats := s.gateway.GetCompressors().Stats(); len(compStats) > 0 {
		dashboard["compression"] = compStats
	}

	// Request decompression
	if decompStats := s.gateway.GetDecompressors().Stats(); len(decompStats) > 0 {
		dashboard["request_decompression"] = decompStats
	}

	// Response limits
	if rlStats := s.gateway.GetResponseLimiters().Stats(); len(rlStats) > 0 {
		dashboard["response_limits"] = rlStats
	}

	// Security headers
	if shStats := s.gateway.GetSecurityHeaders().Stats(); len(shStats) > 0 {
		dashboard["security_headers"] = shStats
	}

	// Maintenance mode
	if maintStats := s.gateway.GetMaintenanceHandlers().Stats(); len(maintStats) > 0 {
		dashboard["maintenance"] = maintStats
	}

	// Trusted proxies
	if ext := s.gateway.GetRealIPExtractor(); ext != nil {
		dashboard["trusted_proxies"] = ext.Stats()
	}

	// Webhooks
	if d := s.gateway.GetWebhookDispatcher(); d != nil {
		dashboard["webhooks"] = d.Stats()
	}

	// Upstreams
	if upstreams := s.gateway.GetUpstreams(); len(upstreams) > 0 {
		dashboard["upstreams"] = upstreams
	}

	// Transport pool
	pool := s.gateway.GetTransportPool()
	dashboard["transport"] = pool.DefaultConfig()

	// Tracing
	if tracer := s.gateway.GetTracer(); tracer != nil {
		dashboard["tracing"] = tracer.Status()
	}

	// TCP/UDP stats
	if s.tcpProxy != nil {
		dashboard["tcp_routes"] = len(s.config.TCPRoutes)
	}
	if s.udpProxy != nil {
		dashboard["udp_routes"] = len(s.config.UDPRoutes)
		dashboard["udp_sessions"] = s.udpProxy.SessionCount()
	}

	json.NewEncoder(w).Encode(dashboard)
}

// boolStatus returns "ok" or "fail" for a boolean.
func boolStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}

// handleWAF handles WAF status requests
func (s *Server) handleWAF(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.gateway.GetWAFHandlers().Stats()
	json.NewEncoder(w).Encode(stats)
}

// handleGraphQL handles GraphQL stats requests
func (s *Server) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.gateway.GetGraphQLParsers().Stats()
	json.NewEncoder(w).Encode(stats)
}

// handleAdaptiveConcurrency handles adaptive concurrency stats requests
func (s *Server) handleAdaptiveConcurrency(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.gateway.GetAdaptiveLimiters().Stats()
	json.NewEncoder(w).Encode(stats)
}

// handleCoalesce handles coalesce stats requests
func (s *Server) handleCoalesce(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.gateway.GetCoalescers().Stats()
	json.NewEncoder(w).Encode(stats)
}

// handleLoadBalancers handles load balancer info requests
func (s *Server) handleLoadBalancers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	info := s.gateway.GetLoadBalancerInfo()
	json.NewEncoder(w).Encode(info)
}

// handleReload handles config reload requests (POST only).
func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	result := s.ReloadConfig()
	json.NewEncoder(w).Encode(result)
}

// handleReloadStatus returns the reload history.
func (s *Server) handleReloadStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.reloadHistory)
}

// handleProtocolTranslators handles protocol translator stats requests
func (s *Server) handleProtocolTranslators(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.gateway.GetTranslators().Stats()
	json.NewEncoder(w).Encode(stats)
}

// handleExtAuth handles ext auth stats requests
func (s *Server) handleExtAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetExtAuths().Stats())
}

// handleVersioning handles versioning stats requests
func (s *Server) handleVersioning(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetVersioners().Stats())
}

// handleAccessLog handles access log config status requests.
func (s *Server) handleAccessLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetAccessLogConfigs().Stats())
}

// handleOpenAPI handles OpenAPI validation stats requests.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetOpenAPIValidators().Stats())
}

// handleTimeouts handles timeout policy stats requests.
func (s *Server) handleTimeouts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetTimeoutConfigs().Stats())
}

// handleNonces handles nonce replay prevention stats requests.
func (s *Server) handleNonces(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetNonceCheckers().Stats())
}

// handleCSRF handles CSRF protection stats requests.
func (s *Server) handleCSRF(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetCSRFProtectors().Stats())
}

// handleErrorPages handles error pages stats requests.
func (s *Server) handleErrorPages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetErrorPages().Stats())
}

// handleOutlierDetection handles outlier detection stats requests.
func (s *Server) handleOutlierDetection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetOutlierDetectors().Stats())
}

// handleGeo handles geo filtering stats requests.
func (s *Server) handleGeo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetGeoFilters().Stats())
}

// handleIdempotency handles idempotency key stats requests.
func (s *Server) handleIdempotency(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetIdempotencyHandlers().Stats())
}

// handleSigning handles backend signing stats requests.
func (s *Server) handleSigning(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetBackendSigners().Stats())
}

// handleCompression handles compression stats requests.
func (s *Server) handleCompression(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetCompressors().Stats())
}

// handleDecompression handles request decompression stats requests.
func (s *Server) handleDecompression(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetDecompressors().Stats())
}

// handleResponseLimits handles response limit stats requests.
func (s *Server) handleResponseLimits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetResponseLimiters().Stats())
}

// handleSecurityHeaders handles security headers stats requests.
func (s *Server) handleSecurityHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetSecurityHeaders().Stats())
}

// handleMaintenance handles maintenance mode status requests.
func (s *Server) handleMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetMaintenanceHandlers().Stats())
}

// handleMaintenanceAction handles runtime enable/disable of maintenance mode.
// POST /maintenance/{routeID}/enable or POST /maintenance/{routeID}/disable
func (s *Server) handleMaintenanceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse /maintenance/{routeID}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/maintenance/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		http.Error(w, `{"error":"expected /maintenance/{route}/{action}"}`, http.StatusBadRequest)
		return
	}
	routeID := parts[0]
	action := parts[1]

	cm := s.gateway.GetMaintenanceHandlers().GetMaintenance(routeID)
	if cm == nil {
		http.Error(w, `{"error":"route not found or maintenance not configured"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	switch action {
	case "enable":
		cm.Enable()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "enabled",
			"route":   routeID,
		})
	case "disable":
		cm.Disable()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "disabled",
			"route":   routeID,
		})
	default:
		http.Error(w, `{"error":"action must be 'enable' or 'disable'"}`, http.StatusBadRequest)
	}
}

// handleTrustedProxies handles trusted proxies stats requests.
func (s *Server) handleTrustedProxies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	extractor := s.gateway.GetRealIPExtractor()
	if extractor != nil {
		json.NewEncoder(w).Encode(extractor.Stats())
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{"enabled": false})
	}
}

// handleDrain handles drain status (GET) and drain initiation (POST).
func (s *Server) handleDrain(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		response := map[string]interface{}{
			"draining": s.draining.Load(),
		}
		if ts := s.drainStart.Load(); ts > 0 {
			startTime := time.Unix(0, ts)
			response["drain_start"] = startTime.Format(time.RFC3339)
			response["drain_duration"] = time.Since(startTime).String()
		}
		json.NewEncoder(w).Encode(response)

	case http.MethodPost:
		if s.draining.Load() {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "already_draining",
				"message": "server is already in drain mode",
			})
			return
		}
		s.Drain()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "draining",
			"message": "drain mode activated, readiness checks will return 503",
		})

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// handleWebhooks handles webhook dispatcher stats requests.
func (s *Server) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	d := s.gateway.GetWebhookDispatcher()
	if d != nil {
		json.NewEncoder(w).Encode(d.Stats())
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{"enabled": false})
	}
}

// handleUpstreams returns configured upstream pools.
func (s *Server) handleUpstreams(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	upstreams := s.gateway.GetUpstreams()
	if upstreams == nil {
		upstreams = make(map[string]config.UpstreamConfig)
	}
	json.NewEncoder(w).Encode(upstreams)
}

// handleTransport returns transport pool configuration.
func (s *Server) handleTransport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	pool := s.gateway.GetTransportPool()
	result := map[string]interface{}{
		"default":   pool.DefaultConfig(),
		"upstreams": make(map[string]interface{}),
	}

	// Show per-upstream transport overrides from config
	for name, us := range s.gateway.GetUpstreams() {
		if us.Transport == (config.TransportConfig{}) {
			continue
		}
		result["upstreams"].(map[string]interface{})[name] = us.Transport
	}

	json.NewEncoder(w).Encode(result)
}

// handleCanary lists all canary deployments with status.
func (s *Server) handleCanary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.gateway.GetCanaryControllers().Stats()
	json.NewEncoder(w).Encode(stats)
}

// handleCanaryAction handles POST /canary/{route}/{action}.
func (s *Server) handleCanaryAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse /canary/{route}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/canary/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "usage: POST /canary/{route}/{action}", http.StatusBadRequest)
		return
	}
	routeID := parts[0]
	actionName := parts[1]

	ctrl := s.gateway.GetCanaryControllers().GetController(routeID)
	if ctrl == nil {
		http.Error(w, fmt.Sprintf("no canary controller for route %q", routeID), http.StatusNotFound)
		return
	}

	var err error
	switch actionName {
	case "start":
		err = ctrl.Start()
	case "pause":
		err = ctrl.Pause()
	case "resume":
		err = ctrl.Resume()
	case "promote":
		err = ctrl.Promote()
	case "rollback":
		err = ctrl.Rollback()
	default:
		http.Error(w, fmt.Sprintf("unknown action %q (valid: start, pause, resume, promote, rollback)", actionName), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": actionName, "route": routeID})
}

// Gateway returns the underlying gateway
func (s *Server) Gateway() *Gateway {
	return s.gateway
}

// ListenerManager returns the listener manager
func (s *Server) ListenerManager() *listener.Manager {
	return s.manager
}

// GetTCPProxy returns the TCP proxy
func (s *Server) GetTCPProxy() *tcp.Proxy {
	return s.tcpProxy
}

// GetUDPProxy returns the UDP proxy
func (s *Server) GetUDPProxy() *udp.Proxy {
	return s.udpProxy
}
