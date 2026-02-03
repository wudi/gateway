package udp

import (
	"net"
	"sync"
	"time"
)

// Session represents a UDP session between a client and backend
type Session struct {
	ClientAddr  *net.UDPAddr
	BackendAddr string
	BackendConn *net.UDPConn
	LastActive  time.Time
	mu          sync.Mutex
}

// UpdateLastActive updates the last activity timestamp
func (s *Session) UpdateLastActive() {
	s.mu.Lock()
	s.LastActive = time.Now()
	s.mu.Unlock()
}

// GetLastActive returns the last activity timestamp
func (s *Session) GetLastActive() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastActive
}

// SessionManager manages UDP sessions
type SessionManager struct {
	sessions       map[string]*Session
	mu             sync.RWMutex
	sessionTimeout time.Duration
	cleanupTicker  *time.Ticker
	closeCh        chan struct{}
}

// SessionManagerConfig holds session manager configuration
type SessionManagerConfig struct {
	SessionTimeout time.Duration
	CleanupInterval time.Duration
}

// DefaultSessionManagerConfig provides default settings
var DefaultSessionManagerConfig = SessionManagerConfig{
	SessionTimeout:  30 * time.Second,
	CleanupInterval: 10 * time.Second,
}

// NewSessionManager creates a new session manager
func NewSessionManager(cfg SessionManagerConfig) *SessionManager {
	if cfg.SessionTimeout == 0 {
		cfg.SessionTimeout = DefaultSessionManagerConfig.SessionTimeout
	}
	if cfg.CleanupInterval == 0 {
		cfg.CleanupInterval = DefaultSessionManagerConfig.CleanupInterval
	}

	sm := &SessionManager{
		sessions:       make(map[string]*Session),
		sessionTimeout: cfg.SessionTimeout,
		cleanupTicker:  time.NewTicker(cfg.CleanupInterval),
		closeCh:        make(chan struct{}),
	}

	// Start cleanup goroutine
	go sm.cleanupLoop()

	return sm
}

// Get retrieves a session by client address
func (sm *SessionManager) Get(clientAddr string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[clientAddr]
	if ok {
		session.UpdateLastActive()
	}
	return session, ok
}

// Create creates a new session
func (sm *SessionManager) Create(clientAddr *net.UDPAddr, backendAddr string) (*Session, error) {
	// Dial backend
	backendUDPAddr, err := net.ResolveUDPAddr("udp", backendAddr)
	if err != nil {
		return nil, err
	}

	backendConn, err := net.DialUDP("udp", nil, backendUDPAddr)
	if err != nil {
		return nil, err
	}

	session := &Session{
		ClientAddr:  clientAddr,
		BackendAddr: backendAddr,
		BackendConn: backendConn,
		LastActive:  time.Now(),
	}

	sm.mu.Lock()
	sm.sessions[clientAddr.String()] = session
	sm.mu.Unlock()

	return session, nil
}

// Remove removes a session
func (sm *SessionManager) Remove(clientAddr string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, ok := sm.sessions[clientAddr]; ok {
		session.BackendConn.Close()
		delete(sm.sessions, clientAddr)
	}
}

// cleanupLoop periodically removes stale sessions
func (sm *SessionManager) cleanupLoop() {
	for {
		select {
		case <-sm.closeCh:
			return
		case <-sm.cleanupTicker.C:
			sm.cleanupStale()
		}
	}
}

// cleanupStale removes sessions that have exceeded the timeout
func (sm *SessionManager) cleanupStale() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	for addr, session := range sm.sessions {
		if now.Sub(session.GetLastActive()) > sm.sessionTimeout {
			session.BackendConn.Close()
			delete(sm.sessions, addr)
		}
	}
}

// Close closes the session manager and all sessions
func (sm *SessionManager) Close() error {
	close(sm.closeCh)
	sm.cleanupTicker.Stop()

	sm.mu.Lock()
	defer sm.mu.Unlock()

	for _, session := range sm.sessions {
		session.BackendConn.Close()
	}
	sm.sessions = make(map[string]*Session)

	return nil
}

// Count returns the number of active sessions
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// Stats returns session statistics
func (sm *SessionManager) Stats() map[string]string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	stats := make(map[string]string)
	for clientAddr, session := range sm.sessions {
		stats[clientAddr] = session.BackendAddr
	}
	return stats
}
