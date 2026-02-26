package ai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/runway/config"
)

func TestPromptDecorator_Prepend(t *testing.T) {
	dec := NewPromptDecorator(config.AIPromptDecorateConfig{
		Prepend: []config.AIPromptMessage{
			{Role: "system", Content: "You are a helpful assistant."},
		},
	}, 0)

	var ctxReq *ChatRequest
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxReq = GetChatRequest(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mw := dec.Middleware()(next)

	body := `{"messages":[{"role":"user","content":"Hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if ctxReq == nil {
		t.Fatal("expected ChatRequest in context")
	}
	if len(ctxReq.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(ctxReq.Messages))
	}
	if ctxReq.Messages[0].Role != "system" || ctxReq.Messages[0].Content != "You are a helpful assistant." {
		t.Errorf("unexpected first message: %+v", ctxReq.Messages[0])
	}
	if ctxReq.Messages[1].Role != "user" || ctxReq.Messages[1].Content != "Hello" {
		t.Errorf("unexpected second message: %+v", ctxReq.Messages[1])
	}
}

func TestPromptDecorator_Append(t *testing.T) {
	dec := NewPromptDecorator(config.AIPromptDecorateConfig{
		Append: []config.AIPromptMessage{
			{Role: "system", Content: "Always be concise."},
		},
	}, 0)

	var ctxReq *ChatRequest
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxReq = GetChatRequest(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mw := dec.Middleware()(next)

	body := `{"messages":[{"role":"user","content":"Hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if ctxReq == nil {
		t.Fatal("expected ChatRequest in context")
	}
	if len(ctxReq.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(ctxReq.Messages))
	}
	if ctxReq.Messages[1].Content != "Always be concise." {
		t.Errorf("unexpected last message: %+v", ctxReq.Messages[1])
	}
}

func TestPromptDecorator_PrependAndAppend(t *testing.T) {
	dec := NewPromptDecorator(config.AIPromptDecorateConfig{
		Prepend: []config.AIPromptMessage{
			{Role: "system", Content: "System prompt"},
		},
		Append: []config.AIPromptMessage{
			{Role: "user", Content: "Extra context"},
		},
	}, 0)

	var ctxReq *ChatRequest
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxReq = GetChatRequest(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mw := dec.Middleware()(next)

	body := `{"messages":[{"role":"user","content":"Hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if len(ctxReq.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(ctxReq.Messages))
	}
	if ctxReq.Messages[0].Content != "System prompt" {
		t.Errorf("unexpected first: %s", ctxReq.Messages[0].Content)
	}
	if ctxReq.Messages[1].Content != "Hello" {
		t.Errorf("unexpected middle: %s", ctxReq.Messages[1].Content)
	}
	if ctxReq.Messages[2].Content != "Extra context" {
		t.Errorf("unexpected last: %s", ctxReq.Messages[2].Content)
	}
}

func TestPromptDecorator_WithExistingContext(t *testing.T) {
	// Simulate guard already parsed the body
	dec := NewPromptDecorator(config.AIPromptDecorateConfig{
		Prepend: []config.AIPromptMessage{
			{Role: "system", Content: "Prepended"},
		},
	}, 0)

	var ctxReq *ChatRequest
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxReq = GetChatRequest(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mw := dec.Middleware()(next)

	// Pre-set ChatRequest in context (simulating guard)
	existingReq := &ChatRequest{
		Messages: []Message{{Role: "user", Content: "Pre-parsed"}},
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(SetChatRequest(req.Context(), existingReq))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if len(ctxReq.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(ctxReq.Messages))
	}
	if ctxReq.Messages[0].Content != "Prepended" {
		t.Errorf("expected prepended message first")
	}
}

func TestPromptDecorator_NilWhenNotConfigured(t *testing.T) {
	dec := NewPromptDecorator(config.AIPromptDecorateConfig{}, 0)
	if dec != nil {
		t.Error("expected nil when no decoration config")
	}
}
