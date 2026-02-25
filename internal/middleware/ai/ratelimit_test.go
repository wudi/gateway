package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/gateway/config"
)

func TestAIRateLimiter_AllowsUnderBudget(t *testing.T) {
	rl := NewAIRateLimiter(config.AIRateLimitConfig{
		TokensPerMinute: 100000,
		Key:             "ip",
	})

	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// Simulate AI handler storing actual tokens
		if cb := GetTokenCallback(r.Context()); cb != nil {
			cb.Store(50)
		}
		w.WriteHeader(http.StatusOK)
	})

	mw := rl.Middleware()(next)

	chatReq := &ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello world"}},
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(SetChatRequest(req.Context(), chatReq))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if !called {
		t.Error("expected next handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAIRateLimiter_RejectsOverBudget(t *testing.T) {
	rl := NewAIRateLimiter(config.AIRateLimitConfig{
		TokensPerMinute: 10, // very small budget
		Key:             "ip",
	})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := rl.Middleware()(next)

	// Request with many words → estimate exceeds budget
	chatReq := &ChatRequest{
		Messages: []Message{{Role: "user", Content: "This is a long prompt with many words that should exceed the token budget easily when estimated using word count heuristic"}},
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(SetChatRequest(req.Context(), chatReq))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}

	var errResp map[string]any
	json.NewDecoder(rec.Body).Decode(&errResp)
	errObj := errResp["error"].(map[string]any)
	if errObj["type"] != "token_rate_limit" {
		t.Errorf("expected token_rate_limit, got %s", errObj["type"])
	}

	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestAIRateLimiter_DailyBudget(t *testing.T) {
	rl := NewAIRateLimiter(config.AIRateLimitConfig{
		TokensPerDay: 5, // very small
		Key:          "ip",
	})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := rl.Middleware()(next)

	chatReq := &ChatRequest{
		Messages: []Message{{Role: "user", Content: "This message has several words that exceed the tiny daily budget"}},
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(SetChatRequest(req.Context(), chatReq))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}

	var errResp map[string]any
	json.NewDecoder(rec.Body).Decode(&errResp)
	errObj := errResp["error"].(map[string]any)
	if !strings.Contains(errObj["message"].(string), "per-day") {
		t.Errorf("expected per-day message, got: %s", errObj["message"])
	}
}

func TestAIRateLimiter_TokenCorrection(t *testing.T) {
	rl := NewAIRateLimiter(config.AIRateLimitConfig{
		TokensPerMinute: 1000,
		Key:             "ip",
	})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cb := GetTokenCallback(r.Context()); cb != nil {
			cb.Store(100)
		}
		w.WriteHeader(http.StatusOK)
	})

	mw := rl.Middleware()(next)

	chatReq := &ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(SetChatRequest(req.Context(), chatReq))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Verify the window was corrected
	rl.mu.Lock()
	for _, win := range rl.windows {
		// Should have actual 100 tokens, not the estimate
		if win.minuteTokens != 100 {
			t.Errorf("expected 100 tokens after correction, got %d", win.minuteTokens)
		}
	}
	rl.mu.Unlock()
}

func TestAIRateLimiter_NilWhenNotConfigured(t *testing.T) {
	rl := NewAIRateLimiter(config.AIRateLimitConfig{})
	if rl != nil {
		t.Error("expected nil when no rate limit config")
	}
}

func TestAIRateLimiter_Stats(t *testing.T) {
	rl := NewAIRateLimiter(config.AIRateLimitConfig{
		TokensPerMinute: 100000,
	})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := rl.Middleware()(next)

	chatReq := &ChatRequest{Messages: []Message{{Role: "user", Content: "Hi"}}}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(SetChatRequest(req.Context(), chatReq))
	mw.ServeHTTP(httptest.NewRecorder(), req)

	stats := rl.Stats()
	if stats["checked"].(int64) != 1 {
		t.Errorf("expected 1 checked, got %v", stats["checked"])
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()

	// ChatRequest
	if GetChatRequest(ctx) != nil {
		t.Error("expected nil for empty context")
	}
	req := &ChatRequest{Model: "gpt-4o"}
	ctx = SetChatRequest(ctx, req)
	if got := GetChatRequest(ctx); got != req {
		t.Error("expected same ChatRequest back")
	}

	// TokenCallback
	if GetTokenCallback(ctx) != nil {
		t.Error("expected nil for no callback")
	}
}
