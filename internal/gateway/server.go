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
)

// Server wraps the gateway with HTTP server functionality
type Server struct {
	gateway     *Gateway
	mainServer  *http.Server
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
		config:  cfg,
	}

	// Configure main server
	s.mainServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      gw.Handler(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
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

// Start starts the gateway servers
func (s *Server) Start() error {
	errCh := make(chan error, 2)

	// Start main server
	go func() {
		log.Printf("Starting gateway on port %d", s.config.Server.Port)
		if err := s.mainServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("main server error: %w", err)
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

	// Wait for error or interrupt
	select {
	case err := <-errCh:
		return err
	default:
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

	// Shutdown main server
	if err := s.mainServer.Shutdown(ctx); err != nil {
		log.Printf("Main server shutdown error: %v", err)
		return err
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
		})
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "not_ready",
			"routes":         stats.Routes,
			"healthy_routes": stats.HealthyRoutes,
		})
	}
}

// handleStats handles stats requests
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.gateway.GetStats())
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

// Gateway returns the underlying gateway
func (s *Server) Gateway() *Gateway {
	return s.gateway
}
