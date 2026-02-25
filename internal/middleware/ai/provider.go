package ai

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/wudi/gateway/config"
)

// Provider translates between the unified chat format and a specific LLM provider.
type Provider interface {
	Name() string
	BuildRequest(ctx context.Context, req *ChatRequest) (*http.Request, error)
	ParseResponse(body []byte, statusCode int) (*ChatResponse, error)
	ParseStreamEvent(eventType string, data []byte) (*StreamEvent, error)
	SupportsStreaming() bool
}

// ChatRequest is the unified chat completion request (OpenAI-compatible).
type ChatRequest struct {
	Model       string    `json:"model,omitempty"`
	Messages    []Message `json:"messages"`
	Stream      *bool     `json:"stream,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	Stop        []string  `json:"stop,omitempty"`
	User        string    `json:"user,omitempty"`
}

// Message represents a chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse is the unified chat completion response.
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice is a single completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage reports token usage.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamEvent represents a single SSE event in the unified format.
type StreamEvent struct {
	ID      string        `json:"id,omitempty"`
	Object  string        `json:"object,omitempty"`
	Model   string        `json:"model,omitempty"`
	Choices []StreamDelta `json:"choices,omitempty"`
	Usage   *Usage        `json:"usage,omitempty"` // final event may carry usage
}

// StreamDelta is a single streaming choice delta.
type StreamDelta struct {
	Index        int          `json:"index"`
	Delta        DeltaContent `json:"delta"`
	FinishReason string       `json:"finish_reason,omitempty"`
}

// DeltaContent is the content portion of a streaming delta.
type DeltaContent struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// IsStreaming returns true if the request has streaming enabled.
func (r *ChatRequest) IsStreaming() bool {
	return r.Stream != nil && *r.Stream
}

// AllText returns concatenated content from all messages.
func (r *ChatRequest) AllText() string {
	var total int
	for _, m := range r.Messages {
		total += len(m.Content)
	}
	buf := make([]byte, 0, total)
	for _, m := range r.Messages {
		buf = append(buf, m.Content...)
	}
	return string(buf)
}

// --- Context helpers ---

type ctxKey int

const (
	ctxChatRequest ctxKey = iota
	ctxTokenCallback
)

// SetChatRequest stores a parsed ChatRequest in the context.
func SetChatRequest(ctx context.Context, req *ChatRequest) context.Context {
	return context.WithValue(ctx, ctxChatRequest, req)
}

// GetChatRequest retrieves a parsed ChatRequest from the context.
func GetChatRequest(ctx context.Context) *ChatRequest {
	v, _ := ctx.Value(ctxChatRequest).(*ChatRequest)
	return v
}

// SetTokenCallback stores an atomic counter in the context for the handler to report actual tokens.
func SetTokenCallback(ctx context.Context, tokens *atomic.Int64) context.Context {
	return context.WithValue(ctx, ctxTokenCallback, tokens)
}

// GetTokenCallback retrieves the token callback from the context.
func GetTokenCallback(ctx context.Context) *atomic.Int64 {
	v, _ := ctx.Value(ctxTokenCallback).(*atomic.Int64)
	return v
}

// --- Provider registry ---

var providers = map[string]func(cfg config.AIConfig) (Provider, error){
	"openai":       newOpenAI,
	"anthropic":    newAnthropic,
	"azure_openai": newAzureOpenAI,
	"gemini":       newGemini,
}

// NewProvider creates a provider from config.
func NewProvider(cfg config.AIConfig) (Provider, error) {
	fn, ok := providers[cfg.Provider]
	if !ok {
		return nil, io.ErrUnexpectedEOF // unreachable after validation
	}
	return fn(cfg)
}
