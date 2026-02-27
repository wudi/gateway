package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/runway/config"
)

func TestAIHandler_NonStreaming(t *testing.T) {
	// Mock OpenAI server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Authorization header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json")
		}

		// Read and verify body
		body, _ := io.ReadAll(r.Body)
		var req ChatRequest
		json.Unmarshal(body, &req)

		if req.Model != "gpt-4o" {
			t.Errorf("expected model gpt-4o, got %s", req.Model)
		}

		// Return mock response
		resp := ChatResponse{
			ID:     "chatcmpl-123",
			Object: "chat.completion",
			Model:  "gpt-4o",
			Choices: []Choice{{
				Index:        0,
				Message:      Message{Role: "assistant", Content: "Hello! How can I help you?"},
				FinishReason: "stop",
			}},
			Usage: Usage{
				PromptTokens:     10,
				CompletionTokens: 8,
				TotalTokens:      18,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	cfg := config.AIConfig{
		Enabled:  true,
		Provider: "openai",
		Model:    "gpt-4o",
		APIKey:   "test-key",
		BaseURL:  mockServer.URL,
	}

	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify response headers
	if rec.Header().Get("X-AI-Provider") != "openai" {
		t.Errorf("expected X-AI-Provider: openai, got %s", rec.Header().Get("X-AI-Provider"))
	}
	if rec.Header().Get("X-AI-Model") != "gpt-4o" {
		t.Errorf("expected X-AI-Model: gpt-4o, got %s", rec.Header().Get("X-AI-Model"))
	}
	if rec.Header().Get("X-AI-Tokens-Total") != "18" {
		t.Errorf("expected X-AI-Tokens-Total: 18, got %s", rec.Header().Get("X-AI-Tokens-Total"))
	}

	var resp ChatResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Choices[0].Message.Content != "Hello! How can I help you?" {
		t.Errorf("unexpected response content: %s", resp.Choices[0].Message.Content)
	}
}

func TestAIHandler_Streaming(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ChatRequest
		json.Unmarshal(body, &req)

		if !req.IsStreaming() {
			t.Error("expected streaming request")
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		// Send SSE events
		events := []string{
			`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
			`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
			`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"!"}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer mockServer.Close()

	cfg := config.AIConfig{
		Enabled:  true,
		Provider: "openai",
		Model:    "gpt-4o",
		APIKey:   "test-key",
		BaseURL:  mockServer.URL,
	}

	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", rec.Header().Get("Content-Type"))
	}

	respBody := rec.Body.String()
	if !strings.Contains(respBody, `"content":"Hello"`) {
		t.Errorf("expected Hello in stream, got: %s", respBody)
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Errorf("expected [DONE] in stream")
	}
}

func TestAIHandler_ModelMapping(t *testing.T) {
	var receivedModel string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ChatRequest
		json.Unmarshal(body, &req)
		receivedModel = req.Model

		json.NewEncoder(w).Encode(ChatResponse{
			Choices: []Choice{{Message: Message{Content: "ok"}}},
		})
	}))
	defer mockServer.Close()

	cfg := config.AIConfig{
		Enabled:  true,
		Provider: "openai",
		Model:    "gpt-4o",
		APIKey:   "test-key",
		BaseURL:  mockServer.URL,
		ModelMapping: map[string]string{
			"cheap": "gpt-4o-mini",
		},
	}

	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"model":"cheap","messages":[{"role":"user","content":"Hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if receivedModel != "gpt-4o-mini" {
		t.Errorf("expected model gpt-4o-mini after mapping, got %s", receivedModel)
	}
}

func TestAIHandler_MaxTokensCap(t *testing.T) {
	var receivedMaxTokens int
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ChatRequest
		json.Unmarshal(body, &req)
		receivedMaxTokens = req.MaxTokens

		json.NewEncoder(w).Encode(ChatResponse{
			Choices: []Choice{{Message: Message{Content: "ok"}}},
		})
	}))
	defer mockServer.Close()

	cfg := config.AIConfig{
		Enabled:   true,
		Provider:  "openai",
		Model:     "gpt-4o",
		APIKey:    "test-key",
		BaseURL:   mockServer.URL,
		MaxTokens: 100,
	}

	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Client requests 500 tokens, cap is 100
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"max_tokens":500}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if receivedMaxTokens != 100 {
		t.Errorf("expected max_tokens capped to 100, got %d", receivedMaxTokens)
	}
}

func TestAIHandler_UnsupportedMediaType(t *testing.T) {
	cfg := config.AIConfig{
		Enabled:  true,
		Provider: "openai",
		Model:    "gpt-4o",
		APIKey:   "test-key",
	}

	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", rec.Code)
	}
}

func TestAIHandler_ProviderError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error": {"message": "invalid api key"}}`))
	}))
	defer mockServer.Close()

	cfg := config.AIConfig{
		Enabled:  true,
		Provider: "openai",
		Model:    "gpt-4o",
		APIKey:   "bad-key",
		BaseURL:  mockServer.URL,
	}

	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Provider 401 maps to 502
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}

	var errResp map[string]any
	json.NewDecoder(rec.Body).Decode(&errResp)
	errObj := errResp["error"].(map[string]any)
	if errObj["type"] != "provider_auth_error" {
		t.Errorf("expected provider_auth_error, got %s", errObj["type"])
	}
}

func TestAIHandler_Stats(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ChatResponse{
			Choices: []Choice{{Message: Message{Content: "ok"}}},
			Usage:   Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		})
	}))
	defer mockServer.Close()

	cfg := config.AIConfig{
		Enabled:  true,
		Provider: "openai",
		Model:    "gpt-4o",
		APIKey:   "test-key",
		BaseURL:  mockServer.URL,
	}

	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"messages":[{"role":"user","content":"Hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	stats := handler.Stats()
	if stats["total_requests"].(int64) != 1 {
		t.Errorf("expected 1 request, got %v", stats["total_requests"])
	}
	if stats["non_streaming_requests"].(int64) != 1 {
		t.Errorf("expected 1 non-streaming request, got %v", stats["non_streaming_requests"])
	}
	if stats["total_tokens_in"].(int64) != 5 {
		t.Errorf("expected 5 tokens in, got %v", stats["total_tokens_in"])
	}
}

func TestAIByRoute(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ChatResponse{})
	}))
	defer mockServer.Close()

	mgr := NewAIByRoute()
	err := mgr.AddRoute("route-1", config.AIConfig{
		Enabled:  true,
		Provider: "openai",
		Model:    "gpt-4o",
		APIKey:   "test-key",
		BaseURL:  mockServer.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	h := mgr.Lookup("route-1")
	if h == nil {
		t.Fatal("expected handler for route-1")
	}
	if mgr.Lookup("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := mgr.RouteIDs()
	if len(ids) != 1 || ids[0] != "route-1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}
}
