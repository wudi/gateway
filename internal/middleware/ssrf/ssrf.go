package ssrf

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/config"
)

// DefaultBlockedRanges returns the default private/reserved IP ranges to block.
func DefaultBlockedRanges() []string {
	return []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"0.0.0.0/8",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
}

// SafeDialer wraps a net.Dialer to block connections to private IP addresses.
// It resolves hostnames before dialing and validates all resolved IPs, preventing
// DNS rebinding attacks by dialing the resolved IP directly.
type SafeDialer struct {
	inner           *net.Dialer
	blocked         []*net.IPNet
	allowed         []*net.IPNet
	blockLinkLocal  bool
	blockedRequests atomic.Int64
}

// New creates a new SafeDialer from config. It parses allow_cidrs and sets up
// default blocked ranges. Returns an error if any allow CIDR is invalid.
func New(dialer *net.Dialer, cfg config.SSRFProtectionConfig) (*SafeDialer, error) {
	blocked, err := parseCIDRs(DefaultBlockedRanges())
	if err != nil {
		return nil, fmt.Errorf("ssrf: failed to parse default blocked ranges: %w", err)
	}

	var allowed []*net.IPNet
	if len(cfg.AllowCIDRs) > 0 {
		allowed, err = parseCIDRs(cfg.AllowCIDRs)
		if err != nil {
			return nil, fmt.Errorf("ssrf: invalid allow_cidrs: %w", err)
		}
	}

	blockLinkLocal := true
	if cfg.BlockLinkLocal != nil {
		blockLinkLocal = *cfg.BlockLinkLocal
	}

	return &SafeDialer{
		inner:          dialer,
		blocked:        blocked,
		allowed:        allowed,
		blockLinkLocal: blockLinkLocal,
	}, nil
}

// DialContext resolves the hostname, validates all IPs, and dials the first valid IP.
func (sd *SafeDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("ssrf: invalid address %q: %w", addr, err)
	}

	// If already an IP literal, validate directly
	if ip := net.ParseIP(host); ip != nil {
		if sd.isBlocked(ip) {
			sd.blockedRequests.Add(1)
			return nil, fmt.Errorf("ssrf: connection to %s blocked (private/reserved IP)", host)
		}
		return sd.inner.DialContext(ctx, network, addr)
	}

	// Resolve hostname
	resolver := sd.inner.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("ssrf: DNS lookup failed for %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("ssrf: no IPs found for %q", host)
	}

	// Validate ALL resolved IPs before dialing any
	for _, ipAddr := range ips {
		if sd.isBlocked(ipAddr.IP) {
			sd.blockedRequests.Add(1)
			return nil, fmt.Errorf("ssrf: connection to %s (%s) blocked (resolves to private/reserved IP)", host, ipAddr.IP)
		}
	}

	// Dial the first resolved IP directly (prevents DNS rebinding)
	resolvedAddr := net.JoinHostPort(ips[0].IP.String(), port)
	return sd.inner.DialContext(ctx, network, resolvedAddr)
}

// isBlocked returns true if the IP falls in a blocked range and is not in the allow list.
func (sd *SafeDialer) isBlocked(ip net.IP) bool {
	// Check allow list first (exempt from blocking)
	for _, n := range sd.allowed {
		if n.Contains(ip) {
			return false
		}
	}

	// Check link-local specifically if configured
	if sd.blockLinkLocal && ip.IsLinkLocalUnicast() {
		return true
	}

	// Check blocked ranges
	for _, n := range sd.blocked {
		if n.Contains(ip) {
			return true
		}
	}

	return false
}

// Stats returns admin status information.
func (sd *SafeDialer) Stats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":          true,
		"blocked_requests": sd.blockedRequests.Load(),
		"blocked_ranges":   len(sd.blocked),
		"allow_ranges":     len(sd.allowed),
		"block_link_local": sd.blockLinkLocal,
	}
}

// BlockedRequests returns the number of blocked connection attempts.
func (sd *SafeDialer) BlockedRequests() int64 {
	return sd.blockedRequests.Load()
}

// parseCIDRs parses a list of CIDR strings into net.IPNet pointers.
func parseCIDRs(cidrs []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
		}
		nets = append(nets, ipNet)
	}
	return nets, nil
}

// NewResolver creates a custom net.Resolver for SSRF that does not bypass the safe dialer.
// This is exposed for testing; production code uses the dialer's resolver.
func NewResolver(nameservers []string, timeout time.Duration) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, "udp", nameservers[0])
		},
	}
}
