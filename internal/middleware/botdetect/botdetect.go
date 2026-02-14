package botdetect

import (
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/middleware"
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
	merged := global
	if len(route.Deny) > 0 {
		merged.Deny = route.Deny
	}
	if len(route.Allow) > 0 {
		merged.Allow = route.Allow
	}
	merged.Enabled = true
	return merged
}

// BotDetectByRoute manages per-route bot detectors.
type BotDetectByRoute struct {
	detectors map[string]*BotDetector
	mu        sync.RWMutex
}

// NewBotDetectByRoute creates a new per-route bot detection manager.
func NewBotDetectByRoute() *BotDetectByRoute {
	return &BotDetectByRoute{
		detectors: make(map[string]*BotDetector),
	}
}

// AddRoute adds a bot detector for a route.
func (m *BotDetectByRoute) AddRoute(routeID string, cfg config.BotDetectionConfig) error {
	bd, err := New(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.detectors[routeID] = bd
	m.mu.Unlock()
	return nil
}

// GetDetector returns the bot detector for a route.
func (m *BotDetectByRoute) GetDetector(routeID string) *BotDetector {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.detectors[routeID]
}

// RouteIDs returns all route IDs with bot detection.
func (m *BotDetectByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.detectors))
	for id := range m.detectors {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns per-route blocked counts.
func (m *BotDetectByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := make(map[string]interface{}, len(m.detectors))
	for id, bd := range m.detectors {
		stats[id] = map[string]interface{}{
			"blocked": bd.Blocked(),
		}
	}
	return stats
}
