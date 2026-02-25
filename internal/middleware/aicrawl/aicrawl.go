package aicrawl

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/middleware"
)

// crawlerPolicy holds a resolved policy for a single crawler.
type crawlerPolicy struct {
	name          string
	pattern       *regexp.Regexp
	action        string
	disallowPaths []string
	allowPaths    []string
}

// crawlerMetrics tracks per-crawler request counts using atomics (no mutex).
type crawlerMetrics struct {
	requests  atomic.Int64
	blocked   atomic.Int64
	allowed   atomic.Int64
	monitored atomic.Int64
	lastSeen  atomic.Int64 // unix millis
}

// AICrawlController detects AI crawlers and enforces per-crawler policies.
type AICrawlController struct {
	keywords         []string // lowercase keywords for fast pre-screening
	customPolicies   []*crawlerPolicy
	builtinPolicies  []*crawlerPolicy
	defaultAction    string
	blockStatus      int
	blockBody        string
	blockContentType string
	exposeHeaders    bool

	metrics map[string]*crawlerMetrics // pre-allocated, keyed by crawler name

	totalDetected  atomic.Int64
	totalBlocked   atomic.Int64
	totalAllowed   atomic.Int64
	totalMonitored atomic.Int64
}

// New creates an AICrawlController from config.
func New(cfg config.AICrawlConfig) (*AICrawlController, error) {
	c := &AICrawlController{
		defaultAction:    cfg.DefaultAction,
		blockStatus:      cfg.BlockStatus,
		blockBody:        cfg.BlockBody,
		blockContentType: cfg.BlockContentType,
		exposeHeaders:    cfg.ExposeHeaders,
		metrics:          make(map[string]*crawlerMetrics),
	}

	// Apply defaults
	if c.defaultAction == "" {
		c.defaultAction = "monitor"
	}
	if c.blockStatus == 0 {
		c.blockStatus = http.StatusForbidden
	}
	if c.blockContentType == "" {
		c.blockContentType = "text/plain"
	}

	// Build policy lookup by crawler name
	policyByName := make(map[string]config.AICrawlPolicyConfig, len(cfg.Policies))
	for _, p := range cfg.Policies {
		policyByName[p.Crawler] = p
	}

	// Build custom crawler policies
	for _, cc := range cfg.CustomCrawlers {
		re, err := regexp.Compile(cc.Pattern)
		if err != nil {
			return nil, fmt.Errorf("custom crawler %q: invalid pattern: %w", cc.Name, err)
		}
		action := c.defaultAction
		var pol crawlerPolicy
		pol.name = cc.Name
		pol.pattern = re
		if p, ok := policyByName[cc.Name]; ok {
			action = p.Action
			pol.disallowPaths = p.DisallowPaths
			pol.allowPaths = p.AllowPaths
		}
		pol.action = action
		c.customPolicies = append(c.customPolicies, &pol)
		c.metrics[cc.Name] = &crawlerMetrics{}
		c.keywords = append(c.keywords, strings.ToLower(cc.Name))
	}

	// Build built-in crawler policies
	for _, bi := range BuiltinCrawlers {
		action := c.defaultAction
		var pol crawlerPolicy
		pol.name = bi.Name
		pol.pattern = bi.Pattern
		if p, ok := policyByName[bi.Name]; ok {
			action = p.Action
			pol.disallowPaths = p.DisallowPaths
			pol.allowPaths = p.AllowPaths
		}
		pol.action = action
		c.builtinPolicies = append(c.builtinPolicies, &pol)
		c.metrics[bi.Name] = &crawlerMetrics{}
		c.keywords = append(c.keywords, bi.Keyword)
	}

	return c, nil
}

