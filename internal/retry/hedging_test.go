package retry

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHedging_FirstResponseWins(t *testing.T) {
	metrics := &RouteRetryMetrics{}
	h := NewHedgingExecutor(config.HedgingConfig{
		Enabled:     true,
		MaxRequests: 2,
		Delay:       10 * time.Millisecond,
	}, metrics)

	var callCount atomic.Int64
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := callCount.Add(1)
		if n == 1 {
			// Original: slow
			time.Sleep(200 * time.Millisecond)
		}
		// Hedged (or fast original): fast
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader([]byte("ok"))),
		}, nil
	})

	backends := []string{"http://backend1:8080", "http://backend2:8080"}
	idx := atomic.Int64{}

	nextBackend := func() string {
		i := idx.Add(1) - 1
		if int(i) >= len(backends) {
			return ""
		}
		return backends[i]
	}

	makeReq := func(target *url.URL) (*http.Request, error) {
		req, _ := http.NewRequest("GET", target.String()+"/test", nil)
		return req, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := h.Execute(ctx, transport, nextBackend, makeReq, 0)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Should complete faster than 200ms (the slow backend)
	if elapsed > 150*time.Millisecond {
		t.Errorf("hedging should have reduced latency, took %v", elapsed)
	}
}

func TestHedging_MaxRequestsLimit(t *testing.T) {
	metrics := &RouteRetryMetrics{}
	h := NewHedgingExecutor(config.HedgingConfig{
		Enabled:     true,
		MaxRequests: 3,
		Delay:       5 * time.Millisecond,
	}, metrics)

	var callCount atomic.Int64
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		callCount.Add(1)
		time.Sleep(50 * time.Millisecond)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader([]byte("ok"))),
		}, nil
	})

	backends := []string{"http://b1:8080", "http://b2:8080", "http://b3:8080", "http://b4:8080"}
	idx := atomic.Int64{}

	nextBackend := func() string {
		i := idx.Add(1) - 1
		if int(i) >= len(backends) {
			return ""
		}
		return backends[i]
	}

	makeReq := func(target *url.URL) (*http.Request, error) {
		req, _ := http.NewRequest("GET", target.String()+"/test", nil)
		return req, nil
	}

	ctx := context.Background()
	resp, err := h.Execute(ctx, transport, nextBackend, makeReq, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// Should have made at most 3 requests (maxRequests)
	count := callCount.Load()
	if count > 3 {
		t.Errorf("expected at most 3 calls, got %d", count)
	}
}

func TestHedging_ContextCancellation(t *testing.T) {
	metrics := &RouteRetryMetrics{}
	h := NewHedgingExecutor(config.HedgingConfig{
		Enabled:     true,
		MaxRequests: 2,
		Delay:       10 * time.Millisecond,
	}, metrics)

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		// Block until context is cancelled
		<-req.Context().Done()
		return nil, req.Context().Err()
	})

	backends := []string{"http://b1:8080", "http://b2:8080"}
	idx := atomic.Int64{}

	nextBackend := func() string {
		i := idx.Add(1) - 1
		if int(i) >= len(backends) {
			return ""
		}
		return backends[i]
	}

	makeReq := func(target *url.URL) (*http.Request, error) {
		req, _ := http.NewRequest("GET", target.String()+"/test", nil)
		return req, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := h.Execute(ctx, transport, nextBackend, makeReq, 0)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestHedging_NoBackends(t *testing.T) {
	metrics := &RouteRetryMetrics{}
	h := NewHedgingExecutor(config.HedgingConfig{
		Enabled:     true,
		MaxRequests: 2,
		Delay:       10 * time.Millisecond,
	}, metrics)

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	})

	nextBackend := func() string { return "" }
	makeReq := func(target *url.URL) (*http.Request, error) {
		return http.NewRequest("GET", target.String(), nil)
	}

	ctx := context.Background()
	_, err := h.Execute(ctx, transport, nextBackend, makeReq, 0)
	if err == nil {
		t.Error("expected error when no backends available")
	}
}

func TestHedging_Metrics(t *testing.T) {
	metrics := &RouteRetryMetrics{}
	h := NewHedgingExecutor(config.HedgingConfig{
		Enabled:     true,
		MaxRequests: 2,
		Delay:       5 * time.Millisecond,
	}, metrics)

	var callCount atomic.Int64
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := callCount.Add(1)
		if n == 1 {
			// Original: slow
			time.Sleep(100 * time.Millisecond)
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader([]byte("ok"))),
		}, nil
	})

	backends := []string{"http://b1:8080", "http://b2:8080"}
	idx := atomic.Int64{}
	nextBackend := func() string {
		i := idx.Add(1) - 1
		if int(i) >= len(backends) {
			return ""
		}
		return backends[i]
	}

	makeReq := func(target *url.URL) (*http.Request, error) {
		return http.NewRequest("GET", target.String()+"/test", nil)
	}

	ctx := context.Background()
	resp, err := h.Execute(ctx, transport, nextBackend, makeReq, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// Wait a bit for goroutines
	time.Sleep(20 * time.Millisecond)

	snap := metrics.Snapshot()
	if snap.HedgedRequests != 1 {
		t.Errorf("expected 1 hedged request, got %d", snap.HedgedRequests)
	}
}

func TestBufferBody(t *testing.T) {
	body := "hello world"
	req, _ := http.NewRequest("POST", "http://example.com", bytes.NewReader([]byte(body)))

	buf, err := BufferBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(buf) != body {
		t.Errorf("expected %q, got %q", body, string(buf))
	}

	// Body should still be readable
	readBack, _ := io.ReadAll(req.Body)
	if string(readBack) != body {
		t.Errorf("expected body to be re-readable, got %q", string(readBack))
	}
}

func TestBufferBody_NilBody(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	buf, err := BufferBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf != nil {
		t.Errorf("expected nil buf for nil body, got %v", buf)
	}
}
