package tcp

import (
	"net"
	"sync"
	"time"
)

// pooledConn represents a connection in the pool
type pooledConn struct {
	conn      net.Conn
	createdAt time.Time
	lastUsed  time.Time
}

// ConnPool manages a pool of TCP connections to backends
type ConnPool struct {
	conns       map[string][]*pooledConn
	mu          sync.Mutex
	maxIdle     int
	maxIdleTime time.Duration
	maxLifetime time.Duration
	dialTimeout time.Duration
	closeCh     chan struct{}
}

// ConnPoolConfig configures the connection pool
type ConnPoolConfig struct {
	MaxIdle     int
	MaxIdleTime time.Duration
	MaxLifetime time.Duration
	DialTimeout time.Duration
}

// DefaultConnPoolConfig provides default pool settings
var DefaultConnPoolConfig = ConnPoolConfig{
	MaxIdle:     10,
	MaxIdleTime: 90 * time.Second,
	MaxLifetime: 10 * time.Minute,
	DialTimeout: 10 * time.Second,
}

// NewConnPool creates a new connection pool
func NewConnPool(cfg ConnPoolConfig) *ConnPool {
	if cfg.MaxIdle == 0 {
		cfg.MaxIdle = DefaultConnPoolConfig.MaxIdle
	}
	if cfg.MaxIdleTime == 0 {
		cfg.MaxIdleTime = DefaultConnPoolConfig.MaxIdleTime
	}
	if cfg.MaxLifetime == 0 {
		cfg.MaxLifetime = DefaultConnPoolConfig.MaxLifetime
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = DefaultConnPoolConfig.DialTimeout
	}

	pool := &ConnPool{
		conns:       make(map[string][]*pooledConn),
		maxIdle:     cfg.MaxIdle,
		maxIdleTime: cfg.MaxIdleTime,
		maxLifetime: cfg.MaxLifetime,
		dialTimeout: cfg.DialTimeout,
		closeCh:     make(chan struct{}),
	}

	// Start cleanup goroutine
	go pool.cleanup()

	return pool
}

// Get retrieves a connection from the pool or creates a new one
func (p *ConnPool) Get(addr string) (net.Conn, error) {
	p.mu.Lock()

	// Try to get an existing connection
	if conns, ok := p.conns[addr]; ok && len(conns) > 0 {
		// Get the last (most recently used) connection
		pc := conns[len(conns)-1]
		p.conns[addr] = conns[:len(conns)-1]
		p.mu.Unlock()

		// Check if connection is still valid
		if p.isValid(pc) {
			return pc.conn, nil
		}
		pc.conn.Close()
	} else {
		p.mu.Unlock()
	}

	// Create new connection
	return net.DialTimeout("tcp", addr, p.dialTimeout)
}

// Put returns a connection to the pool
func (p *ConnPool) Put(addr string, conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if pool is closed
	select {
	case <-p.closeCh:
		conn.Close()
		return
	default:
	}

	// Check if we have room
	conns := p.conns[addr]
	if len(conns) >= p.maxIdle {
		conn.Close()
		return
	}

	pc := &pooledConn{
		conn:      conn,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}

	p.conns[addr] = append(conns, pc)
}

// isValid checks if a pooled connection is still usable
func (p *ConnPool) isValid(pc *pooledConn) bool {
	now := time.Now()

	// Check idle time
	if now.Sub(pc.lastUsed) > p.maxIdleTime {
		return false
	}

	// Check lifetime
	if now.Sub(pc.createdAt) > p.maxLifetime {
		return false
	}

	// Check if connection is still open by setting deadline
	pc.conn.SetReadDeadline(time.Now().Add(1 * time.Millisecond))
	one := make([]byte, 1)
	_, err := pc.conn.Read(one)
	pc.conn.SetReadDeadline(time.Time{})

	// If we get EOF or another error (except timeout), connection is dead
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return true
		}
		return false
	}

	return true
}

// cleanup periodically removes stale connections
func (p *ConnPool) cleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.closeCh:
			return
		case <-ticker.C:
			p.removeStale()
		}
	}
}

// removeStale removes expired connections
func (p *ConnPool) removeStale() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()

	for addr, conns := range p.conns {
		var valid []*pooledConn
		for _, pc := range conns {
			if now.Sub(pc.lastUsed) > p.maxIdleTime || now.Sub(pc.createdAt) > p.maxLifetime {
				pc.conn.Close()
			} else {
				valid = append(valid, pc)
			}
		}
		if len(valid) > 0 {
			p.conns[addr] = valid
		} else {
			delete(p.conns, addr)
		}
	}
}

// Close closes the pool and all connections
func (p *ConnPool) Close() error {
	close(p.closeCh)

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conns := range p.conns {
		for _, pc := range conns {
			pc.conn.Close()
		}
	}
	p.conns = make(map[string][]*pooledConn)

	return nil
}

// Stats returns pool statistics
func (p *ConnPool) Stats() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()

	stats := make(map[string]int)
	for addr, conns := range p.conns {
		stats[addr] = len(conns)
	}
	return stats
}
