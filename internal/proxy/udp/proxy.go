package udp

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/loadbalancer"
)

// Proxy handles UDP proxying
type Proxy struct {
	routes   map[string]*Route
	sessions *SessionManager
	mu       sync.RWMutex
}

// Route represents a UDP route with backends
type Route struct {
	ID         string
	ListenerID string
	Balancer   loadbalancer.Balancer
}

// Config holds UDP proxy configuration
type Config struct {
	SessionTimeout  time.Duration
	ReadBufferSize  int
	WriteBufferSize int
}

// DefaultConfig provides default UDP proxy settings
var DefaultConfig = Config{
	SessionTimeout:  30 * time.Second,
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

// NewProxy creates a new UDP proxy
func NewProxy(cfg Config) *Proxy {
	if cfg.SessionTimeout == 0 {
		cfg.SessionTimeout = DefaultConfig.SessionTimeout
	}

	return &Proxy{
		routes: make(map[string]*Route),
		sessions: NewSessionManager(SessionManagerConfig{
			SessionTimeout: cfg.SessionTimeout,
		}),
	}
}

// AddRoute adds a UDP route
func (p *Proxy) AddRoute(routeCfg config.UDPRouteConfig) error {
	// Parse backends
	var backends []*loadbalancer.Backend
	for _, b := range routeCfg.Backends {
		// Parse the URL to extract host:port
		addr, err := parseUDPBackendURL(b.URL)
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

	route := &Route{
		ID:         routeCfg.ID,
		ListenerID: routeCfg.Listener,
		Balancer:   loadbalancer.NewRoundRobin(backends),
	}

	p.mu.Lock()
	p.routes[routeCfg.ID] = route
	p.mu.Unlock()

	log.Printf("Added UDP route %s -> %d backends", routeCfg.ID, len(backends))
	return nil
}

// RemoveRoute removes a UDP route
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

// Serve handles incoming UDP datagrams on a connection
func (p *Proxy) Serve(ctx context.Context, conn *net.UDPConn, listenerID string, bufferSize int) error {
	if bufferSize == 0 {
		bufferSize = DefaultConfig.ReadBufferSize
	}

	buf := make([]byte, bufferSize)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Set read deadline to allow periodic context checking
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))

		n, clientAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return fmt.Errorf("UDP read error: %w", err)
		}

		// Handle datagram
		go p.handleDatagram(ctx, conn, clientAddr, buf[:n], listenerID)
	}
}

// handleDatagram processes a single UDP datagram
func (p *Proxy) handleDatagram(ctx context.Context, clientConn *net.UDPConn, clientAddr *net.UDPAddr, data []byte, listenerID string) {
	// Find route for this listener
	route := p.getRouteForListener(listenerID)
	if route == nil {
		log.Printf("No UDP route for listener %s", listenerID)
		return
	}

	// Check for existing session
	session, exists := p.sessions.Get(clientAddr.String())
	if !exists {
		// Get backend from load balancer
		backend := route.Balancer.Next()
		if backend == nil {
			log.Printf("No healthy backends for UDP route %s", route.ID)
			return
		}

		// Create new session
		var err error
		session, err = p.sessions.Create(clientAddr, backend.URL)
		if err != nil {
			log.Printf("Failed to create UDP session: %v", err)
			return
		}

		// Start response receiver for this session
		go p.receiveResponses(ctx, clientConn, session)
	}

	// Forward datagram to backend
	_, err := session.BackendConn.Write(data)
	if err != nil {
		log.Printf("Failed to forward UDP datagram: %v", err)
		p.sessions.Remove(clientAddr.String())
	}
}

// receiveResponses reads responses from backend and forwards to client
func (p *Proxy) receiveResponses(ctx context.Context, clientConn *net.UDPConn, session *Session) {
	buf := make([]byte, DefaultConfig.ReadBufferSize)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Set read deadline
		session.BackendConn.SetReadDeadline(time.Now().Add(1 * time.Second))

		n, err := session.BackendConn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Check if session is still valid
				if time.Since(session.GetLastActive()) > p.sessions.sessionTimeout {
					p.sessions.Remove(session.ClientAddr.String())
					return
				}
				continue
			}
			log.Printf("UDP backend read error: %v", err)
			p.sessions.Remove(session.ClientAddr.String())
			return
		}

		// Update session activity
		session.UpdateLastActive()

		// Forward response to client
		_, err = clientConn.WriteToUDP(buf[:n], session.ClientAddr)
		if err != nil {
			log.Printf("UDP client write error: %v", err)
		}
	}
}

// getRouteForListener returns the first route for a listener
// In the future, this could support more sophisticated routing
func (p *Proxy) getRouteForListener(listenerID string) *Route {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, route := range p.routes {
		if route.ListenerID == listenerID {
			return route
		}
	}
	return nil
}

// Close closes the proxy and releases resources
func (p *Proxy) Close() error {
	return p.sessions.Close()
}

// GetBalancer returns the load balancer for a route
func (r *Route) GetBalancer() loadbalancer.Balancer {
	return r.Balancer
}

// SessionCount returns the number of active sessions
func (p *Proxy) SessionCount() int {
	return p.sessions.Count()
}

// parseUDPBackendURL parses a UDP backend URL and returns host:port
func parseUDPBackendURL(rawURL string) (string, error) {
	// Handle udp:// prefix
	if strings.HasPrefix(rawURL, "udp://") {
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

	return "", fmt.Errorf("invalid UDP URL: %s", rawURL)
}
