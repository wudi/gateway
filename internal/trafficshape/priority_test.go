package trafficshape

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/variables"
)

func TestPriorityAdmitter_BasicAdmit(t *testing.T) {
	pa := NewPriorityAdmitter(10)

	release, err := pa.Admit(context.Background(), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer release()

	snap := pa.Snapshot()
	if snap.Active != 1 {
		t.Errorf("expected 1 active, got %d", snap.Active)
	}
	if snap.TotalAdmitted != 1 {
		t.Errorf("expected 1 admitted, got %d", snap.TotalAdmitted)
	}
}

func TestPriorityAdmitter_Release(t *testing.T) {
	pa := NewPriorityAdmitter(1)

	release, err := pa.Admit(context.Background(), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Release the slot
	release()

	snap := pa.Snapshot()
	if snap.Active != 0 {
		t.Errorf("expected 0 active after release, got %d", snap.Active)
	}
}

func TestPriorityAdmitter_QueueAndPriority(t *testing.T) {
	pa := NewPriorityAdmitter(1)

	// Fill the slot
	release1, _ := pa.Admit(context.Background(), 5)

	// Queue a low-priority request
	var lowRelease func()
	var lowErr error
	lowDone := make(chan struct{})
	go func() {
		lowRelease, lowErr = pa.Admit(context.Background(), 10) // low priority
		close(lowDone)
	}()

	// Queue a high-priority request
	var highRelease func()
	var highErr error
	highDone := make(chan struct{})
	go func() {
		// Small delay to ensure both are queued
		time.Sleep(10 * time.Millisecond)
		highRelease, highErr = pa.Admit(context.Background(), 1) // high priority
		close(highDone)
	}()

	// Give time for both to be queued
	time.Sleep(50 * time.Millisecond)

	snap := pa.Snapshot()
	if snap.QueueDepth != 2 {
		t.Errorf("expected queue depth 2, got %d", snap.QueueDepth)
	}

	// Release slot — high priority (level 1) should get it first
	release1()

	// Wait for high priority to be admitted
	select {
	case <-highDone:
	case <-time.After(time.Second):
		t.Fatal("high priority request did not complete in time")
	}

	if highErr != nil {
		t.Fatalf("high priority: unexpected error: %v", highErr)
	}

	// Release high priority slot, then low should proceed
	highRelease()

	select {
	case <-lowDone:
	case <-time.After(time.Second):
		t.Fatal("low priority request did not complete in time")
	}

	if lowErr != nil {
		t.Fatalf("low priority: unexpected error: %v", lowErr)
	}
	lowRelease()
}

func TestPriorityAdmitter_ContextCancel(t *testing.T) {
	pa := NewPriorityAdmitter(1)

	// Fill the slot
	release, _ := pa.Admit(context.Background(), 5)
	defer release()

	// Try to admit with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := pa.Admit(ctx, 5)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	snap := pa.Snapshot()
	if snap.TotalRejected != 1 {
		t.Errorf("expected 1 rejected, got %d", snap.TotalRejected)
	}
}

func TestPriorityAdmitter_Concurrent(t *testing.T) {
	pa := NewPriorityAdmitter(10)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := pa.Admit(context.Background(), 5)
			if err != nil {
				return
			}
			time.Sleep(time.Millisecond)
			release()
		}()
	}
	wg.Wait()

	snap := pa.Snapshot()
	if snap.TotalAdmitted != 20 {
		t.Errorf("expected 20 admitted, got %d", snap.TotalAdmitted)
	}
	if snap.Active != 0 {
		t.Errorf("expected 0 active, got %d", snap.Active)
	}
}

func TestDetermineLevel_Default(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	cfg := config.PriorityConfig{
		DefaultLevel: 5,
	}

	level := DetermineLevel(r, nil, cfg, 0)
	if level != 5 {
		t.Errorf("expected level 5, got %d", level)
	}
}

func TestDetermineLevel_DefaultZero(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	cfg := config.PriorityConfig{}

	level := DetermineLevel(r, nil, cfg, 0)
	if level != 5 {
		t.Errorf("expected default level 5 when not set, got %d", level)
	}
}

func TestDetermineLevel_HeaderMatch(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Priority", "high")

	cfg := config.PriorityConfig{
		DefaultLevel: 5,
		Levels: []config.PriorityLevelConfig{
			{Level: 1, Headers: map[string]string{"X-Priority": "high"}},
		},
	}

	level := DetermineLevel(r, nil, cfg, 0)
	if level != 1 {
		t.Errorf("expected level 1, got %d", level)
	}
}

func TestDetermineLevel_ClientIDMatch(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	identity := &variables.Identity{ClientID: "vip-client"}

	cfg := config.PriorityConfig{
		DefaultLevel: 5,
		Levels: []config.PriorityLevelConfig{
			{Level: 2, ClientIDs: []string{"vip-client", "premium"}},
		},
	}

	level := DetermineLevel(r, identity, cfg, 0)
	if level != 2 {
		t.Errorf("expected level 2, got %d", level)
	}
}

func TestDetermineLevel_NoMatch(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Priority", "low")

	cfg := config.PriorityConfig{
		DefaultLevel: 7,
		Levels: []config.PriorityLevelConfig{
			{Level: 1, Headers: map[string]string{"X-Priority": "high"}},
		},
	}

	level := DetermineLevel(r, nil, cfg, 0)
	if level != 7 {
		t.Errorf("expected level 7, got %d", level)
	}
}

func TestDetermineLevel_TenantPriorityOverride(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Priority", "high")

	cfg := config.PriorityConfig{
		DefaultLevel: 5,
		Levels: []config.PriorityLevelConfig{
			{Level: 1, Headers: map[string]string{"X-Priority": "high"}},
		},
	}

	// Tenant priority overrides even when header-level match exists
	level := DetermineLevel(r, nil, cfg, 3)
	if level != 3 {
		t.Errorf("expected tenant priority 3, got %d", level)
	}

	// Zero tenant priority falls through to normal matching
	level = DetermineLevel(r, nil, cfg, 0)
	if level != 1 {
		t.Errorf("expected header-matched level 1, got %d", level)
	}
}
