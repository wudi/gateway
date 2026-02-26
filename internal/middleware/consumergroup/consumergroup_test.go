package consumergroup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/variables"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
}

// reqWithClaims creates a request with a variable context containing the given claims.
func reqWithClaims(claims map[string]interface{}) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	varCtx := variables.NewContext(r)
	varCtx.Identity = &variables.Identity{
		ClientID: "test-client",
		AuthType: "jwt",
		Claims:   claims,
	}
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
	return r.WithContext(ctx)
}

func testConfig() config.ConsumerGroupsConfig {
	return config.ConsumerGroupsConfig{
		Enabled: true,
		Groups: map[string]config.ConsumerGroup{
			"premium": {
				RateLimit: 1000,
				Quota:     100000,
				Priority:  1,
				Metadata:  map[string]string{"tier": "gold"},
			},
			"standard": {
				RateLimit: 100,
				Quota:     10000,
				Priority:  5,
			},
		},
	}
}

func TestGroupResolvedFromClaim(t *testing.T) {
	gm := NewGroupManager(testConfig())
	mw := gm.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := FromContext(r.Context())
		if info == nil {
			t.Fatal("expected group info in context")
		}
		if info.Name != "premium" {
			t.Errorf("expected group=premium, got %s", info.Name)
		}
		if info.Group.RateLimit != 1000 {
			t.Errorf("expected rate_limit=1000, got %d", info.Group.RateLimit)
		}
		if info.Group.Metadata["tier"] != "gold" {
			t.Errorf("expected metadata tier=gold, got %s", info.Group.Metadata["tier"])
		}
		w.WriteHeader(200)
	}))

	r := reqWithClaims(map[string]interface{}{
		"consumer_group": "premium",
	})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGroupResolvedFromRole(t *testing.T) {
	gm := NewGroupManager(testConfig())
	mw := gm.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := FromContext(r.Context())
		if info == nil {
			t.Fatal("expected group info in context")
		}
		if info.Name != "standard" {
			t.Errorf("expected group=standard, got %s", info.Name)
		}
		if info.Group.RateLimit != 100 {
			t.Errorf("expected rate_limit=100, got %d", info.Group.RateLimit)
		}
		w.WriteHeader(200)
	}))

	r := reqWithClaims(map[string]interface{}{
		"roles": []interface{}{"viewer", "standard"},
	})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGroupNotFound_PassesThrough(t *testing.T) {
	gm := NewGroupManager(testConfig())
	mw := gm.Middleware()

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		info := FromContext(r.Context())
		if info != nil {
			t.Errorf("expected no group info, got %+v", info)
		}
		w.WriteHeader(200)
	}))

	// Claims exist but no matching consumer_group or role
	r := reqWithClaims(map[string]interface{}{
		"sub": "user123",
	})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !called {
		t.Error("expected next handler to be called")
	}
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestFromContext_NilWhenNoGroup(t *testing.T) {
	ctx := context.Background()
	info := FromContext(ctx)
	if info != nil {
		t.Errorf("expected nil, got %+v", info)
	}
}

func TestPerGroupRequestCounting(t *testing.T) {
	gm := NewGroupManager(testConfig())
	mw := gm.Middleware()
	handler := mw(okHandler())

	for i := 0; i < 5; i++ {
		r := reqWithClaims(map[string]interface{}{
			"consumer_group": "premium",
		})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
	}
	for i := 0; i < 3; i++ {
		r := reqWithClaims(map[string]interface{}{
			"consumer_group": "standard",
		})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
	}

	if gm.totalRequests.Load() != 8 {
		t.Errorf("expected total=8, got %d", gm.totalRequests.Load())
	}

	premiumCounter, ok := gm.perGroupRequests.Load("premium")
	if !ok {
		t.Fatal("expected counter for premium")
	}
	if premiumCounter.(*atomic.Int64).Load() != 5 {
		t.Errorf("expected premium=5, got %d", premiumCounter.(*atomic.Int64).Load())
	}

	standardCounter, ok := gm.perGroupRequests.Load("standard")
	if !ok {
		t.Fatal("expected counter for standard")
	}
	if standardCounter.(*atomic.Int64).Load() != 3 {
		t.Errorf("expected standard=3, got %d", standardCounter.(*atomic.Int64).Load())
	}
}

func TestStats(t *testing.T) {
	gm := NewGroupManager(testConfig())
	mw := gm.Middleware()
	handler := mw(okHandler())

	r := reqWithClaims(map[string]interface{}{
		"consumer_group": "premium",
	})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	stats := gm.Stats()

	if stats["enabled"] != true {
		t.Error("expected enabled=true")
	}
	if stats["group_count"] != 2 {
		t.Errorf("expected group_count=2, got %v", stats["group_count"])
	}
	if stats["total_requests"].(int64) != 1 {
		t.Errorf("expected total_requests=1, got %v", stats["total_requests"])
	}

	groups, ok := stats["groups"].(map[string]interface{})
	if !ok {
		t.Fatal("expected groups map in stats")
	}
	premiumStats, ok := groups["premium"].(map[string]interface{})
	if !ok {
		t.Fatal("expected premium stats")
	}
	if premiumStats["rate_limit"] != 1000 {
		t.Errorf("expected rate_limit=1000, got %v", premiumStats["rate_limit"])
	}
	if premiumStats["requests"].(int64) != 1 {
		t.Errorf("expected requests=1, got %v", premiumStats["requests"])
	}
}

func TestNoIdentity_PassesThrough(t *testing.T) {
	gm := NewGroupManager(testConfig())
	mw := gm.Middleware()

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	// Request without any variable context identity
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !called {
		t.Error("expected next handler to be called")
	}
}

func TestClaimTakesPrecedenceOverRole(t *testing.T) {
	gm := NewGroupManager(testConfig())
	mw := gm.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := FromContext(r.Context())
		if info == nil {
			t.Fatal("expected group info in context")
		}
		// consumer_group claim should win over roles
		if info.Name != "premium" {
			t.Errorf("expected group=premium (from claim), got %s", info.Name)
		}
		w.WriteHeader(200)
	}))

	r := reqWithClaims(map[string]interface{}{
		"consumer_group": "premium",
		"roles":          []interface{}{"standard"},
	})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGroupByRoute(t *testing.T) {
	gbr := NewGroupByRoute()
	gm := NewGroupManager(testConfig())
	gbr.SetManager(gm)
	gbr.AddRoute("route1")
	gbr.AddRoute("route2")

	if gbr.GetManager() == nil {
		t.Error("expected manager to be set")
	}

	ids := gbr.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 routes, got %d", len(ids))
	}

	stats := gbr.Stats()
	if stats == nil {
		t.Fatal("expected stats from GroupByRoute")
	}
	if stats["enabled"] != true {
		t.Error("expected enabled=true in stats")
	}

	routes, ok := stats["routes"].([]string)
	if !ok {
		t.Fatal("expected routes in stats")
	}
	if len(routes) != 2 {
		t.Errorf("expected 2 routes in stats, got %d", len(routes))
	}
}

func TestGroupByRoute_NilManager(t *testing.T) {
	gbr := NewGroupByRoute()
	stats := gbr.Stats()
	if stats != nil {
		t.Errorf("expected nil stats with no manager, got %v", stats)
	}
}
