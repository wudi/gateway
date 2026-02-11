package realip

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

// contextKey is the type for the real IP context key.
type contextKey struct{}

// CompiledRealIP extracts the real client IP from trusted proxy chains.
type CompiledRealIP struct {
	trustedNets []*net.IPNet
	headers     []string // ordered list of headers to check
	maxHops     int      // 0 = unlimited

	totalRequests atomic.Int64
	extracted     atomic.Int64 // times IP was extracted from headers (not RemoteAddr)
}

// New creates a CompiledRealIP from a list of trusted proxy CIDRs.
func New(cidrs []string, headers []string, maxHops int) (*CompiledRealIP, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		// Handle bare IPs by adding /32 or /128
		if !strings.Contains(cidr, "/") {
			ip := net.ParseIP(cidr)
			if ip == nil {
				return nil, &net.ParseError{Type: "IP address", Text: cidr}
			}
			if ip.To4() != nil {
				cidr += "/32"
			} else {
				cidr += "/128"
			}
		}
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		nets = append(nets, ipNet)
	}

	if len(headers) == 0 {
		headers = []string{"X-Forwarded-For", "X-Real-IP"}
	}

	return &CompiledRealIP{
		trustedNets: nets,
		headers:     headers,
		maxHops:     maxHops,
	}, nil
}

// Extract determines the real client IP from the request.
// It walks the X-Forwarded-For chain from right to left, skipping
// IPs that match trusted proxy CIDRs, and returns the first
// untrusted IP. If no trusted proxies are configured, it falls
// back to the first XFF entry (legacy behavior).
func (c *CompiledRealIP) Extract(r *http.Request) string {
	c.totalRequests.Add(1)

	remoteIP := extractHost(r.RemoteAddr)

	// If no trusted networks configured, use legacy behavior
	if len(c.trustedNets) == 0 {
		return c.legacyExtract(r, remoteIP)
	}

	// Only trust headers if RemoteAddr is from a trusted proxy
	if !c.isTrusted(remoteIP) {
		return remoteIP
	}

	// Check configured headers in order
	for _, header := range c.headers {
		val := r.Header.Get(header)
		if val == "" {
			continue
		}

		if strings.EqualFold(header, "X-Forwarded-For") {
			if ip := c.walkXFF(val); ip != "" {
				c.extracted.Add(1)
				return ip
			}
		} else {
			// Single-value headers like X-Real-IP
			ip := strings.TrimSpace(val)
			if ip != "" {
				c.extracted.Add(1)
				return ip
			}
		}
	}

	return remoteIP
}

// walkXFF walks the X-Forwarded-For chain from right to left,
// returning the first IP that is NOT in the trusted proxy list.
func (c *CompiledRealIP) walkXFF(xff string) string {
	parts := strings.Split(xff, ",")

	// Walk from right to left
	hops := 0
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		if ip == "" {
			continue
		}
		hops++

		// Respect max_hops limit
		if c.maxHops > 0 && hops > c.maxHops {
			return ip
		}

		if !c.isTrusted(ip) {
			return ip
		}
	}

	// All IPs in XFF were trusted — return the leftmost
	if len(parts) > 0 {
		return strings.TrimSpace(parts[0])
	}
	return ""
}

// isTrusted checks if an IP string matches any trusted CIDR.
func (c *CompiledRealIP) isTrusted(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, n := range c.trustedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// legacyExtract is the old behavior when no trusted proxies are configured.
func (c *CompiledRealIP) legacyExtract(r *http.Request, remoteIP string) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			ip := strings.TrimSpace(ips[0])
			if ip != "" {
				c.extracted.Add(1)
				return ip
			}
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		c.extracted.Add(1)
		return xri
	}
	return remoteIP
}

// Middleware returns an http.Handler middleware that extracts the real
// client IP and stores it in the request context.
func (c *CompiledRealIP) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		realIP := c.Extract(r)
		ctx := context.WithValue(r.Context(), contextKey{}, realIP)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// FromContext retrieves the real client IP from the request context.
// Returns empty string if not set.
func FromContext(ctx context.Context) string {
	if ip, ok := ctx.Value(contextKey{}).(string); ok {
		return ip
	}
	return ""
}

// Stats returns metrics for the real IP extractor.
type Stats struct {
	TotalRequests int64    `json:"total_requests"`
	Extracted     int64    `json:"extracted"`
	TrustedCIDRs  int      `json:"trusted_cidrs"`
	Headers       []string `json:"headers"`
	MaxHops       int      `json:"max_hops"`
}

// Stats returns the current metrics.
func (c *CompiledRealIP) Stats() Stats {
	return Stats{
		TotalRequests: c.totalRequests.Load(),
		Extracted:     c.extracted.Load(),
		TrustedCIDRs:  len(c.trustedNets),
		Headers:       c.headers,
		MaxHops:       c.maxHops,
	}
}

// extractHost extracts the host part from an address (strips port).
func extractHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
