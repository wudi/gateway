package tcp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/logging"
	"go.uber.org/zap"
)

// Proxy handles TCP proxying
type Proxy struct {
	routes   map[string]*Route
	connPool *ConnPool
	mu       sync.RWMutex
}

// Route represents a TCP route with backends and matching rules
type Route struct {
	ID         string
	ListenerID string
	Match      config.TCPMatchConfig
	Balancer   loadbalancer.Balancer
	CIDRs      []*net.IPNet
}

// Config holds TCP proxy configuration
type Config struct {
	ConnectTimeout time.Duration
	IdleTimeout    time.Duration
	PoolConfig     ConnPoolConfig
}

// DefaultConfig provides default TCP proxy settings
var DefaultConfig = Config{
	ConnectTimeout: 10 * time.Second,
	IdleTimeout:    5 * time.Minute,
	PoolConfig:     DefaultConnPoolConfig,
}

// NewProxy creates a new TCP proxy
func NewProxy(cfg Config) *Proxy {
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = DefaultConfig.ConnectTimeout
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = DefaultConfig.IdleTimeout
	}

	return &Proxy{
		routes:   make(map[string]*Route),
		connPool: NewConnPool(cfg.PoolConfig),
	}
}

// AddRoute adds a TCP route
func (p *Proxy) AddRoute(routeCfg config.TCPRouteConfig) error {
	// Parse backends
	var backends []*loadbalancer.Backend
	for _, b := range routeCfg.Backends {
		// Parse the URL to extract host:port
		addr, err := parseTCPBackendURL(b.URL)
		if err != nil {
			return fmt.Errorf("invalid backend URL %s: %w", b.URL, err)
		}

		weight := b.Weight
		if weight == 0 {
			weight = 1
		}
		backends = append(backends, &loadbalancer.Backend{
			URL:     addr,
			Weight:  weight,
			Healthy: true,
		})
	}

	// Parse CIDRs
	var cidrs []*net.IPNet
	if len(routeCfg.Match.SourceCIDR) > 0 {
		var err error
		cidrs, err = routeCfg.Match.ParsedSourceCIDRs()
		if err != nil {
			return fmt.Errorf("invalid source CIDR: %w", err)
		}
	}

	route := &Route{
		ID:         routeCfg.ID,
		ListenerID: routeCfg.Listener,
		Match:      routeCfg.Match,
		Balancer:   loadbalancer.NewRoundRobin(backends),
		CIDRs:      cidrs,
	}

	p.mu.Lock()
	p.routes[routeCfg.ID] = route
	p.mu.Unlock()

	logging.Info("added TCP route", zap.String("route", routeCfg.ID), zap.Int("backends", len(backends)))
	return nil
}

// RemoveRoute removes a TCP route
func (p *Proxy) RemoveRoute(id string) {
	p.mu.Lock()
	delete(p.routes, id)
	p.mu.Unlock()
}

// GetRoute returns a route by ID
func (p *Proxy) GetRoute(id string) *Route {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.routes[id]
}

// GetRoutesForListener returns all routes for a specific listener
func (p *Proxy) GetRoutesForListener(listenerID string) []*Route {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var routes []*Route
	for _, route := range p.routes {
		if route.ListenerID == listenerID {
			routes = append(routes, route)
		}
	}
	return routes
}

// Handle processes an incoming TCP connection
func (p *Proxy) Handle(ctx context.Context, conn net.Conn, listenerID string, sniRouting bool) error {
	defer conn.Close()

	// Get client address for CIDR matching
	clientAddr := conn.RemoteAddr().(*net.TCPAddr).IP

	// Wrap connection for SNI parsing if needed
	buffConn := NewBufferedConn(conn)

	// Extract SNI if enabled
	var sni string
	if sniRouting {
		var err error
		sni, err = ParseClientHelloSNI(buffConn)
		if err != nil && err != ErrNotTLS && err != ErrNoSNI {
			logging.Warn("failed to parse SNI", zap.Error(err))
		}
	}

	// Find matching route
	route := p.matchRoute(listenerID, sni, clientAddr)
	if route == nil {
		logging.Warn("no matching route", zap.String("listener", listenerID), zap.String("sni", sni), zap.String("client", clientAddr.String()))
		return fmt.Errorf("no matching route")
	}

	// Get backend from load balancer
	backend := route.Balancer.Next()
	if backend == nil {
		logging.Warn("no healthy backends for route", zap.String("route", route.ID))
		return fmt.Errorf("no healthy backends")
	}

	// Connect to backend
	backendConn, err := p.connPool.Get(backend.URL)
	if err != nil {
		logging.Error("failed to connect to backend", zap.String("backend", backend.URL), zap.Error(err))
		route.Balancer.MarkUnhealthy(backend.URL)
		return fmt.Errorf("failed to connect to backend: %w", err)
	}
	defer backendConn.Close()

	// Bidirectional copy
	return p.pipe(ctx, buffConn, backendConn)
}

// matchRoute finds the first matching route for the given criteria
func (p *Proxy) matchRoute(listenerID, sni string, clientIP net.IP) *Route {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, route := range p.routes {
		// Must match listener
		if route.ListenerID != listenerID {
			continue
		}

		// Check SNI match if specified
		if len(route.Match.SNI) > 0 {
			if !MatchSNI(sni, route.Match.SNI) {
				continue
			}
		}

		// Check CIDR match if specified
		if len(route.CIDRs) > 0 {
			matched := false
			for _, cidr := range route.CIDRs {
				if cidr.Contains(clientIP) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		return route
	}

	return nil
}

// pipe performs bidirectional copy between two connections
func (p *Proxy) pipe(ctx context.Context, client, backend net.Conn) error {
	errCh := make(chan error, 2)

	// Client -> Backend
	go func() {
		_, err := io.Copy(backend, client)
		// Close write side to signal EOF
		if tcpConn, ok := backend.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
		errCh <- err
	}()

	// Backend -> Client
	go func() {
		_, err := io.Copy(client, backend)
		// Close write side to signal EOF
		if tcpConn, ok := client.(*net.TCPConn); ok {
			tcpConn.CloseWrite()
		}
		errCh <- err
	}()

	// Wait for either direction to complete or context cancellation
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		// Wait for the other direction with a timeout
		select {
		case <-time.After(5 * time.Second):
		case <-errCh:
		}
		return err
	}
}

// Close closes the proxy and releases resources
func (p *Proxy) Close() error {
	return p.connPool.Close()
}

// GetBalancer returns the load balancer for a route
func (r *Route) GetBalancer() loadbalancer.Balancer {
	return r.Balancer
}

// parseTCPBackendURL parses a TCP backend URL and returns host:port
func parseTCPBackendURL(rawURL string) (string, error) {
	// Handle tcp:// prefix
	if strings.HasPrefix(rawURL, "tcp://") {
		u, err := url.Parse(rawURL)
		if err != nil {
			return "", err
		}
		return u.Host, nil
	}

	// If it already looks like host:port, return as-is
	if strings.Contains(rawURL, ":") {
		return rawURL, nil
	}

	return "", fmt.Errorf("invalid TCP URL: %s", rawURL)
}
