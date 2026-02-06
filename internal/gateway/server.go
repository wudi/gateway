package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/listener"
	"github.com/example/gateway/internal/proxy/tcp"
	"github.com/example/gateway/internal/proxy/udp"
)

// Server wraps the gateway with HTTP server functionality
type Server struct {
	gateway     *Gateway
	manager     *listener.Manager
	adminServer *http.Server
	config      *config.Config
	tcpProxy    *tcp.Proxy
	udpProxy    *udp.Proxy
}

// NewServer creates a new gateway server
func NewServer(cfg *config.Config) (*Server, error) {
	gw, err := New(cfg)
	if err != nil {
		return nil, err
	}

	s := &Server{
		gateway: gw,
		manager: listener.NewManager(),
		config:  cfg,
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
		log.Printf("Initialized TCP proxy with %d routes", len(s.config.TCPRoutes))
	}

	if len(s.config.UDPRoutes) > 0 {
		s.udpProxy = udp.NewProxy(udp.Config{})
		for _, routeCfg := range s.config.UDPRoutes {
			if err := s.udpProxy.AddRoute(routeCfg); err != nil {
				return fmt.Errorf("failed to add UDP route %s: %w", routeCfg.ID, err)
			}
		}
		log.Printf("Initialized UDP proxy with %d routes", len(s.config.UDPRoutes))
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
			log.Printf("Starting admin server on port %d", s.config.Admin.Port)
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

	log.Println("Shutting down gracefully...")

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
			log.Printf("Admin server shutdown error: %v", err)
		}
	}

	// Shutdown all listeners
	if err := s.manager.StopAll(ctx); err != nil {
		log.Printf("Listener manager shutdown error: %v", err)
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
		log.Printf("Gateway close error: %v", err)
		return err
	}

	log.Println("Server shutdown complete")
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

	// Feature 14: API Key management endpoints
	if s.gateway.GetAPIKeyAuth() != nil {
		mux.HandleFunc("/admin/keys", s.handleAdminKeys)
	}

	return mux
}

// handleHealth handles health check requests
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// handleReady handles readiness check requests
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	stats := s.gateway.GetStats()

	w.Header().Set("Content-Type", "application/json")

	// Check if we have at least one healthy route
	if stats.HealthyRoutes > 0 || stats.Routes == 0 {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ready",
			"routes":         stats.Routes,
			"healthy_routes": stats.HealthyRoutes,
			"listeners":      s.manager.Count(),
		})
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "not_ready",
			"routes":         stats.Routes,
			"healthy_routes": stats.HealthyRoutes,
			"listeners":      s.manager.Count(),
		})
	}
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
	s.gateway.GetMetricsCollector().WritePrometheus(w)
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

	json.NewEncoder(w).Encode(result)
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
