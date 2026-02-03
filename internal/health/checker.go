package health

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Status represents health status
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
	StatusUnknown   Status = "unknown"
)

// CheckResult represents the result of a health check
type CheckResult struct {
	URL       string
	Status    Status
	Latency   time.Duration
	Error     error
	Timestamp time.Time
}

// Backend represents a backend to check
type Backend struct {
	URL           string
	HealthPath    string
	Timeout       time.Duration
	Interval      time.Duration
	HealthyAfter  int // consecutive successes needed to be healthy
	UnhealthyAfter int // consecutive failures needed to be unhealthy
}

// Checker performs health checks on backends
type Checker struct {
	client         *http.Client
	backends       map[string]*backendState
	mu             sync.RWMutex
	defaultTimeout time.Duration
	defaultInterval time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	onChange       func(url string, status Status)
}

type backendState struct {
	backend          Backend
	status           Status
	lastCheck        time.Time
	lastError        error
	consecutivePass  int
	consecutiveFail  int
	latency          time.Duration
}

// Config holds health checker configuration
type Config struct {
	DefaultTimeout  time.Duration
	DefaultInterval time.Duration
	OnChange        func(url string, status Status)
}

// DefaultConfig provides default health checker settings
var DefaultConfig = Config{
	DefaultTimeout:  5 * time.Second,
	DefaultInterval: 10 * time.Second,
}

// NewChecker creates a new health checker
func NewChecker(cfg Config) *Checker {
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = DefaultConfig.DefaultTimeout
	}
	if cfg.DefaultInterval == 0 {
		cfg.DefaultInterval = DefaultConfig.DefaultInterval
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Checker{
		client: &http.Client{
			Timeout: cfg.DefaultTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		backends:        make(map[string]*backendState),
		defaultTimeout:  cfg.DefaultTimeout,
		defaultInterval: cfg.DefaultInterval,
		ctx:             ctx,
		cancel:          cancel,
		onChange:        cfg.OnChange,
	}
}

// AddBackend adds a backend for health checking
func (c *Checker) AddBackend(b Backend) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if b.HealthPath == "" {
		b.HealthPath = "/health"
	}
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

	state := &backendState{
		backend: b,
		status:  StatusUnknown,
	}

	c.backends[b.URL] = state

	// Start checking this backend
	go c.checkLoop(b.URL)
}

// RemoveBackend removes a backend from health checking
func (c *Checker) RemoveBackend(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.backends, url)
}

// GetStatus returns the health status of a backend
func (c *Checker) GetStatus(url string) Status {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if state, ok := c.backends[url]; ok {
		return state.status
	}
	return StatusUnknown
}

// GetAllStatus returns the health status of all backends
func (c *Checker) GetAllStatus() map[string]CheckResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	results := make(map[string]CheckResult)
	for url, state := range c.backends {
		results[url] = CheckResult{
			URL:       url,
			Status:    state.status,
			Latency:   state.latency,
			Error:     state.lastError,
			Timestamp: state.lastCheck,
		}
	}
	return results
}

// IsHealthy returns true if the backend is healthy
func (c *Checker) IsHealthy(url string) bool {
	return c.GetStatus(url) == StatusHealthy
}

// checkLoop runs periodic health checks for a backend
func (c *Checker) checkLoop(url string) {
	// Initial check
	c.check(url)

	c.mu.RLock()
	state, exists := c.backends[url]
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
			_, exists := c.backends[url]
			c.mu.RUnlock()

			if !exists {
				return
			}

			c.check(url)
		}
	}
}

// check performs a single health check
func (c *Checker) check(url string) {
	c.mu.RLock()
	state, exists := c.backends[url]
	if !exists {
		c.mu.RUnlock()
		return
	}
	backend := state.backend
	c.mu.RUnlock()

	// Perform HTTP health check
	checkURL := url + backend.HealthPath
	start := time.Now()

	ctx, cancel := context.WithTimeout(c.ctx, backend.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", checkURL, nil)
	if err != nil {
		c.updateStatus(url, false, time.Since(start), err)
		return
	}

	resp, err := c.client.Do(req)
	latency := time.Since(start)

	if err != nil {
		c.updateStatus(url, false, latency, err)
		return
	}
	defer resp.Body.Close()

	// Consider 2xx and 3xx as healthy
	healthy := resp.StatusCode >= 200 && resp.StatusCode < 400
	var checkErr error
	if !healthy {
		checkErr = fmt.Errorf("unhealthy status code: %d", resp.StatusCode)
	}

	c.updateStatus(url, healthy, latency, checkErr)
}

// updateStatus updates the health status with threshold logic
func (c *Checker) updateStatus(url string, healthy bool, latency time.Duration, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, exists := c.backends[url]
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
		go c.onChange(url, state.status)
	}
}

// CheckNow performs an immediate health check
func (c *Checker) CheckNow(url string) CheckResult {
	c.check(url)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if state, ok := c.backends[url]; ok {
		return CheckResult{
			URL:       url,
			Status:    state.status,
			Latency:   state.latency,
			Error:     state.lastError,
			Timestamp: state.lastCheck,
		}
	}

	return CheckResult{
		URL:       url,
		Status:    StatusUnknown,
		Timestamp: time.Now(),
	}
}

// Start starts all health checks
func (c *Checker) Start() {
	c.mu.RLock()
	urls := make([]string, 0, len(c.backends))
	for url := range c.backends {
		urls = append(urls, url)
	}
	c.mu.RUnlock()

	for _, url := range urls {
		go c.checkLoop(url)
	}
}

// Stop stops all health checks
func (c *Checker) Stop() {
	c.cancel()
}

// HealthyBackends returns URLs of all healthy backends
func (c *Checker) HealthyBackends() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var healthy []string
	for url, state := range c.backends {
		if state.status == StatusHealthy {
			healthy = append(healthy, url)
		}
	}
	return healthy
}
