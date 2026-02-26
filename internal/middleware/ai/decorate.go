package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
)

// PromptDecorator prepends/appends messages to chat requests.
type PromptDecorator struct {
	prepend     []Message
	append      []Message
	maxBodySize int64
}

// NewPromptDecorator creates a PromptDecorator from config. Returns nil if no decoration is configured.
func NewPromptDecorator(cfg config.AIPromptDecorateConfig, maxBodySize int64) *PromptDecorator {
	if len(cfg.Prepend) == 0 && len(cfg.Append) == 0 {
		return nil
	}

	pre := make([]Message, len(cfg.Prepend))
	for i, m := range cfg.Prepend {
		pre[i] = Message{Role: m.Role, Content: m.Content}
	}
	app := make([]Message, len(cfg.Append))
	for i, m := range cfg.Append {
		app[i] = Message{Role: m.Role, Content: m.Content}
	}

	if maxBodySize == 0 {
		maxBodySize = defaultMaxBodySize
	}

	return &PromptDecorator{
		prepend:     pre,
		append:      app,
		maxBodySize: maxBodySize,
	}
}

// Middleware returns the prompt decoration middleware.
func (d *PromptDecorator) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chatReq := GetChatRequest(r.Context())
			if chatReq == nil {
				// Parse body if guard didn't already
				ct := r.Header.Get("Content-Type")
				if !strings.HasPrefix(ct, "application/json") {
					writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json", "")
					return
				}
				body, err := io.ReadAll(io.LimitReader(r.Body, d.maxBodySize))
				if err != nil {
					writeError(w, http.StatusBadRequest, "read_error", "failed to read request body", "")
					return
				}
				var req ChatRequest
				if err := json.Unmarshal(body, &req); err != nil {
					writeError(w, http.StatusBadRequest, "parse_error", "invalid JSON request body", "")
					return
				}
				chatReq = &req
				r.Body = io.NopCloser(bytes.NewReader(body))
			}

			// Prepend + original + append
			newMsgs := make([]Message, 0, len(d.prepend)+len(chatReq.Messages)+len(d.append))
			newMsgs = append(newMsgs, d.prepend...)
			newMsgs = append(newMsgs, chatReq.Messages...)
			newMsgs = append(newMsgs, d.append...)
			chatReq.Messages = newMsgs

			r = r.WithContext(SetChatRequest(r.Context(), chatReq))
			next.ServeHTTP(w, r)
		})
	}
}
