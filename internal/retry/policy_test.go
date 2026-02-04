package retry

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
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
		if err != context.Canceled {
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

func TestCalculateBackoff(t *testing.T) {
	p := &Policy{
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        1 * time.Second,
		BackoffMultiplier: 2.0,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{5, 1 * time.Second}, // capped at MaxBackoff
		{6, 1 * time.Second}, // capped at MaxBackoff
	}

	for _, tt := range tests {
		got := p.calculateBackoff(tt.attempt)
		if got != tt.expected {
			t.Errorf("calculateBackoff(%d) = %v, want %v", tt.attempt, got, tt.expected)
		}
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
