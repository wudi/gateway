package ssrf

import (
	"context"
	"net"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestDefaultBlockedRanges(t *testing.T) {
	ranges := DefaultBlockedRanges()
	if len(ranges) == 0 {
		t.Fatal("expected non-empty blocked ranges")
	}
	// Verify all are parseable
	for _, r := range ranges {
		if _, _, err := net.ParseCIDR(r); err != nil {
			t.Errorf("invalid default range %q: %v", r, err)
		}
	}
}

func TestNew(t *testing.T) {
	dialer := &net.Dialer{}

	// Valid config
	sd, err := New(dialer, config.SSRFProtectionConfig{Enabled: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sd == nil {
		t.Fatal("expected non-nil SafeDialer")
	}

	// With allow CIDRs
	sd, err = New(dialer, config.SSRFProtectionConfig{
		Enabled:    true,
		AllowCIDRs: []string{"10.0.0.0/24"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sd.allowed) != 1 {
		t.Errorf("expected 1 allowed range, got %d", len(sd.allowed))
	}

	// Invalid allow CIDR
	_, err = New(dialer, config.SSRFProtectionConfig{
		Enabled:    true,
		AllowCIDRs: []string{"not-a-cidr"},
	})
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestIsBlocked(t *testing.T) {
	dialer := &net.Dialer{}
	sd, err := New(dialer, config.SSRFProtectionConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.1.1", true},
		{"0.0.0.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
		{"::1", true},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Errorf("failed to parse IP %q", tt.ip)
			continue
		}
		got := sd.isBlocked(ip)
		if got != tt.blocked {
			t.Errorf("isBlocked(%s) = %v, want %v", tt.ip, got, tt.blocked)
		}
	}
}

func TestIsBlockedWithAllowList(t *testing.T) {
	dialer := &net.Dialer{}
	sd, err := New(dialer, config.SSRFProtectionConfig{
		Enabled:    true,
		AllowCIDRs: []string{"10.0.0.0/24", "172.16.5.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 10.0.0.1 should be allowed (in allow list)
	if sd.isBlocked(net.ParseIP("10.0.0.1")) {
		t.Error("10.0.0.1 should be allowed (in allow list)")
	}

	// 10.0.1.1 should still be blocked (not in allow list)
	if !sd.isBlocked(net.ParseIP("10.0.1.1")) {
		t.Error("10.0.1.1 should be blocked (not in allow list)")
	}

	// 172.16.5.10 should be allowed
	if sd.isBlocked(net.ParseIP("172.16.5.10")) {
		t.Error("172.16.5.10 should be allowed (in allow list)")
	}

	// 172.16.6.10 should be blocked
	if !sd.isBlocked(net.ParseIP("172.16.6.10")) {
		t.Error("172.16.6.10 should be blocked (not in allow list)")
	}
}

func TestBlockLinkLocal(t *testing.T) {
	dialer := &net.Dialer{}

	// Default: block link-local
	sd, _ := New(dialer, config.SSRFProtectionConfig{Enabled: true})
	if !sd.isBlocked(net.ParseIP("169.254.1.1")) {
		t.Error("link-local should be blocked by default")
	}

	// Explicitly disable link-local blocking
	f := false
	sd, _ = New(dialer, config.SSRFProtectionConfig{
		Enabled:        true,
		BlockLinkLocal: &f,
	})
	// 169.254.0.0/16 is still in blocked ranges, so it stays blocked
	// but fe80::1 link-local unicast check is disabled
	if sd.blockLinkLocal {
		t.Error("blockLinkLocal should be false")
	}
}

func TestDialContextBlocksPrivateIP(t *testing.T) {
	dialer := &net.Dialer{}
	sd, err := New(dialer, config.SSRFProtectionConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Direct IP literal - should be blocked
	_, err = sd.DialContext(ctx, "tcp", "127.0.0.1:80")
	if err == nil {
		t.Error("expected connection to 127.0.0.1 to be blocked")
	}

	_, err = sd.DialContext(ctx, "tcp", "10.0.0.1:80")
	if err == nil {
		t.Error("expected connection to 10.0.0.1 to be blocked")
	}

	if sd.BlockedRequests() != 2 {
		t.Errorf("expected 2 blocked requests, got %d", sd.BlockedRequests())
	}
}

func TestDialContextInvalidAddress(t *testing.T) {
	dialer := &net.Dialer{}
	sd, _ := New(dialer, config.SSRFProtectionConfig{Enabled: true})

	_, err := sd.DialContext(context.Background(), "tcp", "no-port")
	if err == nil {
		t.Error("expected error for address without port")
	}
}

func TestStats(t *testing.T) {
	dialer := &net.Dialer{}
	sd, _ := New(dialer, config.SSRFProtectionConfig{
		Enabled:    true,
		AllowCIDRs: []string{"10.0.0.0/8"},
	})

	stats := sd.Stats()
	if stats["enabled"] != true {
		t.Error("expected enabled=true")
	}
	if stats["allow_ranges"] != 1 {
		t.Errorf("expected 1 allow range, got %v", stats["allow_ranges"])
	}
}
