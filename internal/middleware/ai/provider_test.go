package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/runway/config"
)

func TestOpenAIProvider_BuildRequest(t *testing.T) {
	p, _ := newOpenAI(config.AIConfig{APIKey: "sk-test", OrgID: "org-123", Model: "gpt-4o"})

	req := &ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	}

	httpReq, err := p.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if httpReq.URL.Path != "/v1/chat/completions" {
		t.Errorf("expected /v1/chat/completions, got %s", httpReq.URL.Path)
	}
	if httpReq.Header.Get("Authorization") != "Bearer sk-test" {
		t.Errorf("expected Bearer auth header")
	}
	if httpReq.Header.Get("OpenAI-Organization") != "org-123" {
		t.Errorf("expected org header")
	}
}

func TestOpenAIProvider_ParseResponse(t *testing.T) {
	p, _ := newOpenAI(config.AIConfig{APIKey: "sk-test"})

	body := `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`

	resp, err := p.ParseResponse([]byte(body), 200)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content != "Hi" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 6 {
		t.Errorf("expected 6 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestOpenAIProvider_ParseStreamEvent(t *testing.T) {
	p, _ := newOpenAI(config.AIConfig{APIKey: "sk-test"})

	// Normal event
	data := `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hello"}}]}`
	evt, err := p.ParseStreamEvent("", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if evt.Choices[0].Delta.Content != "Hello" {
		t.Errorf("unexpected delta content: %s", evt.Choices[0].Delta.Content)
	}

	// Done event
	_, err = p.ParseStreamEvent("", []byte("[DONE]"))
	if err != io.EOF {
		t.Errorf("expected io.EOF for [DONE], got %v", err)
	}
}

func TestAnthropicProvider_BuildRequest(t *testing.T) {
	p, _ := newAnthropic(config.AIConfig{APIKey: "sk-ant-test", Model: "claude-sonnet-4-6-20250514"})

	req := &ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: 100,
	}

	httpReq, err := p.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if httpReq.URL.Path != "/v1/messages" {
		t.Errorf("expected /v1/messages, got %s", httpReq.URL.Path)
	}
	if httpReq.Header.Get("x-api-key") != "sk-ant-test" {
		t.Errorf("expected x-api-key header")
	}
	if httpReq.Header.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("expected anthropic-version header")
	}

	// Verify system message was extracted
	body, _ := io.ReadAll(httpReq.Body)
	var areq anthropicRequest
	json.Unmarshal(body, &areq)

	if areq.System != "You are helpful." {
		t.Errorf("expected system message extracted, got: %s", areq.System)
	}
	if len(areq.Messages) != 1 {
		t.Errorf("expected 1 message (system extracted), got %d", len(areq.Messages))
	}
}

func TestAnthropicProvider_ParseResponse(t *testing.T) {
	p, _ := newAnthropic(config.AIConfig{APIKey: "sk-test"})

	body := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-6-20250514","content":[{"type":"text","text":"Hello!"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`

	resp, err := p.ParseResponse([]byte(body), 200)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content != "Hello!" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected stop, got: %s", resp.Choices[0].FinishReason)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestAnthropicProvider_ParseStreamEvents(t *testing.T) {
	p, _ := newAnthropic(config.AIConfig{APIKey: "sk-test"})

	// message_start → skip
	evt, err := p.ParseStreamEvent("message_start", []byte(`{"type":"message_start"}`))
	if err != nil || evt != nil {
		t.Errorf("expected skip for message_start")
	}

	// content_block_delta → content
	evt, err = p.ParseStreamEvent("content_block_delta", []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt.Choices[0].Delta.Content != "Hi" {
		t.Errorf("unexpected delta: %s", evt.Choices[0].Delta.Content)
	}

	// message_stop → EOF
	_, err = p.ParseStreamEvent("message_stop", []byte(`{"type":"message_stop"}`))
	if err != io.EOF {
		t.Errorf("expected io.EOF for message_stop")
	}
}

func TestAzureOpenAIProvider_BuildRequest(t *testing.T) {
	p, _ := newAzureOpenAI(config.AIConfig{
		APIKey:       "azure-key",
		BaseURL:      "https://myresource.openai.azure.com",
		DeploymentID: "my-gpt4",
		APIVersion:   "2024-02-01",
		Model:        "gpt-4",
	})

	req := &ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
	}

	httpReq, err := p.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	expectedPath := "/openai/deployments/my-gpt4/chat/completions"
	if httpReq.URL.Path != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, httpReq.URL.Path)
	}
	if httpReq.URL.Query().Get("api-version") != "2024-02-01" {
		t.Errorf("expected api-version query param")
	}
	if httpReq.Header.Get("api-key") != "azure-key" {
		t.Errorf("expected api-key header")
	}
}

func TestGeminiProvider_BuildRequest(t *testing.T) {
	p, _ := newGemini(config.AIConfig{APIKey: "gem-key", Model: "gemini-pro"})

	req := &ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "Be brief."},
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: 50,
	}

	httpReq, err := p.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if httpReq.URL.Path != "/v1beta/models/gemini-pro:generateContent" {
		t.Errorf("unexpected path: %s", httpReq.URL.Path)
	}
	if httpReq.Header.Get("x-goog-api-key") != "gem-key" {
		t.Errorf("expected x-goog-api-key header")
	}

	body, _ := io.ReadAll(httpReq.Body)
	var greq geminiRequest
	json.Unmarshal(body, &greq)

	if greq.SystemInstruct == nil {
		t.Fatal("expected system instruction")
	}
	if greq.SystemInstruct.Parts[0].Text != "Be brief." {
		t.Errorf("unexpected system instruction: %v", greq.SystemInstruct)
	}
	// "user" role should remain, "assistant" should become "model"
	if len(greq.Contents) != 1 || greq.Contents[0].Role != "user" {
		t.Errorf("unexpected contents: %+v", greq.Contents)
	}
}

func TestGeminiProvider_StreamingURL(t *testing.T) {
	p, _ := newGemini(config.AIConfig{APIKey: "gem-key", Model: "gemini-pro"})

	stream := true
	req := &ChatRequest{
		Messages: []Message{{Role: "user", Content: "Hello"}},
		Stream:   &stream,
	}

	httpReq, err := p.BuildRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	expected := "/v1beta/models/gemini-pro:streamGenerateContent"
	if httpReq.URL.Path != expected {
		t.Errorf("expected path %s, got %s", expected, httpReq.URL.Path)
	}
}

func TestGeminiProvider_ParseResponse(t *testing.T) {
	p, _ := newGemini(config.AIConfig{APIKey: "gem-key", Model: "gemini-pro"})

	body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello!"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}`

	resp, err := p.ParseResponse([]byte(body), 200)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Content != "Hello!" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected stop, got: %s", resp.Choices[0].FinishReason)
	}
	if resp.Usage.TotalTokens != 8 {
		t.Errorf("expected 8 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestAnthropicProvider_EndToEnd(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key header")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("expected anthropic-version header")
		}

		// Return Anthropic-format response
		resp := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-6-20250514","content":[{"type":"text","text":"Hi there!"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":4}}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(resp))
	}))
	defer mockServer.Close()

	cfg := config.AIConfig{
		Enabled:  true,
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6-20250514",
		APIKey:   "test-key",
		BaseURL:  mockServer.URL,
	}

	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"messages":[{"role":"system","content":"Be helpful"},{"role":"user","content":"Hello"}],"max_tokens":100}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ChatResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Choices[0].Message.Content != "Hi there!" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 12 {
		t.Errorf("expected 12 tokens, got %d", resp.Usage.TotalTokens)
	}
}
