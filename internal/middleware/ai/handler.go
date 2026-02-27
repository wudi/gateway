package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/internal/middleware"
)

const (
	defaultTimeout     = 60 * time.Second
	defaultIdleTimeout = 30 * time.Second
	defaultMaxBodySize = 10 * 1024 * 1024 // 10MB
)

// AIHandler implements http.Handler for AI proxy requests.
type AIHandler struct {
	provider      Provider
	client        *http.Client
	cfg           config.AIConfig
	timeout       time.Duration
	idleTimeout   time.Duration
	maxBodySize   int64
	modelMapping  map[string]string
	passHeaders   []string

	// Metrics (atomic, lock-free)
	totalRequests      atomic.Int64
	streamingRequests  atomic.Int64
	nonStreamRequests  atomic.Int64
	totalTokensIn      atomic.Int64
	totalTokensOut     atomic.Int64
	totalErrors        atomic.Int64
	latencySumMS       atomic.Int64
}

// New creates a new AIHandler from config.
func New(cfg config.AIConfig) (*AIHandler, error) {
	provider, err := NewProvider(cfg)
	if err != nil {
		return nil, err
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = defaultIdleTimeout
	}
	maxBody := cfg.MaxBodySize
	if maxBody == 0 {
		maxBody = defaultMaxBodySize
	}

	return &AIHandler{
		provider:     provider,
		client:       &http.Client{Timeout: 0}, // timeout handled per-request via context
		cfg:          cfg,
		timeout:      timeout,
		idleTimeout:  idleTimeout,
		maxBodySize:  maxBody,
		modelMapping: cfg.ModelMapping,
		passHeaders:  cfg.PassHeaders,
	}, nil
}

func (h *AIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	h.totalRequests.Add(1)

	// 1. Get or parse ChatRequest
	chatReq := GetChatRequest(r.Context())
	if chatReq == nil {
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json", h.provider.Name())
			h.totalErrors.Add(1)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, h.maxBodySize))
		if err != nil {
			writeError(w, http.StatusBadRequest, "read_error", "failed to read request body", h.provider.Name())
			h.totalErrors.Add(1)
			return
		}
		var req ChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "parse_error", "invalid JSON request body", h.provider.Name())
			h.totalErrors.Add(1)
			return
		}
		chatReq = &req
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	// 2. Model mapping
	if h.modelMapping != nil {
		if mapped, ok := h.modelMapping[chatReq.Model]; ok {
			chatReq.Model = mapped
		}
	}

	// 3. Max tokens cap
	if h.cfg.MaxTokens > 0 && (chatReq.MaxTokens == 0 || chatReq.MaxTokens > h.cfg.MaxTokens) {
		chatReq.MaxTokens = h.cfg.MaxTokens
	}

	// 4. Temperature override
	if h.cfg.Temperature != nil {
		chatReq.Temperature = h.cfg.Temperature
	}

	// 5. Stream default
	if chatReq.Stream == nil && h.cfg.StreamDefault {
		t := true
		chatReq.Stream = &t
	}

	// 6. Timeout context
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	// 7. Build provider request
	providerReq, err := h.provider.BuildRequest(ctx, chatReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_error", fmt.Sprintf("failed to build provider request: %v", err), h.provider.Name())
		h.totalErrors.Add(1)
		return
	}

	// 8. Copy pass_headers
	for _, hdr := range h.passHeaders {
		if v := r.Header.Get(hdr); v != "" {
			providerReq.Header.Set(hdr, v)
		}
	}

	// Set common response headers early
	w.Header().Set("X-AI-Provider", h.provider.Name())
	model := chatReq.Model
	if model == "" {
		model = h.cfg.Model
	}
	w.Header().Set("X-AI-Model", model)

	if chatReq.IsStreaming() {
		h.handleStreaming(w, providerReq, start)
	} else {
		h.handleNonStreaming(w, providerReq, start)
	}
}

func (h *AIHandler) handleStreaming(w http.ResponseWriter, providerReq *http.Request, start time.Time) {
	h.streamingRequests.Add(1)

	resp, err := h.client.Do(providerReq)
	if err != nil {
		h.totalErrors.Add(1)
		statusCode := mapNetworkError(err)
		writeError(w, statusCode, errorTypeFromStatus(statusCode), err.Error(), h.provider.Name())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		h.totalErrors.Add(1)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		mapped := mapProviderStatusCode(resp.StatusCode)
		writeError(w, mapped, errorTypeFromProviderStatus(resp.StatusCode), string(body), h.provider.Name())
		return
	}

	usage, streamErr := streamResponse(w, resp, h.provider, h.idleTimeout)
	if streamErr != nil {
		h.totalErrors.Add(1)
	}

	if usage != nil {
		h.totalTokensIn.Add(int64(usage.PromptTokens))
		h.totalTokensOut.Add(int64(usage.CompletionTokens))
		if cb := GetTokenCallback(providerReq.Context()); cb != nil {
			cb.Store(int64(usage.TotalTokens))
		}
	}

	h.latencySumMS.Add(time.Since(start).Milliseconds())
}

