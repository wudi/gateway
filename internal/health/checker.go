package health

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
	URL            string
	HealthPath     string
	Method         string        // HTTP method, default "GET"
	Timeout        time.Duration
	Interval       time.Duration
	HealthyAfter   int // consecutive successes needed to be healthy
	UnhealthyAfter int // consecutive failures needed to be unhealthy
	ExpectedStatus []StatusRange // parsed ranges, default [{200, 399}]
}

// StatusRange represents a range of HTTP status codes.
type StatusRange struct {
	Lo, Hi int
}

// ParseStatusRange parses a status range string like "200", "2xx", "200-299".
func ParseStatusRange(s string) (StatusRange, error) {
	s = strings.TrimSpace(s)
	// Pattern: Nxx (e.g. "4xx", "5xx")
	if len(s) == 3 && s[1] == 'x' && s[2] == 'x' {
		base := int(s[0]-'0') * 100
		if base < 100 || base > 500 {
			return StatusRange{}, fmt.Errorf("invalid status range %q", s)
		}
		return StatusRange{base, base + 99}, nil
	}
	// Pattern: N-M (e.g. "200-299")
	if parts := strings.SplitN(s, "-", 2); len(parts) == 2 {
		lo, err1 := strconv.Atoi(parts[0])
		hi, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || lo < 100 || hi > 599 || lo > hi {
			return StatusRange{}, fmt.Errorf("invalid status range %q", s)
		}
		return StatusRange{lo, hi}, nil
	}
	// Pattern: single code (e.g. "200")
	code, err := strconv.Atoi(s)
	if err != nil || code < 100 || code > 599 {
		return StatusRange{}, fmt.Errorf("invalid status code %q", s)
	}
	return StatusRange{code, code}, nil
}

// matchStatus checks if a status code falls within any of the given ranges.
func matchStatus(code int, ranges []StatusRange) bool {
	for _, r := range ranges {
		if code >= r.Lo && code <= r.Hi {
			return true
		}
	}
	return false
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
	if b.Method == "" {
		b.Method = "GET"
	}
	if len(b.ExpectedStatus) == 0 {
		b.ExpectedStatus = []StatusRange{{200, 399}}
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

// UpdateBackend adds or replaces a backend's health check configuration.
// If the backend already exists with identical config, this is a no-op.
// If config changed, the backend is removed and re-added (restarts the check loop).
func (c *Checker) UpdateBackend(b Backend) {
	// Apply defaults so comparison works correctly
	applyBackendDefaults(&b, c.defaultTimeout, c.defaultInterval)

	c.mu.RLock()
	existing, exists := c.backends[b.URL]
	c.mu.RUnlock()

	if exists && backendsEqual(existing.backend, b) {
		return
	}

	if exists {
		c.RemoveBackend(b.URL)
	}
	c.AddBackend(b)
}

func applyBackendDefaults(b *Backend, defaultTimeout, defaultInterval time.Duration) {
	if b.HealthPath == "" {
		b.HealthPath = "/health"
	}
	if b.Timeout == 0 {
		b.Timeout = defaultTimeout
	}
	if b.Interval == 0 {
		b.Interval = defaultInterval
	}
	if b.Method == "" {
		b.Method = "GET"
	}
	if len(b.ExpectedStatus) == 0 {
		b.ExpectedStatus = []StatusRange{{200, 399}}
	}
	if b.HealthyAfter == 0 {
		b.HealthyAfter = 2
	}
	if b.UnhealthyAfter == 0 {
		b.UnhealthyAfter = 3
	}
}

func backendsEqual(a, b Backend) bool {
	if a.HealthPath != b.HealthPath || a.Method != b.Method ||
		a.Timeout != b.Timeout || a.Interval != b.Interval ||
		a.HealthyAfter != b.HealthyAfter || a.UnhealthyAfter != b.UnhealthyAfter {
		return false
	}
	if len(a.ExpectedStatus) != len(b.ExpectedStatus) {
		return false
	}
	for i := range a.ExpectedStatus {
		if a.ExpectedStatus[i] != b.ExpectedStatus[i] {
			return false
		}
	}
	return true
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

// GetBackendConfig returns the health check configuration for a backend.
func (c *Checker) GetBackendConfig(url string) (Backend, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if state, ok := c.backends[url]; ok {
		return state.backend, true
	}
	return Backend{}, false
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

	req, err := http.NewRequestWithContext(ctx, backend.Method, checkURL, nil)
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

	// Check against expected status ranges
	healthy := matchStatus(resp.StatusCode, backend.ExpectedStatus)
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
