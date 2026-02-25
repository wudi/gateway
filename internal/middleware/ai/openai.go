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

const defaultOpenAIBaseURL = "https://api.openai.com"

type openaiProvider struct {
	apiKey  string
	baseURL string
	orgID   string
	model   string
}

func newOpenAI(cfg config.AIConfig) (Provider, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultOpenAIBaseURL
	}
	return &openaiProvider{
		apiKey:  cfg.APIKey,
		baseURL: base,
		orgID:   cfg.OrgID,
		model:   cfg.Model,
	}, nil
}

func (o *openaiProvider) Name() string { return "openai" }

func (o *openaiProvider) BuildRequest(ctx context.Context, req *ChatRequest) (*http.Request, error) {
	if req.Model == "" {
		req.Model = o.model
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	if o.orgID != "" {
		httpReq.Header.Set("OpenAI-Organization", o.orgID)
	}
	return httpReq, nil
}

func (o *openaiProvider) ParseResponse(body []byte, statusCode int) (*ChatResponse, error) {
	if statusCode < 200 || statusCode >= 300 {
		return nil, &ProviderError{Status: statusCode, Body: body, Provider: "openai"}
	}
	var resp ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("openai: parse response: %w", err)
	}
	return &resp, nil
}

func (o *openaiProvider) ParseStreamEvent(eventType string, data []byte) (*StreamEvent, error) {
	if string(data) == "[DONE]" {
		return nil, io.EOF
	}
	var evt StreamEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return nil, fmt.Errorf("openai: parse stream event: %w", err)
	}
	return &evt, nil
}

func (o *openaiProvider) SupportsStreaming() bool { return true }

// ProviderError represents an error response from a provider.
type ProviderError struct {
	Status   int
	Body     []byte
	Provider string
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, string(e.Body))
}
