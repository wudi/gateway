package costtrack

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/variables"
)

// consumerBucket tracks a single consumer's cost within a window.
type consumerBucket struct {
	mu          sync.Mutex
	cost        int64
	windowStart time.Time
}

// budgetTracker enforces per-consumer cost budgets over sliding windows.
type budgetTracker struct {
	limit     int64
	windowDur time.Duration
	action    string // "reject" or "log_only"
	consumers sync.Map // map[string]*consumerBucket
}

// CostTracker tracks request costs for a route.
type CostTracker struct {
	cost         int
	costByMethod map[string]int
	keyFunc      func(*http.Request) string
	budget       *budgetTracker

	totalCost     atomic.Int64
	totalRequests atomic.Int64
	totalRejected atomic.Int64
}

// New creates a CostTracker from config.
func New(cfg config.RequestCostConfig) *CostTracker {
	cost := cfg.Cost
	if cost == 0 {
		cost = 1
	}

	ct := &CostTracker{
		cost:         cost,
		costByMethod: cfg.CostByMethod,
		keyFunc:      buildKeyFunc(cfg.Key),
	}

	if cfg.Budget != nil && cfg.Budget.Limit > 0 {
		ct.budget = &budgetTracker{
			limit:     cfg.Budget.Limit,
			windowDur: parseWindow(cfg.Budget.Window),
			action:    cfg.Budget.Action,
		}
		if ct.budget.action == "" {
			ct.budget.action = "reject"
		}
	}

	return ct
}

// Middleware returns a middleware that tracks request costs.
func (ct *CostTracker) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ct.totalRequests.Add(1)

			// Determine cost for this request
			reqCost := ct.cost
			if mc, ok := ct.costByMethod[r.Method]; ok {
				reqCost = mc
			}

			costStr := strconv.Itoa(reqCost)

			// Check budget if configured
			if ct.budget != nil {
				key := ct.keyFunc(r)
				now := time.Now()
				windowStart := now.Truncate(ct.budget.windowDur)

				actual, _ := ct.budget.consumers.LoadOrStore(key, &consumerBucket{
					windowStart: windowStart,
				})
				bucket := actual.(*consumerBucket)

				bucket.mu.Lock()
				// Reset if window has rolled over
				if !bucket.windowStart.Equal(windowStart) {
					bucket.cost = 0
					bucket.windowStart = windowStart
				}
				currentCost := bucket.cost
				bucket.cost += int64(reqCost)
				bucket.mu.Unlock()

				if currentCost+int64(reqCost) > ct.budget.limit {
					ct.totalRejected.Add(1)
					w.Header().Set("X-Request-Cost", costStr)

					if ct.budget.action == "reject" {
						windowEnd := windowStart.Add(ct.budget.windowDur)
						retryAfter := int64(time.Until(windowEnd).Seconds()) + 1
						if retryAfter < 1 {
							retryAfter = 1
						}
						w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
						http.Error(w, "Request cost budget exceeded", http.StatusTooManyRequests)
						return
					}
					// log_only: allow through but header is already set
				}
			}

			ct.totalCost.Add(int64(reqCost))
			w.Header().Set("X-Request-Cost", costStr)
			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns cost tracker statistics.
func (ct *CostTracker) Stats() map[string]interface{} {
	result := map[string]interface{}{
		"default_cost":    ct.cost,
		"total_cost":      ct.totalCost.Load(),
		"total_requests":  ct.totalRequests.Load(),
		"total_rejected":  ct.totalRejected.Load(),
	}
	if ct.budget != nil {
		result["budget_limit"] = ct.budget.limit
		result["budget_window"] = ct.budget.windowDur.String()
		result["budget_action"] = ct.budget.action
	}
	return result
}

// buildKeyFunc creates a key extraction function from configuration.
func buildKeyFunc(key string) func(*http.Request) string {
	switch key {
	case "", "ip":
		return func(r *http.Request) string {
			return variables.ExtractClientIP(r)
		}
	case "client_id":
		return func(r *http.Request) string {
			varCtx := variables.GetFromRequest(r)
			if varCtx.Identity != nil && varCtx.Identity.ClientID != "" {
				return varCtx.Identity.ClientID
			}
			return variables.ExtractClientIP(r)
		}
	}

	if strings.HasPrefix(key, "header:") {
		name := key[len("header:"):]
		prefix := "header:" + name + ":"
		return func(r *http.Request) string {
			if v := r.Header.Get(name); v != "" {
				return prefix + v
			}
			return variables.ExtractClientIP(r)
		}
	}

	// Default to IP
	return func(r *http.Request) string {
		return variables.ExtractClientIP(r)
	}
}

// parseWindow parses a window string into a duration.
func parseWindow(w string) time.Duration {
	switch strings.ToLower(w) {
	case "hour":
		return time.Hour
	case "day":
		return 24 * time.Hour
	case "month":
		return 720 * time.Hour
	default:
		// Try to parse as Go duration
		if d, err := time.ParseDuration(w); err == nil {
			return d
		}
		return time.Hour
	}
}

// ValidateKey checks whether a cost tracking key is valid.
func ValidateKey(key string) bool {
	switch key {
	case "", "ip", "client_id":
		return true
	}
	if strings.HasPrefix(key, "header:") && len(key) > len("header:") {
		return true
	}
	return false
}

// ValidateAction checks whether a budget action is valid.
func ValidateAction(action string) bool {
	switch action {
	case "", "reject", "log_only":
		return true
	}
	return false
}

// ValidateWindow checks whether a budget window is valid.
func ValidateWindow(window string) bool {
	switch strings.ToLower(window) {
	case "hour", "day", "month":
		return true
	}
	if _, err := time.ParseDuration(window); err == nil {
		return true
	}
	return false
}

// CostByRoute manages per-route cost trackers.
type CostByRoute struct {
	byroute.Manager[*CostTracker]
}

// NewCostByRoute creates a new per-route cost tracker manager.
func NewCostByRoute() *CostByRoute {
	return &CostByRoute{}
}

// AddRoute adds a cost tracker for a route.
func (m *CostByRoute) AddRoute(routeID string, cfg config.RequestCostConfig) {
	m.Add(routeID, New(cfg))
}

// GetTracker returns the cost tracker for a route.
func (m *CostByRoute) GetTracker(routeID string) *CostTracker {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route cost tracker stats.
func (m *CostByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(ct *CostTracker) interface{} {
		return ct.Stats()
	})
}

// FormatBudgetKey returns a formatted budget key for display.
func FormatBudgetKey(routeID, consumerKey string) string {
	return fmt.Sprintf("cost:%s:%s", routeID, consumerKey)
}