func (h *AIHandler) handleNonStreaming(w http.ResponseWriter, providerReq *http.Request, start time.Time) {
	h.nonStreamRequests.Add(1)

	resp, err := h.client.Do(providerReq)
	if err != nil {
		h.totalErrors.Add(1)
		statusCode := mapNetworkError(err)
		writeError(w, statusCode, errorTypeFromStatus(statusCode), err.Error(), h.provider.Name())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		h.totalErrors.Add(1)
		writeError(w, http.StatusBadGateway, "provider_error", "failed to read provider response", h.provider.Name())
		return
	}

	chatResp, err := h.provider.ParseResponse(body, resp.StatusCode)
	if err != nil {
		h.totalErrors.Add(1)
		if pe, ok := err.(*ProviderError); ok {
			mapped := mapProviderStatusCode(pe.Status)
			// Forward Retry-After for 429
			if pe.Status == 429 {
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					w.Header().Set("Retry-After", ra)
				}
			}
			writeError(w, mapped, errorTypeFromProviderStatus(pe.Status), string(pe.Body), pe.Provider)
		} else {
			writeError(w, http.StatusBadGateway, "provider_parse_error", err.Error(), h.provider.Name())
		}
		return
	}

	// Set token headers
	w.Header().Set("X-AI-Tokens-Input", strconv.Itoa(chatResp.Usage.PromptTokens))
	w.Header().Set("X-AI-Tokens-Output", strconv.Itoa(chatResp.Usage.CompletionTokens))
	w.Header().Set("X-AI-Tokens-Total", strconv.Itoa(chatResp.Usage.TotalTokens))

	h.totalTokensIn.Add(int64(chatResp.Usage.PromptTokens))
	h.totalTokensOut.Add(int64(chatResp.Usage.CompletionTokens))

	if cb := GetTokenCallback(providerReq.Context()); cb != nil {
		cb.Store(int64(chatResp.Usage.TotalTokens))
	}

	h.latencySumMS.Add(time.Since(start).Milliseconds())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(chatResp)
}

// Stats returns handler statistics.
func (h *AIHandler) Stats() map[string]any {
	stats := map[string]any{
		"provider":              h.provider.Name(),
		"model":                 h.cfg.Model,
		"total_requests":        h.totalRequests.Load(),
		"streaming_requests":    h.streamingRequests.Load(),
		"non_streaming_requests": h.nonStreamRequests.Load(),
		"total_tokens_in":       h.totalTokensIn.Load(),
		"total_tokens_out":      h.totalTokensOut.Load(),
		"total_errors":          h.totalErrors.Load(),
		"latency_sum_ms":        h.latencySumMS.Load(),
	}
	return stats
}

// Middleware returns the AI prompt guard middleware for this handler.
// (The handler itself is the innermost handler, not a middleware.)
func (h *AIHandler) PromptGuardMiddleware() middleware.Middleware {
	guard := NewPromptGuard(h.cfg.PromptGuard, h.maxBodySize)
	if guard == nil {
		return nil
	}
	return guard.Middleware()
}

// PromptDecorateMiddleware returns the prompt decoration middleware.
func (h *AIHandler) PromptDecorateMiddleware() middleware.Middleware {
	dec := NewPromptDecorator(h.cfg.PromptDecorate, h.maxBodySize)
	if dec == nil {
		return nil
	}
	return dec.Middleware()
}

// AIRateLimitMiddleware returns the AI token rate limit middleware.
func (h *AIHandler) AIRateLimitMiddleware() middleware.Middleware {
	rl := NewAIRateLimiter(h.cfg.RateLimit)
	if rl == nil {
		return nil
	}
	return rl.Middleware()
}

// --- AIByRoute ---

// AIByRoute manages per-route AI handlers.
type AIByRoute = byroute.Factory[*AIHandler, config.AIConfig]

// NewAIByRoute creates a new per-route AI handler manager.
func NewAIByRoute() *AIByRoute {
	return byroute.NewFactory(New, func(h *AIHandler) any { return h.Stats() })
}

// --- Error helpers ---

func writeError(w http.ResponseWriter, status int, errType, message, provider string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"type":     errType,
			"message":  message,
			"provider": provider,
		},
	})
}

func mapNetworkError(err error) int {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

func mapProviderStatusCode(status int) int {
	switch {
	case status == 401 || status == 403:
		return http.StatusBadGateway
	case status == 429:
		return http.StatusTooManyRequests
	case status >= 500:
		return http.StatusBadGateway
	default:
		return http.StatusBadGateway
	}
}

func errorTypeFromStatus(status int) string {
	if status == http.StatusGatewayTimeout {
		return "gateway_timeout"
	}
	return "provider_error"
}

func errorTypeFromProviderStatus(status int) string {
	switch {
	case status == 401 || status == 403:
		return "provider_auth_error"
	case status == 429:
		return "rate_limit_exceeded"
	default:
		return "provider_error"
	}
}
