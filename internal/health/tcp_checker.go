package health

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// TCPBackend represents a TCP backend to health check
type TCPBackend struct {
	Address        string
	Timeout        time.Duration
	Interval       time.Duration
	HealthyAfter   int
	UnhealthyAfter int
}

// TCPChecker performs TCP health checks on backends
type TCPChecker struct {
	backends        map[string]*tcpBackendState
	mu              sync.RWMutex
	defaultTimeout  time.Duration
	defaultInterval time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
	onChange        func(address string, status Status)
}

type tcpBackendState struct {
	backend         TCPBackend
	status          Status
	lastCheck       time.Time
	lastError       error
	consecutivePass int
	consecutiveFail int
	latency         time.Duration
}

// TCPCheckerConfig holds TCP health checker configuration
type TCPCheckerConfig struct {
	DefaultTimeout  time.Duration
	DefaultInterval time.Duration
	OnChange        func(address string, status Status)
}

// DefaultTCPCheckerConfig provides default TCP health checker settings
var DefaultTCPCheckerConfig = TCPCheckerConfig{
	DefaultTimeout:  5 * time.Second,
	DefaultInterval: 10 * time.Second,
}

// NewTCPChecker creates a new TCP health checker
func NewTCPChecker(cfg TCPCheckerConfig) *TCPChecker {
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = DefaultTCPCheckerConfig.DefaultTimeout
	}
	if cfg.DefaultInterval == 0 {
		cfg.DefaultInterval = DefaultTCPCheckerConfig.DefaultInterval
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &TCPChecker{
		backends:        make(map[string]*tcpBackendState),
		defaultTimeout:  cfg.DefaultTimeout,
		defaultInterval: cfg.DefaultInterval,
		ctx:             ctx,
		cancel:          cancel,
		onChange:        cfg.OnChange,
	}
}

// AddBackend adds a TCP backend for health checking
func (c *TCPChecker) AddBackend(b TCPBackend) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if b.Timeout == 0 {
		b.Timeout = c.defaultTimeout
	}
	if b.Interval == 0 {
		b.Interval = c.defaultInterval
	}
	if b.HealthyAfter == 0 {
		b.HealthyAfter = 2
	}
	if b.UnhealthyAfter == 0 {
		b.UnhealthyAfter = 3
	}

	state := &tcpBackendState{
		backend: b,
		status:  StatusUnknown,
	}

	c.backends[b.Address] = state

	// Start checking this backend
	go c.checkLoop(b.Address)
}

// RemoveBackend removes a TCP backend from health checking
func (c *TCPChecker) RemoveBackend(address string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.backends, address)
}

// GetStatus returns the health status of a TCP backend
func (c *TCPChecker) GetStatus(address string) Status {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if state, ok := c.backends[address]; ok {
		return state.status
	}
	return StatusUnknown
}

// GetAllStatus returns the health status of all TCP backends
func (c *TCPChecker) GetAllStatus() map[string]CheckResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	results := make(map[string]CheckResult)
	for addr, state := range c.backends {
		results[addr] = CheckResult{
			URL:       addr,
			Status:    state.status,
			Latency:   state.latency,
			Error:     state.lastError,
			Timestamp: state.lastCheck,
		}
	}
	return results
}

// IsHealthy returns true if the TCP backend is healthy
func (c *TCPChecker) IsHealthy(address string) bool {
	return c.GetStatus(address) == StatusHealthy
}

// checkLoop runs periodic TCP health checks for a backend
func (c *TCPChecker) checkLoop(address string) {
	// Initial check
	c.check(address)

	c.mu.RLock()
	state, exists := c.backends[address]
	if !exists {
		c.mu.RUnlock()
		return
	}
	interval := state.backend.Interval
	c.mu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			_, exists := c.backends[address]
			c.mu.RUnlock()

			if !exists {
				return
			}

			c.check(address)
		}
	}
}

// check performs a single TCP health check
func (c *TCPChecker) check(address string) {
	c.mu.RLock()
	state, exists := c.backends[address]
	if !exists {
		c.mu.RUnlock()
		return
	}
	backend := state.backend
	c.mu.RUnlock()

	// Perform TCP connection check
	start := time.Now()

	conn, err := net.DialTimeout("tcp", address, backend.Timeout)
	latency := time.Since(start)

	if err != nil {
		c.updateStatus(address, false, latency, err)
		return
	}

	// Connection successful - close it immediately
	conn.Close()
	c.updateStatus(address, true, latency, nil)
}

// updateStatus updates the health status with threshold logic
func (c *TCPChecker) updateStatus(address string, healthy bool, latency time.Duration, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, exists := c.backends[address]
	if !exists {
		return
	}

	state.lastCheck = time.Now()
	state.lastError = err
	state.latency = latency

	oldStatus := state.status

	if healthy {
		state.consecutiveFail = 0
		state.consecutivePass++

		if state.consecutivePass >= state.backend.HealthyAfter {
			state.status = StatusHealthy
		}
	} else {
		state.consecutivePass = 0
		state.consecutiveFail++

		if state.consecutiveFail >= state.backend.UnhealthyAfter {
			state.status = StatusUnhealthy
		}
	}

	// Notify on status change
	if oldStatus != state.status && c.onChange != nil {
		go c.onChange(address, state.status)
	}
}

// CheckNow performs an immediate TCP health check
func (c *TCPChecker) CheckNow(address string) CheckResult {
	c.check(address)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if state, ok := c.backends[address]; ok {
		return CheckResult{
			URL:       address,
			Status:    state.status,
			Latency:   state.latency,
			Error:     state.lastError,
			Timestamp: state.lastCheck,
		}
	}

	return CheckResult{
		URL:       address,
		Status:    StatusUnknown,
		Timestamp: time.Now(),
	}
}

// Start starts all TCP health checks
func (c *TCPChecker) Start() {
	c.mu.RLock()
	addresses := make([]string, 0, len(c.backends))
	for addr := range c.backends {
		addresses = append(addresses, addr)
	}
	c.mu.RUnlock()

	for _, addr := range addresses {
		go c.checkLoop(addr)
	}
}

// Stop stops all TCP health checks
func (c *TCPChecker) Stop() {
	c.cancel()
}

// HealthyBackends returns addresses of all healthy TCP backends
func (c *TCPChecker) HealthyBackends() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var healthy []string
	for addr, state := range c.backends {
		if state.status == StatusHealthy {
			healthy = append(healthy, addr)
		}
	}
	return healthy
}

// CheckTCPConnection performs a one-shot TCP connection check
func CheckTCPConnection(address string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return fmt.Errorf("TCP connection failed: %w", err)
	}
	conn.Close()
	return nil
}
