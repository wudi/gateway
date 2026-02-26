package maintenance

import (
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// CompiledMaintenance holds pre-compiled maintenance mode state for a route.
type CompiledMaintenance struct {
	enabled     atomic.Bool
	statusCode  int
	body        []byte
	contentType string
	retryAfter  string
	excludePaths []string
	excludeNets []*net.IPNet
	excludeIPs  []net.IP
	headers     map[string]string
	metrics     Metrics
}

// Metrics tracks maintenance mode statistics.
type Metrics struct {
	TotalBlocked int64
	TotalBypassed int64
}

// Snapshot is a point-in-time copy of maintenance state and metrics.
type Snapshot struct {
	Enabled       bool              `json:"enabled"`
	StatusCode    int               `json:"status_code"`
	RetryAfter    string            `json:"retry_after,omitempty"`
	ExcludePaths  []string          `json:"exclude_paths,omitempty"`
	TotalBlocked  int64             `json:"total_blocked"`
	TotalBypassed int64             `json:"total_bypassed"`
}

// New creates a CompiledMaintenance from config.
func New(cfg config.MaintenanceConfig) *CompiledMaintenance {
	statusCode := cfg.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusServiceUnavailable
	}

	body := cfg.Body
	if body == "" {
		body = `{"error":"service unavailable","message":"the service is currently undergoing maintenance"}`
	}

	contentType := cfg.ContentType
	if contentType == "" {
		contentType = "application/json"
	}

	cm := &CompiledMaintenance{
		statusCode:   statusCode,
		body:         []byte(body),
		contentType:  contentType,
		retryAfter:   cfg.RetryAfter,
		excludePaths: cfg.ExcludePaths,
		headers:      cfg.Headers,
	}
	cm.enabled.Store(cfg.Enabled)

	// Parse exclude IPs/CIDRs
	for _, cidr := range cfg.ExcludeIPs {
		if strings.Contains(cidr, "/") {
			if _, ipNet, err := net.ParseCIDR(cidr); err == nil {
				cm.excludeNets = append(cm.excludeNets, ipNet)
			}
		} else {
			if ip := net.ParseIP(cidr); ip != nil {
				cm.excludeIPs = append(cm.excludeIPs, ip)
			}
		}
	}

	return cm
}

// IsEnabled returns whether maintenance mode is currently active.
func (cm *CompiledMaintenance) IsEnabled() bool {
	return cm.enabled.Load()
}

// Enable activates maintenance mode.
func (cm *CompiledMaintenance) Enable() {
	cm.enabled.Store(true)
}

// Disable deactivates maintenance mode.
func (cm *CompiledMaintenance) Disable() {
	cm.enabled.Store(false)
}

// ShouldBlock returns true if the request should be blocked by maintenance mode.
// It checks excludePaths and excludeIPs before deciding.
func (cm *CompiledMaintenance) ShouldBlock(r *http.Request) bool {
	if !cm.enabled.Load() {
		return false
	}

	// Check excluded paths
	for _, pattern := range cm.excludePaths {
		if matched, _ := filepath.Match(pattern, r.URL.Path); matched {
			atomic.AddInt64(&cm.metrics.TotalBypassed, 1)
			return false
		}
	}

	// Check excluded IPs
	if len(cm.excludeNets) > 0 || len(cm.excludeIPs) > 0 {
		clientIP := extractIP(r)
		if clientIP != nil {
			for _, ipNet := range cm.excludeNets {
				if ipNet.Contains(clientIP) {
					atomic.AddInt64(&cm.metrics.TotalBypassed, 1)
					return false
				}
			}
			for _, ip := range cm.excludeIPs {
				if ip.Equal(clientIP) {
					atomic.AddInt64(&cm.metrics.TotalBypassed, 1)
					return false
				}
			}
		}
	}

	atomic.AddInt64(&cm.metrics.TotalBlocked, 1)
	return true
}

// WriteResponse writes the maintenance response to the client.
func (cm *CompiledMaintenance) WriteResponse(w http.ResponseWriter) {
	for k, v := range cm.headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", cm.contentType)
	if cm.retryAfter != "" {
		w.Header().Set("Retry-After", cm.retryAfter)
	}
	w.WriteHeader(cm.statusCode)
	w.Write(cm.body)
}

// Snapshot returns a point-in-time copy of state and metrics.
func (cm *CompiledMaintenance) Snapshot() Snapshot {
	return Snapshot{
		Enabled:       cm.enabled.Load(),
		StatusCode:    cm.statusCode,
		RetryAfter:    cm.retryAfter,
		ExcludePaths:  cm.excludePaths,
		TotalBlocked:  atomic.LoadInt64(&cm.metrics.TotalBlocked),
		TotalBypassed: atomic.LoadInt64(&cm.metrics.TotalBypassed),
	}
}

// extractIP parses the client IP from RemoteAddr (host:port format).
func extractIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// MergeMaintenanceConfig merges per-route over global config.
func MergeMaintenanceConfig(perRoute, global config.MaintenanceConfig) config.MaintenanceConfig {
	return config.MergeNonZero(global, perRoute)
}

// MaintenanceByRoute is a ByRoute manager for per-route maintenance mode.
type MaintenanceByRoute struct {
	byroute.Manager[*CompiledMaintenance]
}

// NewMaintenanceByRoute creates a new manager.
func NewMaintenanceByRoute() *MaintenanceByRoute {
	return &MaintenanceByRoute{}
}

// AddRoute adds compiled maintenance mode for a route.
func (m *MaintenanceByRoute) AddRoute(routeID string, cfg config.MaintenanceConfig) {
	m.Add(routeID, New(cfg))
}

// GetMaintenance returns the compiled maintenance for a route, or nil.
func (m *MaintenanceByRoute) GetMaintenance(routeID string) *CompiledMaintenance {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route snapshots.
func (m *MaintenanceByRoute) Stats() map[string]Snapshot {
	return byroute.CollectStats(&m.Manager, func(h *CompiledMaintenance) Snapshot { return h.Snapshot() })
}

// Middleware returns a middleware that short-circuits with a maintenance response when active.
func (cm *CompiledMaintenance) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cm.ShouldBlock(r) {
				cm.WriteResponse(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
