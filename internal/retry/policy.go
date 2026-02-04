package retry

import (
	"context"
	"math"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/example/gateway/internal/config"
)

// DefaultRetryableStatuses are HTTP status codes that trigger a retry
var DefaultRetryableStatuses = []int{502, 503, 504}

// DefaultRetryableMethods are HTTP methods safe to retry
var DefaultRetryableMethods = []string{"GET", "HEAD", "OPTIONS"}

// Policy implements retry logic with exponential backoff
type Policy struct {
	MaxRetries        int
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64
	RetryableStatuses map[int]bool
	RetryableMethods  map[string]bool
	PerTryTimeout     time.Duration
	Metrics           *RouteRetryMetrics
}

// RouteRetryMetrics tracks retry statistics for a route
type RouteRetryMetrics struct {
	Requests  atomic.Int64
	Retries   atomic.Int64
	Successes atomic.Int64
	Failures  atomic.Int64
}

// Snapshot returns a point-in-time copy of the metrics
func (m *RouteRetryMetrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Requests:  m.Requests.Load(),
		Retries:   m.Retries.Load(),
		Successes: m.Successes.Load(),
		Failures:  m.Failures.Load(),
	}
}

// MetricsSnapshot is a point-in-time copy of retry metrics
type MetricsSnapshot struct {
	Requests  int64 `json:"requests"`
	Retries   int64 `json:"retries"`
	Successes int64 `json:"successes"`
	Failures  int64 `json:"failures"`
}

// NewPolicy creates a retry policy from config
func NewPolicy(cfg config.RetryConfig) *Policy {
	p := &Policy{
		MaxRetries:        cfg.MaxRetries,
		InitialBackoff:    cfg.InitialBackoff,
		MaxBackoff:        cfg.MaxBackoff,
		BackoffMultiplier: cfg.BackoffMultiplier,
		PerTryTimeout:     cfg.PerTryTimeout,
		Metrics:           &RouteRetryMetrics{},
	}

	// Apply defaults
	if p.InitialBackoff == 0 {
		p.InitialBackoff = 100 * time.Millisecond
	}
	if p.MaxBackoff == 0 {
		p.MaxBackoff = 10 * time.Second
	}
	if p.BackoffMultiplier == 0 {
		p.BackoffMultiplier = 2.0
	}

	// Build retryable statuses map
	statuses := cfg.RetryableStatuses
	if len(statuses) == 0 {
		statuses = DefaultRetryableStatuses
	}
	p.RetryableStatuses = make(map[int]bool, len(statuses))
	for _, s := range statuses {
		p.RetryableStatuses[s] = true
	}

	// Build retryable methods map
	methods := cfg.RetryableMethods
	if len(methods) == 0 {
		methods = DefaultRetryableMethods
	}
	p.RetryableMethods = make(map[string]bool, len(methods))
	for _, m := range methods {
		p.RetryableMethods[m] = true
	}

	return p
}

// NewPolicyFromLegacy creates a retry policy from legacy Retries/Timeout fields
func NewPolicyFromLegacy(retries int, timeout time.Duration) *Policy {
	cfg := config.RetryConfig{
		MaxRetries:     retries,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
	}
	if timeout > 0 {
		cfg.PerTryTimeout = timeout
	}
	return NewPolicy(cfg)
}

// Execute runs the request with retry logic
func (p *Policy) Execute(ctx context.Context, transport http.RoundTripper, req *http.Request) (*http.Response, error) {
	p.Metrics.Requests.Add(1)

	if p.MaxRetries <= 0 {
		resp, err := p.doRoundTrip(ctx, transport, req)
		if err != nil {
			p.Metrics.Failures.Add(1)
			return nil, err
		}
		p.Metrics.Successes.Add(1)
		return resp, nil
	}

	var lastResp *http.Response
	var lastErr error

	for attempt := 0; attempt <= p.MaxRetries; attempt++ {
		if attempt > 0 {
			p.Metrics.Retries.Add(1)

			// Wait with backoff
			backoff := p.calculateBackoff(attempt)
			select {
			case <-ctx.Done():
				if lastResp != nil {
					lastResp.Body.Close()
				}
				p.Metrics.Failures.Add(1)
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		resp, err := p.doRoundTrip(ctx, transport, req)
		if err != nil {
			lastErr = err
			lastResp = nil
			continue
		}

		if !p.IsRetryable(req.Method, resp.StatusCode) {
			p.Metrics.Successes.Add(1)
			return resp, nil
		}

		// Close the body before retrying
		if lastResp != nil {
			lastResp.Body.Close()
		}
		lastResp = resp
		lastErr = nil
	}

	// All retries exhausted
	p.Metrics.Failures.Add(1)
	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

func (p *Policy) doRoundTrip(ctx context.Context, transport http.RoundTripper, req *http.Request) (*http.Response, error) {
	if p.PerTryTimeout > 0 {
		tryCtx, cancel := context.WithTimeout(ctx, p.PerTryTimeout)
		defer cancel()
		return transport.RoundTrip(req.WithContext(tryCtx))
	}
	return transport.RoundTrip(req)
}

// IsRetryable returns true if the method+status combination should be retried
func (p *Policy) IsRetryable(method string, statusCode int) bool {
	if !p.RetryableMethods[method] {
		return false
	}
	return p.RetryableStatuses[statusCode]
}

// calculateBackoff returns the backoff duration for a given attempt
func (p *Policy) calculateBackoff(attempt int) time.Duration {
	backoff := float64(p.InitialBackoff) * math.Pow(p.BackoffMultiplier, float64(attempt-1))
	if backoff > float64(p.MaxBackoff) {
		backoff = float64(p.MaxBackoff)
	}
	return time.Duration(backoff)
}
