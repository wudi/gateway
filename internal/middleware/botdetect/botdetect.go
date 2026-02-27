package botdetect

import (
	"net/http"
	"regexp"
	"sync/atomic"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/internal/middleware"
)

// BotDetector checks User-Agent against deny/allow regex patterns.
type BotDetector struct {
	deny    []*regexp.Regexp
	allow   []*regexp.Regexp
	blocked atomic.Int64
}

// New compiles a BotDetector from config.
func New(cfg config.BotDetectionConfig) (*BotDetector, error) {
	bd := &BotDetector{}
	for _, p := range cfg.Deny {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		bd.deny = append(bd.deny, re)
	}
	for _, p := range cfg.Allow {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		bd.allow = append(bd.allow, re)
	}
	return bd, nil
}

// Check returns true if the request should be allowed through.
func (bd *BotDetector) Check(r *http.Request) bool {
	ua := r.Header.Get("User-Agent")
	if ua == "" {
		return true
	}

	// Check deny patterns
	denied := false
	for _, re := range bd.deny {
		if re.MatchString(ua) {
			denied = true
			break
		}
	}
	if !denied {
		return true
	}

	// Check allow patterns for override
	for _, re := range bd.allow {
		if re.MatchString(ua) {
			return true
		}
	}

	bd.blocked.Add(1)
	return false
}

// Blocked returns the number of blocked requests.
func (bd *BotDetector) Blocked() int64 {
	return bd.blocked.Load()
}

// Middleware returns a middleware that rejects bot requests with 403.
func (bd *BotDetector) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !bd.Check(r) {
				errors.ErrForbidden.WithDetails("Bot detected").WriteJSON(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MergeBotDetectionConfig merges per-route config with global, preferring per-route when set.
func MergeBotDetectionConfig(route, global config.BotDetectionConfig) config.BotDetectionConfig {
	merged := config.MergeNonZero(global, route)
	merged.Enabled = true
	return merged
}

// BotDetectByRoute manages per-route bot detectors.
type BotDetectByRoute = byroute.Factory[*BotDetector, config.BotDetectionConfig]

// NewBotDetectByRoute creates a new per-route bot detection manager.
func NewBotDetectByRoute() *BotDetectByRoute {
	return byroute.NewFactory(New, func(bd *BotDetector) any {
		return map[string]interface{}{"blocked": bd.Blocked()}
	})
}
