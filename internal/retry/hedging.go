package retry

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/wudi/gateway/config"
)

// HedgingExecutor sends speculative duplicate requests to reduce tail latency.
// It launches the original request, waits for a delay, then sends hedged copies
// to different backends. The first successful response wins.
type HedgingExecutor struct {
	maxRequests int
	delay       time.Duration
	metrics     *RouteRetryMetrics
}

// NewHedgingExecutor creates a hedging executor from config.
func NewHedgingExecutor(cfg config.HedgingConfig, metrics *RouteRetryMetrics) *HedgingExecutor {
	maxReqs := cfg.MaxRequests
	if maxReqs < 2 {
		maxReqs = 2
	}
	delay := cfg.Delay
	if delay <= 0 {
		delay = 100 * time.Millisecond
	}
	return &HedgingExecutor{
		maxRequests: maxReqs,
		delay:       delay,
		metrics:     metrics,
	}
}

type hedgeResult struct {
	resp    *http.Response
	err     error
	isHedge bool
}

// Execute sends the request with hedging. nextBackend returns a backend URL string.
// makeReq creates a new request for a given backend URL.
func (h *HedgingExecutor) Execute(
	ctx context.Context,
	transport http.RoundTripper,
	nextBackend func() string,
	makeReq func(target *url.URL) (*http.Request, error),
	perTryTimeout time.Duration,
) (*http.Response, error) {
	// Buffer the body for reuse across hedged requests
	// We need the original request to read the body from, so makeReq is called per-backend
	// The caller is responsible for providing a makeReq that can be called multiple times

	hedgeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultCh := make(chan hedgeResult, h.maxRequests)

	var wg sync.WaitGroup

	// Launch original request
	backendURL := nextBackend()
	if backendURL == "" {
		return nil, &noBackendError{}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := h.doRequest(hedgeCtx, transport, makeReq, backendURL, perTryTimeout)
		resultCh <- hedgeResult{resp: resp, err: err, isHedge: false}
	}()

	// After delay, launch hedged requests
	for i := 1; i < h.maxRequests; i++ {
		select {
		case res := <-resultCh:
			// Original already returned before delay expired
			if res.err == nil && res.resp != nil && res.resp.StatusCode < 500 {
				// Close any remaining in-flight responses in background
				go func() {
					wg.Wait()
					close(resultCh)
					for r := range resultCh {
						if r.resp != nil {
							r.resp.Body.Close()
						}
					}
				}()
				return res.resp, nil
			}
			// Original failed/errored; still launch hedge
			if res.resp != nil {
				res.resp.Body.Close()
			}
			// Fall through to launch hedge
		case <-time.After(h.delay):
			// Delay expired, launch hedge
		case <-hedgeCtx.Done():
			break
		}

		hedgeBackend := nextBackend()
		if hedgeBackend == "" {
			continue
		}

		h.metrics.HedgedRequests.Add(1)
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			resp, err := h.doRequest(hedgeCtx, transport, makeReq, url, perTryTimeout)
			resultCh <- hedgeResult{resp: resp, err: err, isHedge: true}
		}(hedgeBackend)
	}

	// Collect results — pick first successful response
	var bestResp *http.Response
	var bestErr error
	var bestIsHedge bool
	remaining := h.maxRequests

	for remaining > 0 {
		select {
		case res := <-resultCh:
			remaining--
			if res.err == nil && res.resp != nil && res.resp.StatusCode < 500 {
				if bestResp != nil {
					bestResp.Body.Close()
				}
				bestResp = res.resp
				bestErr = nil
				bestIsHedge = res.isHedge

				// Cancel other in-flight requests
				cancel()

				// Drain remaining results
				go func() {
					wg.Wait()
					close(resultCh)
					for r := range resultCh {
						if r.resp != nil && r.resp != bestResp {
							r.resp.Body.Close()
						}
					}
				}()

				if bestIsHedge {
					h.metrics.HedgedWins.Add(1)
				}
				return bestResp, nil
			}
			// Save as fallback
			if bestResp == nil && res.resp != nil {
				bestResp = res.resp
				bestErr = res.err
				bestIsHedge = res.isHedge
			} else if res.resp != nil {
				res.resp.Body.Close()
			}
			if bestErr == nil && res.err != nil {
				bestErr = res.err
			}
		case <-hedgeCtx.Done():
			if bestResp != nil {
				if bestIsHedge {
					h.metrics.HedgedWins.Add(1)
				}
				return bestResp, nil
			}
			return nil, hedgeCtx.Err()
		}
	}

	if bestIsHedge && bestResp != nil {
		h.metrics.HedgedWins.Add(1)
	}
	if bestResp != nil {
		return bestResp, nil
	}
	return nil, bestErr
}

func (h *HedgingExecutor) doRequest(
	ctx context.Context,
	transport http.RoundTripper,
	makeReq func(target *url.URL) (*http.Request, error),
	backendURL string,
	perTryTimeout time.Duration,
) (*http.Response, error) {
	target, err := url.Parse(backendURL)
	if err != nil {
		return nil, err
	}

	req, err := makeReq(target)
	if err != nil {
		return nil, err
	}

	if perTryTimeout > 0 {
		tryCtx, cancel := context.WithTimeout(ctx, perTryTimeout)
		defer cancel()
		return transport.RoundTrip(req.WithContext(tryCtx))
	}
	return transport.RoundTrip(req.WithContext(ctx))
}

// noBackendError indicates no backend was available.
type noBackendError struct{}

func (e *noBackendError) Error() string { return "no healthy backends available" }

// BufferBody reads r.Body into a byte slice and replaces r.Body with a new reader.
// This allows the body to be read multiple times for hedged requests.
func BufferBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}
