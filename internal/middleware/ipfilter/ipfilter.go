package ipfilter

import (
	"net"
	"net/http"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/variables"
)

// Filter checks client IPs against allow/deny lists
type Filter struct {
	enabled bool
	allow   []*net.IPNet
	deny    []*net.IPNet
	order   string // "allow_first" or "deny_first"
}

// New creates a new IP filter from config
func New(cfg config.IPFilterConfig) (*Filter, error) {
	f := &Filter{
		enabled: cfg.Enabled,
		order:   cfg.Order,
	}

	if f.order == "" {
		f.order = "deny_first"
	}

	for _, cidr := range cfg.Allow {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			// Try as single IP
			ip := net.ParseIP(cidr)
			if ip == nil {
				return nil, err
			}
			if ip.To4() != nil {
				_, ipNet, _ = net.ParseCIDR(cidr + "/32")
			} else {
				_, ipNet, _ = net.ParseCIDR(cidr + "/128")
			}
		}
		f.allow = append(f.allow, ipNet)
	}

	for _, cidr := range cfg.Deny {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			ip := net.ParseIP(cidr)
			if ip == nil {
				return nil, err
			}
			if ip.To4() != nil {
				_, ipNet, _ = net.ParseCIDR(cidr + "/32")
			} else {
				_, ipNet, _ = net.ParseCIDR(cidr + "/128")
			}
		}
		f.deny = append(f.deny, ipNet)
	}

	return f, nil
}

// Check returns true if the IP is allowed
func (f *Filter) Check(r *http.Request) bool {
	if !f.enabled {
		return true
	}

	clientIP := variables.ExtractClientIP(r)
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}

	if f.order == "allow_first" {
		return f.checkAllowFirst(ip)
	}
	return f.checkDenyFirst(ip)
}

func (f *Filter) checkAllowFirst(ip net.IP) bool {
	// If allow list exists and IP matches, allow
	if len(f.allow) > 0 {
		for _, cidr := range f.allow {
			if cidr.Contains(ip) {
				return true
			}
		}
		// Allow list exists but IP not in it — deny
		return false
	}

	// No allow list, check deny list
	for _, cidr := range f.deny {
		if cidr.Contains(ip) {
			return false
		}
	}
	return true
}

func (f *Filter) checkDenyFirst(ip net.IP) bool {
	// Check deny list first
	for _, cidr := range f.deny {
		if cidr.Contains(ip) {
			return false
		}
	}

	// If allow list exists, IP must be in it
	if len(f.allow) > 0 {
		for _, cidr := range f.allow {
			if cidr.Contains(ip) {
				return true
			}
		}
		return false
	}

	return true
}

// IsEnabled returns whether the filter is active
func (f *Filter) IsEnabled() bool {
	return f.enabled
}

// IPFilterByRoute manages IP filters per route
type IPFilterByRoute struct {
	byroute.Manager[*Filter]
}

// NewIPFilterByRoute creates a new per-route IP filter manager
func NewIPFilterByRoute() *IPFilterByRoute {
	return &IPFilterByRoute{}
}

// AddRoute adds an IP filter for a route
func (m *IPFilterByRoute) AddRoute(routeID string, cfg config.IPFilterConfig) error {
	f, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, f)
	return nil
}

// GetFilter returns the IP filter for a route
func (m *IPFilterByRoute) GetFilter(routeID string) *Filter {
	v, _ := m.Get(routeID)
	return v
}

// CheckRequest checks if a request is allowed by the route's IP filter
func (m *IPFilterByRoute) CheckRequest(routeID string, r *http.Request) bool {
	f := m.GetFilter(routeID)
	if f == nil {
		return true
	}
	return f.Check(r)
}

// RejectRequest sends a 403 Forbidden response
func RejectRequest(w http.ResponseWriter) {
	errors.ErrForbidden.WithDetails("IP address not allowed").WriteJSON(w)
}
