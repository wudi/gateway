package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wudi/runway/config"
)

type azureOpenAIProvider struct {
	apiKey       string
	baseURL      string
	deploymentID string
	apiVersion   string
	model        string
}

func newAzureOpenAI(cfg config.AIConfig) (Provider, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	return &azureOpenAIProvider{
		apiKey:       cfg.APIKey,
		baseURL:      base,
		deploymentID: cfg.DeploymentID,
		apiVersion:   cfg.APIVersion,
		model:        cfg.Model,
	}, nil
}

func (az *azureOpenAIProvider) Name() string { return "azure_openai" }

func (az *azureOpenAIProvider) BuildRequest(ctx context.Context, req *ChatRequest) (*http.Request, error) {
	if req.Model == "" {
		req.Model = az.model
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("azure_openai: marshal request: %w", err)
	}

	// Azure uses: {base}/openai/deployments/{deployment}/chat/completions?api-version={version}
	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		az.baseURL, az.deploymentID, az.apiVersion)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-key", az.apiKey)
	return httpReq, nil
}

func (az *azureOpenAIProvider) ParseResponse(body []byte, statusCode int) (*ChatResponse, error) {
	if statusCode < 200 || statusCode >= 300 {
		return nil, &ProviderError{Status: statusCode, Body: body, Provider: "azure_openai"}
	}
	// Azure returns OpenAI-compatible JSON
	var resp ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("azure_openai: parse response: %w", err)
	}
	return &resp, nil
}

func (az *azureOpenAIProvider) ParseStreamEvent(eventType string, data []byte) (*StreamEvent, error) {
	// Azure uses the same SSE format as OpenAI
	if string(data) == "[DONE]" {
		return nil, io.EOF
	}
	var evt StreamEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return nil, fmt.Errorf("azure_openai: parse stream event: %w", err)
	}
	return &evt, nil
}

func (az *azureOpenAIProvider) SupportsStreaming() bool { return true }
