package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/wudi/gateway/config"
)

const defaultGeminiBaseURL = "https://generativelanguage.googleapis.com"

type geminiProvider struct {
	apiKey  string
	baseURL string
	model   string
}

func newGemini(cfg config.AIConfig) (Provider, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultGeminiBaseURL
	}
	return &geminiProvider{
		apiKey:  cfg.APIKey,
		baseURL: base,
		model:   cfg.Model,
	}, nil
}

func (g *geminiProvider) Name() string { return "gemini" }

// geminiRequest is the Gemini generateContent request format.
type geminiRequest struct {
	Contents         []geminiContent  `json:"contents"`
	SystemInstruct   *geminiContent   `json:"systemInstruction,omitempty"`
	GenerationConfig *geminiGenConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenConfig struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

// geminiResponse is the Gemini generateContent response format.
type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata geminiUsage       `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
	Index        int           `json:"index"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

func (gp *geminiProvider) BuildRequest(ctx context.Context, req *ChatRequest) (*http.Request, error) {
	model := req.Model
	if model == "" {
		model = gp.model
	}

	// Convert messages to Gemini format
	var system *geminiContent
	contents := make([]geminiContent, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			if system == nil {
				system = &geminiContent{Parts: []geminiPart{{Text: m.Content}}}
			} else {
				system.Parts = append(system.Parts, geminiPart{Text: m.Content})
			}
			continue
		}
		role := m.Role
		if role == "assistant" {
			role = "model" // Gemini uses "model" instead of "assistant"
		}
		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}

	greq := geminiRequest{
		Contents:       contents,
		SystemInstruct: system,
	}

	if req.MaxTokens > 0 || req.Temperature != nil || req.TopP != nil || len(req.Stop) > 0 {
		greq.GenerationConfig = &geminiGenConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     req.Temperature,
			TopP:            req.TopP,
			StopSequences:   req.Stop,
		}
	}

	body, err := json.Marshal(greq)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	action := "generateContent"
	if req.IsStreaming() {
		action = "streamGenerateContent?alt=sse"
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:%s", gp.baseURL, model, action)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", gp.apiKey)
	return httpReq, nil
}

func (gp *geminiProvider) ParseResponse(body []byte, statusCode int) (*ChatResponse, error) {
	if statusCode < 200 || statusCode >= 300 {
		return nil, &ProviderError{Status: statusCode, Body: body, Provider: "gemini"}
	}

	var gresp geminiResponse
	if err := json.Unmarshal(body, &gresp); err != nil {
		return nil, fmt.Errorf("gemini: parse response: %w", err)
	}

	choices := make([]Choice, len(gresp.Candidates))
	for i, c := range gresp.Candidates {
		var text string
		for _, p := range c.Content.Parts {
			text += p.Text
		}
		choices[i] = Choice{
			Index:        c.Index,
			Message:      Message{Role: "assistant", Content: text},
			FinishReason: mapGeminiFinishReason(c.FinishReason),
		}
	}

	return &ChatResponse{
		Object:  "chat.completion",
		Model:   gp.model,
		Choices: choices,
		Usage: Usage{
			PromptTokens:     gresp.UsageMetadata.PromptTokenCount,
			CompletionTokens: gresp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gresp.UsageMetadata.TotalTokenCount,
		},
	}, nil
}

func (gp *geminiProvider) ParseStreamEvent(eventType string, data []byte) (*StreamEvent, error) {
	// Gemini streams as JSON objects with candidates array (same structure as non-streaming)
	var gresp geminiResponse
	if err := json.Unmarshal(data, &gresp); err != nil {
		if string(data) == "[DONE]" || len(data) == 0 {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("gemini: parse stream event: %w", err)
	}

	if len(gresp.Candidates) == 0 {
		return nil, nil // metadata-only event
	}

	deltas := make([]StreamDelta, len(gresp.Candidates))
	for i, c := range gresp.Candidates {
		var text string
		for _, p := range c.Content.Parts {
			text += p.Text
		}
		deltas[i] = StreamDelta{
			Index:        c.Index,
			Delta:        DeltaContent{Content: text},
			FinishReason: mapGeminiFinishReason(c.FinishReason),
		}
	}

	evt := &StreamEvent{Choices: deltas}
	if gresp.UsageMetadata.TotalTokenCount > 0 {
		evt.Usage = &Usage{
			PromptTokens:     gresp.UsageMetadata.PromptTokenCount,
			CompletionTokens: gresp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gresp.UsageMetadata.TotalTokenCount,
		}
	}
	return evt, nil
}

func (gp *geminiProvider) SupportsStreaming() bool { return true }

func mapGeminiFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	default:
		return reason
	}
}
