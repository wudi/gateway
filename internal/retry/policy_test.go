package retry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

// mockTransport is a mock http.RoundTripper for testing
type mockTransport struct {
	responses []*http.Response
	errors    []error
	calls     int
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	idx := m.calls
	m.calls++

	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}

	if idx < len(m.responses) {
		return m.responses[idx], nil
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
	}, nil
}

func TestNewPolicy(t *testing.T) {
	cfg := config.RetryConfig{
		MaxRetries:        3,
		InitialBackoff:    50 * time.Millisecond,
		MaxBackoff:        1 * time.Second,
		BackoffMultiplier: 2.0,
		RetryableStatuses: []int{502, 503},
		RetryableMethods:  []string{"GET"},
	}

	p := NewPolicy(cfg)

	if p.MaxRetries != 3 {
		t.Errorf("expected MaxRetries 3, got %d", p.MaxRetries)
	}
	if p.InitialBackoff != 50*time.Millisecond {
		t.Errorf("expected InitialBackoff 50ms, got %v", p.InitialBackoff)
	}
	if !p.RetryableStatuses[502] {
		t.Error("expected 502 to be retryable")
	}
	if p.RetryableStatuses[504] {
		t.Error("expected 504 to not be retryable")
	}
	if !p.RetryableMethods["GET"] {
		t.Error("expected GET to be retryable")
	}
	if p.RetryableMethods["POST"] {
		t.Error("expected POST to not be retryable")
	}
}

func TestNewPolicyDefaults(t *testing.T) {
	p := NewPolicy(config.RetryConfig{MaxRetries: 1})

	if p.InitialBackoff != 100*time.Millisecond {
		t.Errorf("expected default InitialBackoff 100ms, got %v", p.InitialBackoff)
	}
	if p.MaxBackoff != 10*time.Second {
		t.Errorf("expected default MaxBackoff 10s, got %v", p.MaxBackoff)
	}
	if p.BackoffMultiplier != 2.0 {
		t.Errorf("expected default BackoffMultiplier 2.0, got %v", p.BackoffMultiplier)
	}
	if !p.RetryableStatuses[502] || !p.RetryableStatuses[503] || !p.RetryableStatuses[504] {
		t.Error("expected default retryable statuses 502, 503, 504")
	}
	if !p.RetryableMethods["GET"] || !p.RetryableMethods["HEAD"] || !p.RetryableMethods["OPTIONS"] {
		t.Error("expected default retryable methods GET, HEAD, OPTIONS")
	}
}

func TestNewPolicyFromLegacy(t *testing.T) {
	p := NewPolicyFromLegacy(2, 5*time.Second)

	if p.MaxRetries != 2 {
		t.Errorf("expected MaxRetries 2, got %d", p.MaxRetries)
	}
	if p.PerTryTimeout != 5*time.Second {
		t.Errorf("expected PerTryTimeout 5s, got %v", p.PerTryTimeout)
	}
}

func TestIsRetryable(t *testing.T) {
	p := NewPolicy(config.RetryConfig{MaxRetries: 1})

	tests := []struct {
		method string
		status int
		want   bool
	}{
		{"GET", 502, true},
		{"GET", 503, true},
		{"GET", 504, true},
		{"GET", 200, false},
		{"GET", 500, false},
		{"POST", 502, false},
		{"HEAD", 503, true},
		{"OPTIONS", 504, true},
		{"DELETE", 502, false},
	}

	for _, tt := range tests {
		got := p.IsRetryable(tt.method, tt.status)
		if got != tt.want {
			t.Errorf("IsRetryable(%s, %d) = %v, want %v", tt.method, tt.status, got, tt.want)
		}
	}
}

func TestExecuteNoRetries(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{
			{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))},
		},
	}

	p := NewPolicy(config.RetryConfig{MaxRetries: 0})
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	resp, err := p.Execute(context.Background(), transport, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if transport.calls != 1 {
		t.Errorf("expected 1 call, got %d", transport.calls)
	}
}

func TestExecuteRetriesOnError(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{
			nil,
			nil,
			{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))},
		},
		errors: []error{
			io.ErrUnexpectedEOF,
			io.ErrUnexpectedEOF,
			nil,
		},
	}

	p := NewPolicy(config.RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
	})
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	resp, err := p.Execute(context.Background(), transport, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if transport.calls != 3 {
		t.Errorf("expected 3 calls, got %d", transport.calls)
	}
}

func TestExecuteRetriesOnStatus(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{
			{StatusCode: 502, Body: io.NopCloser(strings.NewReader("bad"))},
			{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))},
		},
	}

	p := NewPolicy(config.RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
	})
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	resp, err := p.Execute(context.Background(), transport, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if transport.calls != 2 {
		t.Errorf("expected 2 calls, got %d", transport.calls)
	}
}

func TestExecuteExhaustsRetries(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{
			{StatusCode: 502, Body: io.NopCloser(strings.NewReader("bad"))},
			{StatusCode: 502, Body: io.NopCloser(strings.NewReader("bad"))},
			{StatusCode: 502, Body: io.NopCloser(strings.NewReader("bad"))},
		},
	}

	p := NewPolicy(config.RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
	})
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	resp, err := p.Execute(context.Background(), transport, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return the last response (502)
	if resp.StatusCode != 502 {
		t.Errorf("expected 502, got %d", resp.StatusCode)
	}
	if transport.calls != 3 {
		t.Errorf("expected 3 calls, got %d", transport.calls)
	}
}

func TestExecuteContextCancellation(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{
			{StatusCode: 502, Body: io.NopCloser(strings.NewReader("bad"))},
		},
	}

	p := NewPolicy(config.RetryConfig{
		MaxRetries:     5,
		InitialBackoff: 1 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	done := make(chan struct{})
	go func() {
		_, err := p.Execute(ctx, transport, req)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
		close(done)
	}()

	// Cancel after the first attempt
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return after context cancellation")
	}
}