// detect checks a User-Agent string and returns the matching policy, or nil.
func (c *AICrawlController) detect(ua string) *crawlerPolicy {
	if ua == "" {
		return nil
	}
	// Fast pre-screen: lowercase UA once, check for any keyword match.
	// This rejects 99%+ of normal browser UAs without running any regex.
	lower := strings.ToLower(ua)
	found := false
	for _, kw := range c.keywords {
		if strings.Contains(lower, kw) {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	// Slow path: identify which specific crawler matched.
	// Check custom policies first.
	for _, pol := range c.customPolicies {
		if pol.pattern.MatchString(ua) {
			return pol
		}
	}
	// Check built-in policies.
	for _, pol := range c.builtinPolicies {
		if pol.pattern.MatchString(ua) {
			return pol
		}
	}
	return nil
}

// resolveAction determines the effective action for a crawler+path combination.
func resolveAction(pol *crawlerPolicy, reqPath string) string {
	if len(pol.allowPaths) > 0 {
		for _, pattern := range pol.allowPaths {
			if matched, _ := doublestar.PathMatch(pattern, reqPath); matched {
				return pol.action
			}
		}
		return "block"
	}
	if len(pol.disallowPaths) > 0 {
		for _, pattern := range pol.disallowPaths {
			if matched, _ := doublestar.PathMatch(pattern, reqPath); matched {
				return "block"
			}
		}
	}
	return pol.action
}

// Middleware returns an HTTP middleware that detects and enforces AI crawler policies.
func (c *AICrawlController) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ua := r.Header.Get("User-Agent")
			pol := c.detect(ua)
			if pol == nil {
				next.ServeHTTP(w, r)
				return
			}

			m := c.metrics[pol.name]
			m.requests.Add(1)
			m.lastSeen.Store(time.Now().UnixMilli())
			c.totalDetected.Add(1)

			action := resolveAction(pol, r.URL.Path)

			switch action {
			case "block":
				m.blocked.Add(1)
				c.totalBlocked.Add(1)
				if c.exposeHeaders {
					w.Header().Set("X-AI-Crawler-Blocked", pol.name)
				}
				if c.blockBody != "" || c.blockStatus != http.StatusForbidden {
					w.Header().Set("Content-Type", c.blockContentType)
					w.WriteHeader(c.blockStatus)
					if c.blockBody != "" {
						w.Write([]byte(c.blockBody))
					}
				} else {
					errors.ErrForbidden.WithDetails("AI crawler blocked: " + pol.name).WriteJSON(w)
				}
				return
			case "monitor":
				m.monitored.Add(1)
				c.totalMonitored.Add(1)
				if c.exposeHeaders {
					w.Header().Set("X-AI-Crawler-Detected", pol.name)
				}
			default: // "allow"
				m.allowed.Add(1)
				c.totalAllowed.Add(1)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns aggregate and per-crawler metrics.
func (c *AICrawlController) Stats() map[string]interface{} {
	crawlers := make(map[string]interface{}, len(c.metrics))
	for name, m := range c.metrics {
		// Find action for this crawler
		action := c.defaultAction
		for _, pol := range c.customPolicies {
			if pol.name == name {
				action = pol.action
				break
			}
		}
		for _, pol := range c.builtinPolicies {
			if pol.name == name {
				action = pol.action
				break
			}
		}

		lastSeen := m.lastSeen.Load()
		var lastSeenStr string
		if lastSeen > 0 {
			lastSeenStr = time.UnixMilli(lastSeen).UTC().Format(time.RFC3339)
		}

		crawlers[name] = map[string]interface{}{
			"requests":  m.requests.Load(),
			"blocked":   m.blocked.Load(),
			"allowed":   m.allowed.Load(),
			"monitored": m.monitored.Load(),
			"last_seen": lastSeenStr,
			"action":    action,
		}
	}
	return map[string]interface{}{
		"total_detected":  c.totalDetected.Load(),
		"total_blocked":   c.totalBlocked.Load(),
		"total_allowed":   c.totalAllowed.Load(),
		"total_monitored": c.totalMonitored.Load(),
		"crawlers":        crawlers,
	}
}

// MergeAICrawlConfig merges per-route config with global, preferring per-route when set.
func MergeAICrawlConfig(route, global config.AICrawlConfig) config.AICrawlConfig {
	merged := config.MergeNonZero(global, route)
	merged.Enabled = true
	return merged
}

// AICrawlByRoute manages per-route AI crawl controllers.
type AICrawlByRoute struct {
	byroute.Manager[*AICrawlController]
}

// NewAICrawlByRoute creates a new per-route AI crawl control manager.
func NewAICrawlByRoute() *AICrawlByRoute {
	return &AICrawlByRoute{}
}

// AddRoute adds an AI crawl controller for a route.
func (m *AICrawlByRoute) AddRoute(routeID string, cfg config.AICrawlConfig) error {
	ctrl, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, ctrl)
	return nil
}

// GetController returns the AI crawl controller for a route.
func (m *AICrawlByRoute) GetController(routeID string) *AICrawlController {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route AI crawl statistics.
func (m *AICrawlByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(ctrl *AICrawlController) interface{} {
		return ctrl.Stats()
	})
}
