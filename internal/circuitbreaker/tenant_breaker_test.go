package circuitbreaker

import (
	"fmt"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

var testErrServer = fmt.Errorf("server error")

func TestTenantAwareBreaker_AllowForTenant(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 2,
		MaxRequests:      1,
	}
	route := NewBreaker(cfg, nil)
	tab := NewTenantAwareBreaker(route, cfg, "route1", nil, nil)

	// Different tenants get independent breakers
	done1, err := tab.AllowForTenant("tenantA")
	if err != nil {
		t.Fatalf("tenantA Allow: %v", err)
	}
	done1(nil) // success

	done2, err := tab.AllowForTenant("tenantB")
	if err != nil {
		t.Fatalf("tenantB Allow: %v", err)
	}
	done2(nil)

	// Trip tenantA's breaker
	for i := 0; i < 3; i++ {
		d, err := tab.AllowForTenant("tenantA")
		if err != nil {
			break
		}
		d(testErrServer)
	}

	// tenantA should be open
	_, errA := tab.AllowForTenant("tenantA")
	if errA == nil {
		t.Error("expected tenantA breaker to be open")
	}

	// tenantB should still be closed
	d, errB := tab.AllowForTenant("tenantB")
	if errB != nil {
		t.Errorf("tenantB should still be closed, got: %v", errB)
	}
	if d != nil {
		d(nil)
	}
}

func TestTenantAwareBreaker_EmptyTenantFallsThrough(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 5,
	}
	route := NewBreaker(cfg, nil)
	tab := NewTenantAwareBreaker(route, cfg, "route1", nil, nil)

	// Empty tenant uses route-level breaker
	done, err := tab.AllowForTenant("")
	if err != nil {
		t.Fatalf("empty tenant Allow: %v", err)
	}
	done(nil)

	// Route-level breaker should track this request
	snap := tab.Snapshot()
	if snap.TotalRequests != 1 {
		t.Errorf("expected 1 total request on route breaker, got %d", snap.TotalRequests)
	}
}

func TestTenantAwareBreaker_TenantSnapshots(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 5,
	}
	route := NewBreaker(cfg, nil)
	tab := NewTenantAwareBreaker(route, cfg, "route1", nil, nil)

	d, _ := tab.AllowForTenant("t1")
	d(nil)
	d, _ = tab.AllowForTenant("t2")
	d(nil)

	snaps := tab.TenantSnapshots()
	if len(snaps) != 2 {
		t.Errorf("expected 2 tenant snapshots, got %d", len(snaps))
	}
	if _, ok := snaps["t1"]; !ok {
		t.Error("missing t1 snapshot")
	}
	if _, ok := snaps["t2"]; !ok {
		t.Error("missing t2 snapshot")
	}
}

func TestTenantAwareBreaker_Snapshot(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Enabled:          true,
		FailureThreshold: 5,
	}
	route := NewBreaker(cfg, nil)
	tab := NewTenantAwareBreaker(route, cfg, "route1", nil, nil)

	snap := tab.Snapshot()
	if !snap.TenantIsolation {
		t.Error("expected TenantIsolation=true in snapshot")
	}
}
