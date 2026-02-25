package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware"
	"go.uber.org/zap"
)

// PromptGuard checks incoming prompts against deny/allow patterns and length limits.
type PromptGuard struct {
	denyPatterns  []*regexp.Regexp
	allowPatterns []*regexp.Regexp
	denyAction    string // "block" or "log"
	maxPromptLen  int
	maxBodySize   int64

	checked atomic.Int64
	blocked atomic.Int64
}

// NewPromptGuard creates a PromptGuard from config. Returns nil if no guard is configured.
func NewPromptGuard(cfg config.AIPromptGuardConfig, maxBodySize int64) *PromptGuard {
	if len(cfg.DenyPatterns) == 0 && len(cfg.AllowPatterns) == 0 && cfg.MaxPromptLen == 0 {
		return nil
	}

	deny := make([]*regexp.Regexp, 0, len(cfg.DenyPatterns))
	for _, p := range cfg.DenyPatterns {
		deny = append(deny, regexp.MustCompile(p)) // already validated
	}
	allow := make([]*regexp.Regexp, 0, len(cfg.AllowPatterns))
	for _, p := range cfg.AllowPatterns {
		allow = append(allow, regexp.MustCompile(p))
	}

	action := cfg.DenyAction
	if action == "" {
		action = "block"
	}

	if maxBodySize == 0 {
		maxBodySize = defaultMaxBodySize
	}

	return &PromptGuard{
		denyPatterns:  deny,
		allowPatterns: allow,
		denyAction:    action,
		maxPromptLen:  cfg.MaxPromptLen,
		maxBodySize:   maxBodySize,
	}
}

// Middleware returns the prompt guard middleware.
func (g *PromptGuard) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			g.checked.Add(1)

			// Ensure Content-Type is JSON
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json", "")
				return
			}

			// Parse body and store in context
			chatReq := GetChatRequest(r.Context())
			if chatReq == nil {
				body, err := io.ReadAll(io.LimitReader(r.Body, g.maxBodySize))
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
				r = r.WithContext(SetChatRequest(r.Context(), chatReq))
				r.Body = io.NopCloser(bytes.NewReader(body))
			}

			allText := chatReq.AllText()

			// Check max prompt length
			if g.maxPromptLen > 0 && len(allText) > g.maxPromptLen {
				g.blocked.Add(1)
				if g.denyAction == "block" {
					writeError(w, http.StatusBadRequest, "prompt_too_long", "prompt exceeds maximum length", "")
					return
				}
				logging.Warn("prompt guard: prompt exceeds max length", zap.Int("len", len(allText)), zap.Int("max", g.maxPromptLen))
			}

			// Check deny patterns
			for _, re := range g.denyPatterns {
				if re.MatchString(allText) {
					// Check allow patterns (allow overrides deny)
					allowed := false
					for _, are := range g.allowPatterns {
						if are.MatchString(allText) {
							allowed = true
							break
						}
					}
					if !allowed {
						g.blocked.Add(1)
						if g.denyAction == "block" {
							writeError(w, http.StatusBadRequest, "prompt_blocked", "prompt matches deny pattern", "")
							return
						}
						logging.Warn("prompt guard: prompt matches deny pattern", zap.String("pattern", re.String()))
					}
					break
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns prompt guard statistics.
func (g *PromptGuard) Stats() map[string]any {
	return map[string]any{
		"checked": g.checked.Load(),
		"blocked": g.blocked.Load(),
	}
}
