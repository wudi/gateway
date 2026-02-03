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
)

// Server wraps the gateway with HTTP server functionality
type Server struct {
	gateway     *Gateway
	manager     *listener.Manager
	adminServer *http.Server
	config      *config.Config
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

// initListeners initializes all listeners from configuration
func (s *Server) initListeners() error {
	cfg := s.config

	// Backward compatibility: if no listeners defined, create from server config
	if len(cfg.Listeners) == 0 {
		httpListener, err := listener.NewHTTPListener(listener.HTTPListenerConfig{
			ID:           "default-http",
			Address:      fmt.Sprintf(":%d", cfg.Server.Port),
			Handler:      s.gateway.Handler(),
			ReadTimeout:  cfg.Server.ReadTimeout,
			WriteTimeout: cfg.Server.WriteTimeout,
			IdleTimeout:  cfg.Server.IdleTimeout,
		})
		if err != nil {
			return fmt.Errorf("failed to create default HTTP listener: %w", err)
		}
		return s.manager.Add(httpListener)
	}

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
			tcpProxy := s.gateway.GetTCPProxy()
			if tcpProxy == nil {
				return fmt.Errorf("TCP proxy not initialized for listener %s", listenerCfg.ID)
			}
			l, err = listener.NewTCPListener(listener.TCPListenerConfig{
				ID:          listenerCfg.ID,
				Address:     listenerCfg.Address,
				Proxy:       tcpProxy,
				TLS:         listenerCfg.TLS,
				SNIRouting:  listenerCfg.TCP.SNIRouting,
				IdleTimeout: listenerCfg.TCP.IdleTimeout,
			})

		case config.ProtocolUDP:
			udpProxy := s.gateway.GetUDPProxy()
			if udpProxy == nil {
				return fmt.Errorf("UDP proxy not initialized for listener %s", listenerCfg.ID)
			}
			l, err = listener.NewUDPListener(listener.UDPListenerConfig{
				ID:      listenerCfg.ID,
				Address: listenerCfg.Address,
				Proxy:   udpProxy,
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
	if tcpProxy := s.gateway.GetTCPProxy(); tcpProxy != nil {
		response["tcp_routes"] = len(s.config.TCPRoutes)
	}
	if udpProxy := s.gateway.GetUDPProxy(); udpProxy != nil {
		response["udp_routes"] = len(s.config.UDPRoutes)
		response["udp_sessions"] = udpProxy.SessionCount()
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
	}

	result := make([]routeInfo, 0, len(routes))
	for _, route := range routes {
		info := routeInfo{
			ID:         route.ID,
			Path:       route.Path,
			PathPrefix: route.PathPrefix,
			Backends:   len(route.Backends),
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

// Gateway returns the underlying gateway
func (s *Server) Gateway() *Gateway {
	return s.gateway
}

// ListenerManager returns the listener manager
func (s *Server) ListenerManager() *listener.Manager {
	return s.manager
}