func TestExecuteNoRetryOnPOST(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{
			{StatusCode: 502, Body: io.NopCloser(strings.NewReader("bad"))},
		},
	}

	p := NewPolicy(config.RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Millisecond,
	})
	req, _ := http.NewRequest("POST", "http://example.com", nil)

	resp, err := p.Execute(context.Background(), transport, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 502 {
		t.Errorf("expected 502 (no retry for POST), got %d", resp.StatusCode)
	}
	if transport.calls != 1 {
		t.Errorf("expected 1 call (no retry for POST), got %d", transport.calls)
	}
}

func TestMetrics(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{
			{StatusCode: 502, Body: io.NopCloser(strings.NewReader("bad"))},
			{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))},
		},
	}

	p := NewPolicy(config.RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Millisecond,
	})
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	_, err := p.Execute(context.Background(), transport, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	snap := p.Metrics.Snapshot()
	if snap.Requests != 1 {
		t.Errorf("expected 1 request, got %d", snap.Requests)
	}
	if snap.Retries != 1 {
		t.Errorf("expected 1 retry, got %d", snap.Retries)
	}
	if snap.Successes != 1 {
		t.Errorf("expected 1 success, got %d", snap.Successes)
	}
	if snap.Failures != 0 {
		t.Errorf("expected 0 failures, got %d", snap.Failures)
	}
}

func TestBudgetExhaustedStopsRetries(t *testing.T) {
	// All responses are retryable 503s
	transport := &mockTransport{
		responses: []*http.Response{
			{StatusCode: 503, Body: io.NopCloser(strings.NewReader("bad"))},
			{StatusCode: 503, Body: io.NopCloser(strings.NewReader("bad"))},
			{StatusCode: 503, Body: io.NopCloser(strings.NewReader("bad"))},
			{StatusCode: 503, Body: io.NopCloser(strings.NewReader("bad"))},
			{StatusCode: 503, Body: io.NopCloser(strings.NewReader("bad"))},
		},
	}

	p := NewPolicy(config.RetryConfig{
		MaxRetries:     5,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     2 * time.Millisecond,
		Budget: config.BudgetConfig{
			Ratio:      0.01, // extremely tight: ~1% retry ratio
			MinRetries: 0,    // no minimum bypass
			Window:     10 * time.Second,
		},
	})

	if p.Budget == nil {
		t.Fatal("expected budget to be non-nil")
	}

	// Simulate many prior requests to fill the budget window
	for i := 0; i < 100; i++ {
		p.Budget.RecordRequest()
	}
	// Record 2 retries → 2/100 = 2%, well over the 1% limit
	p.Budget.RecordRetry()
	p.Budget.RecordRetry()

	req, _ := http.NewRequest("GET", "http://example.com", nil)
	resp, err := p.Execute(context.Background(), transport, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return 503 (the first response) because budget prevented retries
	if resp.StatusCode != 503 {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}

	// Budget should have been exhausted — only 1 attempt (first call), retry blocked
	if transport.calls != 1 {
		t.Errorf("expected 1 call (budget should prevent retries), got %d", transport.calls)
	}

	snap := p.Metrics.Snapshot()
	if snap.BudgetExhausted != 1 {
		t.Errorf("expected BudgetExhausted=1, got %d", snap.BudgetExhausted)
	}
}

func TestBudgetAllowsRetriesWithinLimit(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{
			{StatusCode: 503, Body: io.NopCloser(strings.NewReader("bad"))},
			{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))},
		},
	}

	p := NewPolicy(config.RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     2 * time.Millisecond,
		Budget: config.BudgetConfig{
			Ratio:      0.5, // generous: 50% retries allowed
			MinRetries: 0,
			Window:     10 * time.Second,
		},
	})

	req, _ := http.NewRequest("GET", "http://example.com", nil)
	resp, err := p.Execute(context.Background(), transport, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("expected 200 (retry should succeed), got %d", resp.StatusCode)
	}
	if transport.calls != 2 {
		t.Errorf("expected 2 calls, got %d", transport.calls)
	}

	snap := p.Metrics.Snapshot()
	if snap.BudgetExhausted != 0 {
		t.Errorf("expected BudgetExhausted=0, got %d", snap.BudgetExhausted)
	}
}

func TestNewPolicyCreatesHedging(t *testing.T) {
	p := NewPolicy(config.RetryConfig{
		Hedging: config.HedgingConfig{
			Enabled:     true,
			MaxRequests: 3,
			Delay:       50 * time.Millisecond,
		},
	})

	if p.Hedging == nil {
		t.Fatal("expected hedging executor to be created")
	}
	if p.Hedging.maxRequests != 3 {
		t.Errorf("expected maxRequests=3, got %d", p.Hedging.maxRequests)
	}
	if p.Hedging.delay != 50*time.Millisecond {
		t.Errorf("expected delay=50ms, got %v", p.Hedging.delay)
	}
}

func TestNewPolicyCreatesBudget(t *testing.T) {
	p := NewPolicy(config.RetryConfig{
		MaxRetries: 3,
		Budget: config.BudgetConfig{
			Ratio:      0.2,
			MinRetries: 5,
			Window:     30 * time.Second,
		},
	})

	if p.Budget == nil {
		t.Fatal("expected budget to be created")
	}
	if p.Budget.ratio != 0.2 {
		t.Errorf("expected ratio=0.2, got %f", p.Budget.ratio)
	}
	if p.Budget.minRetriesPerS != 5 {
		t.Errorf("expected minRetriesPerS=5, got %d", p.Budget.minRetriesPerS)
	}
}
