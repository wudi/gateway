package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/wudi/runway/config"
)

const defaultAnthropicBaseURL = "https://api.anthropic.com"

type anthropicProvider struct {
	apiKey  string
	baseURL string
	model   string
}

func newAnthropic(cfg config.AIConfig) (Provider, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultAnthropicBaseURL
	}
	return &anthropicProvider{
		apiKey:  cfg.APIKey,
		baseURL: base,
		model:   cfg.Model,
	}, nil
}

func (a *anthropicProvider) Name() string { return "anthropic" }

// anthropicRequest is the Anthropic Messages API request format.
type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream,omitempty"`
	Metadata  *anthropicMeta     `json:"metadata,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicMeta struct {
	UserID string `json:"user_id,omitempty"`
}

// anthropicResponse is the Anthropic Messages API response format.
type anthropicResponse struct {
	ID      string             `json:"id"`
	Type    string             `json:"type"`
	Role    string             `json:"role"`
	Model   string             `json:"model"`
	Content []anthropicContent `json:"content"`
	Usage   anthropicUsage     `json:"usage"`
	Stop    string             `json:"stop_reason"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (a *anthropicProvider) BuildRequest(ctx context.Context, req *ChatRequest) (*http.Request, error) {
	model := req.Model
	if model == "" {
		model = a.model
	}

	// Extract system message and convert remaining to Anthropic format
	var system string
	msgs := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
			continue
		}
		msgs = append(msgs, anthropicMessage{Role: m.Role, Content: m.Content})
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096 // Anthropic requires max_tokens
	}

	areq := anthropicRequest{
		Model:     model,
		Messages:  msgs,
		System:    system,
		MaxTokens: maxTokens,
		Stream:    req.IsStreaming(),
	}
	if req.User != "" {
		areq.Metadata = &anthropicMeta{UserID: req.User}
	}

	body, err := json.Marshal(areq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	return httpReq, nil
}

func (a *anthropicProvider) ParseResponse(body []byte, statusCode int) (*ChatResponse, error) {
	if statusCode < 200 || statusCode >= 300 {
		return nil, &ProviderError{Status: statusCode, Body: body, Provider: "anthropic"}
	}
	var aresp anthropicResponse
	if err := json.Unmarshal(body, &aresp); err != nil {
		return nil, fmt.Errorf("anthropic: parse response: %w", err)
	}

	// Convert to unified format
	var content string
	for _, c := range aresp.Content {
		if c.Type == "text" {
			content += c.Text
		}
	}

	return &ChatResponse{
		ID:     aresp.ID,
		Object: "chat.completion",
		Model:  aresp.Model,
		Choices: []Choice{{
			Index:        0,
			Message:      Message{Role: aresp.Role, Content: content},
			FinishReason: mapAnthropicStopReason(aresp.Stop),
		}},
		Usage: Usage{
			PromptTokens:     aresp.Usage.InputTokens,
			CompletionTokens: aresp.Usage.OutputTokens,
			TotalTokens:       aresp.Usage.InputTokens + aresp.Usage.OutputTokens,
		},
	}, nil
}

// anthropicStreamEvent represents a raw Anthropic SSE event.
type anthropicStreamEvent struct {
	Type  string          `json:"type"`
	Delta json.RawMessage `json:"delta,omitempty"`
	Usage json.RawMessage `json:"usage,omitempty"`
	Index int             `json:"index"`
}

type anthropicDelta struct {
	Type       string `json:"type"`
	Text       string `json:"text"`
	StopReason string `json:"stop_reason,omitempty"`
}

func (a *anthropicProvider) ParseStreamEvent(eventType string, data []byte) (*StreamEvent, error) {
	// Anthropic uses event: type lines, but data is JSON
	var raw anthropicStreamEvent
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("anthropic: parse stream event: %w", err)
	}

	switch raw.Type {
	case "message_stop":
		return nil, io.EOF

	case "content_block_delta":
		var delta anthropicDelta
		if err := json.Unmarshal(raw.Delta, &delta); err != nil {
			return nil, fmt.Errorf("anthropic: parse delta: %w", err)
		}
		return &StreamEvent{
			Choices: []StreamDelta{{
				Index: raw.Index,
				Delta: DeltaContent{Content: delta.Text},
			}},
		}, nil

	case "message_delta":
		var delta anthropicDelta
		if err := json.Unmarshal(raw.Delta, &delta); err != nil {
			return nil, fmt.Errorf("anthropic: parse message_delta: %w", err)
		}
		evt := &StreamEvent{
			Choices: []StreamDelta{{
				Index:        0,
				FinishReason: mapAnthropicStopReason(delta.StopReason),
			}},
		}
		if len(raw.Usage) > 0 {
			var usage anthropicUsage
			if err := json.Unmarshal(raw.Usage, &usage); err == nil {
				evt.Usage = &Usage{
					PromptTokens:     usage.InputTokens,
					CompletionTokens: usage.OutputTokens,
					TotalTokens:      usage.InputTokens + usage.OutputTokens,
				}
			}
		}
		return evt, nil

	default:
		// message_start, content_block_start, ping — skip
		return nil, nil
	}
}

func (a *anthropicProvider) SupportsStreaming() bool { return true }

func mapAnthropicStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	default:
		return reason
	}
}
