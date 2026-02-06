package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/listener"
	"github.com/example/gateway/internal/logging"
	"github.com/example/gateway/internal/proxy/tcp"
	"github.com/example/gateway/internal/proxy/udp"
	"go.uber.org/zap"
)

// Server wraps the gateway with HTTP server functionality
type Server struct {
	gateway     *Gateway
	manager     *listener.Manager
	adminServer *http.Server
	config      *config.Config
	tcpProxy    *tcp.Proxy
	udpProxy    *udp.Proxy
	startTime   time.Time
}

// NewServer creates a new gateway server
func NewServer(cfg *config.Config) (*Server, error) {
	gw, err := New(cfg)
	if err != nil {
		return nil, err
	}

	s := &Server{
		gateway:   gw,
		manager:   listener.NewManager(),
		config:    cfg,
		startTime: time.Now(),
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

// Run starts the server and handles graceful shutdown
func (s *Server) Run() error {
	// Start servers
	if err := s.Start(); err != nil {
		return err
	}

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logging.Info("Shutting down gracefully...")

	// Graceful shutdown
	return s.Shutdown(30 * time.Second)
}

// Shutdown gracefully shuts down the servers
func (s *Server) Shutdown(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

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
	}

	result := make([]routeInfo, 0, len(routes))
	for _, route := range routes {
		info := routeInfo{
			ID:         route.ID,
			Path:       route.Path,
			PathPrefix: route.PathPrefix,
			Backends:   len(route.Backends),
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

	results := s.gateway.GetHealthChecker().GetAllStatus()

	type backendStatus struct {
		URL       string `json:"url"`
		Status    string `json:"status"`
		Latency   string `json:"latency,omitempty"`
		LastCheck string `json:"last_check,omitempty"`
		Error     string `json:"error,omitempty"`
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
	}

	result := make([]listenerInfo, 0, len(listenerIDs))
	for _, id := range listenerIDs {
		if l, ok := s.manager.Get(id); ok {
			result = append(result, listenerInfo{
				ID:       l.ID(),
				Protocol: l.Protocol(),
				Address:  l.Addr(),
			})
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
	routeIDs := s.gateway.GetRateLimiters().RouteIDs()
	result := make(map[string]interface{})
	for _, id := range routeIDs {
		info := map[string]interface{}{
			"mode": "local",
		}
		if dl := s.gateway.GetRateLimiters().GetDistributedLimiter(id); dl != nil {
			info["mode"] = "distributed"
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

// handleProtocolTranslators handles protocol translator stats requests
func (s *Server) handleProtocolTranslators(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.gateway.GetTranslators().Stats()
	json.NewEncoder(w).Encode(stats)
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
