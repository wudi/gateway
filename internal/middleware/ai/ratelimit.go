package ai

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/variables"
)

// AIRateLimiter enforces token-based rate limits for AI routes.
type AIRateLimiter struct {
	tokensPerMinute int64
	tokensPerDay    int64
	keyFunc         func(r *http.Request) string

	mu      sync.Mutex
	windows map[string]*tokenWindow

	checked atomic.Int64
	limited atomic.Int64
}

type tokenWindow struct {
	minuteTokens int64
	minuteStart  time.Time
	dayTokens    int64
	dayStart     time.Time
}

// NewAIRateLimiter creates an AIRateLimiter from config. Returns nil if no rate limiting is configured.
func NewAIRateLimiter(cfg config.AIRateLimitConfig) *AIRateLimiter {
	if cfg.TokensPerMinute == 0 && cfg.TokensPerDay == 0 {
		return nil
	}

	keyFunc := buildAIKeyFunc(cfg.Key)

	return &AIRateLimiter{
		tokensPerMinute: cfg.TokensPerMinute,
		tokensPerDay:    cfg.TokensPerDay,
		keyFunc:         keyFunc,
		windows:         make(map[string]*tokenWindow),
	}
}

// Middleware returns the AI rate limiting middleware.
func (rl *AIRateLimiter) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rl.checked.Add(1)

			key := rl.keyFunc(r)
			chatReq := GetChatRequest(r.Context())

			// Estimate input tokens using word count heuristic
			var estimate int64
			if chatReq != nil {
				words := len(strings.Fields(chatReq.AllText()))
				estimate = int64(float64(words) * 1.3)
			}

			now := time.Now()

			rl.mu.Lock()
			win, ok := rl.windows[key]
			if !ok {
				win = &tokenWindow{minuteStart: now, dayStart: now}
				rl.windows[key] = win
			}

			// Reset windows if expired
			if now.Sub(win.minuteStart) >= time.Minute {
				win.minuteTokens = 0
				win.minuteStart = now
			}
			if now.Sub(win.dayStart) >= 24*time.Hour {
				win.dayTokens = 0
				win.dayStart = now
			}

			// Check budget
			if rl.tokensPerMinute > 0 && win.minuteTokens+estimate > rl.tokensPerMinute {
				rl.mu.Unlock()
				rl.limited.Add(1)
				retryAfter := int(time.Minute.Seconds() - now.Sub(win.minuteStart).Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeError(w, http.StatusTooManyRequests, "token_rate_limit", "token budget exceeded (per-minute)", "")
				return
			}
			if rl.tokensPerDay > 0 && win.dayTokens+estimate > rl.tokensPerDay {
				rl.mu.Unlock()
				rl.limited.Add(1)
				retryAfter := int((24 * time.Hour).Seconds() - now.Sub(win.dayStart).Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeError(w, http.StatusTooManyRequests, "token_rate_limit", "token budget exceeded (per-day)", "")
				return
			}

			// Pre-deduct estimate
			win.minuteTokens += estimate
			win.dayTokens += estimate
			rl.mu.Unlock()

			// Set up token callback for post-response correction
			var actualTokens atomic.Int64
			ctx := SetTokenCallback(r.Context(), &actualTokens)
			r = r.WithContext(ctx)

			next.ServeHTTP(w, r)

			// Post-response: correct estimate with actual tokens
			actual := actualTokens.Load()
			if actual > 0 {
				diff := actual - estimate
				rl.mu.Lock()
				win.minuteTokens += diff
				win.dayTokens += diff
				rl.mu.Unlock()
			}
		})
	}
}

// Stats returns rate limiter statistics.
func (rl *AIRateLimiter) Stats() map[string]any {
	return map[string]any{
		"checked": rl.checked.Load(),
		"limited": rl.limited.Load(),
	}
}

func buildAIKeyFunc(key string) func(r *http.Request) string {
	if key == "" {
		key = "ip"
	}

	switch {
	case key == "ip":
		return func(r *http.Request) string {
			return variables.ExtractClientIP(r)
		}
	case key == "client_id":
		return func(r *http.Request) string {
			if vc := variables.GetFromRequest(r); vc != nil {
				return vc.Identity.ClientID
			}
			return variables.ExtractClientIP(r)
		}
	case strings.HasPrefix(key, "header:"):
		header := strings.TrimPrefix(key, "header:")
		return func(r *http.Request) string {
			if v := r.Header.Get(header); v != "" {
				return v
			}
			return variables.ExtractClientIP(r)
		}
	case strings.HasPrefix(key, "cookie:"):
		name := strings.TrimPrefix(key, "cookie:")
		return func(r *http.Request) string {
			if c, err := r.Cookie(name); err == nil {
				return c.Value
			}
			return variables.ExtractClientIP(r)
		}
	case strings.HasPrefix(key, "jwt_claim:"):
		claim := strings.TrimPrefix(key, "jwt_claim:")
		return func(r *http.Request) string {
			if vc := variables.GetFromRequest(r); vc != nil {
				if v, ok := vc.Identity.Claims[claim]; ok {
					return fmt.Sprintf("%v", v)
				}
			}
			return variables.ExtractClientIP(r)
		}
	default:
		return func(r *http.Request) string {
			return variables.ExtractClientIP(r)
		}
	}
}
