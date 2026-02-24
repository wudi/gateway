package tenant

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
}

func TestManager_ResolveTenantByHeader(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false)
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-Tenant-ID") != "acme" {
		t.Errorf("expected X-Tenant-ID=acme, got %s", w.Header().Get("X-Tenant-ID"))
	}
}

func TestManager_UnknownTenantRejected(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, true) // required=true
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "unknown")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Errorf("expected 403 for unknown tenant, got %d", w.Code)
	}
}

func TestManager_DefaultTenantFallback(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled:       true,
		Key:           "header:X-Tenant-ID",
		DefaultTenant: "default",
		Tenants: map[string]config.TenantConfig{
			"acme":    {},
			"default": {},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, true)
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "unknown")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200 with default tenant, got %d", w.Code)
	}
	if w.Header().Get("X-Tenant-ID") != "default" {
		t.Errorf("expected X-Tenant-ID=default, got %s", w.Header().Get("X-Tenant-ID"))
	}
}

func TestManager_RouteACLByTenantAllowed(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	// Route restricts to "other" tenant only
	mw := m.Middleware([]string{"other"}, false)
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Errorf("expected 403 for tenant not in route allowed list, got %d", w.Code)
	}
}

func TestManager_RouteACLByTenantRoutes(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {
				Routes: []string{"api-v2"},
			},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false)
	handler := mw(okHandler())

	// Request without route context — tenant's route list has "api-v2"
	// but request path won't match since no variable context is set
	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Errorf("expected 403 for tenant route ACL mismatch, got %d", w.Code)
	}
}

func TestManager_RateLimitEnforced(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {
				RateLimit: &config.TenantRateLimitConfig{
					Rate:   2,
					Period: time.Second,
					Burst:  2,
				},
			},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false)
	handler := mw(okHandler())

	// First 2 should pass (burst=2)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Tenant-ID", "acme")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("request %d: expected 200, got %d", i, w.Code)
		}
	}

	// 3rd should be rate limited
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 429 {
		t.Errorf("expected 429 for rate limited request, got %d", w.Code)
	}
}

func TestManager_QuotaEnforced(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {
				Quota: &config.TenantQuotaConfig{
					Limit:  3,
					Period: "hourly",
				},
			},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false)
	handler := mw(okHandler())

	// First 3 should pass
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Tenant-ID", "acme")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("request %d: expected 200, got %d", i, w.Code)
		}
	}

	// 4th should be quota exceeded
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 429 {
		t.Errorf("expected 429 for quota exceeded, got %d", w.Code)
	}
}

func TestManager_ContextPropagation(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {
				Metadata: map[string]string{
					"Plan": "enterprise",
				},
			},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false)

	var tenantInfo *TenantInfo
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantInfo = FromContext(r.Context())
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if tenantInfo == nil {
		t.Fatal("expected tenant info in context")
	}
	if tenantInfo.ID != "acme" {
		t.Errorf("expected tenant ID=acme, got %s", tenantInfo.ID)
	}
	if tenantInfo.Metadata["Plan"] != "enterprise" {
		t.Errorf("expected metadata Plan=enterprise, got %s", tenantInfo.Metadata["Plan"])
	}
}

func TestManager_MetadataPropagation(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {
				Metadata: map[string]string{
					"Plan":  "enterprise",
					"Region": "us-east-1",
				},
			},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false)

	var capturedPlan, capturedRegion string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPlan = r.Header.Get("X-Tenant-Plan")
		capturedRegion = r.Header.Get("X-Tenant-Region")
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if capturedPlan != "enterprise" {
		t.Errorf("expected X-Tenant-Plan=enterprise, got %s", capturedPlan)
	}
	if capturedRegion != "us-east-1" {
		t.Errorf("expected X-Tenant-Region=us-east-1, got %s", capturedRegion)
	}
}

func TestManager_Stats(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme":    {},
			"startup": {},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false)
	handler := mw(okHandler())

	// Send a request for acme
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	stats := m.Stats()
	if stats["enabled"] != true {
		t.Error("expected enabled=true")
	}
	if stats["tenant_count"] != 2 {
		t.Errorf("expected tenant_count=2, got %v", stats["tenant_count"])
	}

	tenants := stats["tenants"].(map[string]interface{})
	acmeStats := tenants["acme"].(map[string]interface{})
	if acmeStats["allowed"] != int64(1) {
		t.Errorf("expected acme allowed=1, got %v", acmeStats["allowed"])
	}
}

func TestManager_NoTenantNotRequired(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false) // required=false
	handler := mw(okHandler())

	// No tenant header — should pass through without tenant context
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200 when tenant not required, got %d", w.Code)
	}
}

func TestManager_MissingHeaderRequired(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, true) // required=true
	handler := mw(okHandler())

	// No tenant header
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Errorf("expected 403 when tenant required but missing, got %d", w.Code)
	}
}

func TestManager_TierMerge(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tiers: map[string]config.TenantTierConfig{
			"enterprise": {
				RateLimit: &config.TenantRateLimitConfig{Rate: 1000, Period: time.Second, Burst: 2000},
				MaxBodySize: 10 * 1024 * 1024,
				Priority:    2,
				Metadata:    map[string]string{"plan": "enterprise", "support": "premium"},
				ResponseHeaders: map[string]string{"X-Plan": "enterprise"},
			},
		},
		Tenants: map[string]config.TenantConfig{
			"acme": {
				Tier:     "enterprise",
				Metadata: map[string]string{"region": "us-east-1"}, // should merge
			},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	tc := m.ListTenants()["acme"]
	if tc.MaxBodySize != 10*1024*1024 {
		t.Errorf("expected max_body_size from tier, got %d", tc.MaxBodySize)
	}
	if tc.Priority != 2 {
		t.Errorf("expected priority 2 from tier, got %d", tc.Priority)
	}
	if tc.RateLimit == nil || tc.RateLimit.Rate != 1000 {
		t.Error("expected rate_limit from tier")
	}
	// Metadata should merge: tier's "plan","support" + tenant's "region"
	if tc.Metadata["plan"] != "enterprise" {
		t.Errorf("expected plan=enterprise from tier, got %s", tc.Metadata["plan"])
	}
	if tc.Metadata["region"] != "us-east-1" {
		t.Errorf("expected region=us-east-1 from tenant, got %s", tc.Metadata["region"])
	}
	if tc.Metadata["support"] != "premium" {
		t.Errorf("expected support=premium from tier, got %s", tc.Metadata["support"])
	}
	// Response headers from tier
	if tc.ResponseHeaders["X-Plan"] != "enterprise" {
		t.Errorf("expected X-Plan=enterprise from tier, got %s", tc.ResponseHeaders["X-Plan"])
	}
}

func TestManager_TierOverrideByTenant(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tiers: map[string]config.TenantTierConfig{
			"basic": {
				MaxBodySize: 1024,
				Priority:    5,
			},
		},
		Tenants: map[string]config.TenantConfig{
			"acme": {
				Tier:        "basic",
				MaxBodySize: 2048, // tenant overrides tier
				Priority:    3,    // tenant overrides tier
			},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	tc := m.ListTenants()["acme"]
	if tc.MaxBodySize != 2048 {
		t.Errorf("expected tenant override 2048, got %d", tc.MaxBodySize)
	}
	if tc.Priority != 3 {
		t.Errorf("expected tenant override 3, got %d", tc.Priority)
	}
}

func TestManager_ResponseHeaders(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {
				ResponseHeaders: map[string]string{
					"X-Custom": "value1",
					"X-Plan":   "enterprise",
				},
			},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false)
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-Custom") != "value1" {
		t.Errorf("expected X-Custom=value1, got %s", w.Header().Get("X-Custom"))
	}
	if w.Header().Get("X-Plan") != "enterprise" {
		t.Errorf("expected X-Plan=enterprise, got %s", w.Header().Get("X-Plan"))
	}
}

func TestManager_Analytics(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("hello"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	stats := m.Stats()
	tenants := stats["tenants"].(map[string]interface{})
	acmeStats := tenants["acme"].(map[string]interface{})
	analytics := acmeStats["analytics"].(map[string]interface{})

	if analytics["request_count"] != int64(1) {
		t.Errorf("expected request_count=1, got %v", analytics["request_count"])
	}
	if analytics["bytes_out"] != int64(5) {
		t.Errorf("expected bytes_out=5, got %v", analytics["bytes_out"])
	}
	if analytics["status_2xx"] != int64(1) {
		t.Errorf("expected status_2xx=1, got %v", analytics["status_2xx"])
	}
}

func TestManager_TenantTimeout(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"acme": {
				Timeout: 50 * time.Millisecond,
			},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	mw := m.Middleware(nil, false)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that context has a deadline
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("expected context to have a deadline from tenant timeout")
		}
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestManager_CRUD(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{
			"existing": {},
		},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	// Add a new tenant
	err := m.AddTenant("new-tenant", config.TenantConfig{
		RateLimit: &config.TenantRateLimitConfig{Rate: 50, Period: time.Second, Burst: 100},
		Metadata:  map[string]string{"plan": "starter"},
	})
	if err != nil {
		t.Fatalf("AddTenant: %v", err)
	}

	// Verify it exists
	tc, ok := m.GetTenant("new-tenant")
	if !ok {
		t.Fatal("GetTenant: new-tenant not found")
	}
	if tc.Metadata["plan"] != "starter" {
		t.Errorf("expected plan=starter, got %s", tc.Metadata["plan"])
	}

	// Cannot add duplicate
	if err := m.AddTenant("new-tenant", config.TenantConfig{}); err == nil {
		t.Error("expected error adding duplicate tenant")
	}

	// Update tenant
	err = m.UpdateTenant("new-tenant", config.TenantConfig{
		Metadata: map[string]string{"plan": "pro"},
	})
	if err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}
	tc, _ = m.GetTenant("new-tenant")
	if tc.Metadata["plan"] != "pro" {
		t.Errorf("expected plan=pro after update, got %s", tc.Metadata["plan"])
	}

	// Update non-existent
	if err := m.UpdateTenant("ghost", config.TenantConfig{}); err == nil {
		t.Error("expected error updating non-existent tenant")
	}

	// Remove tenant
	if err := m.RemoveTenant("new-tenant"); err != nil {
		t.Fatalf("RemoveTenant: %v", err)
	}
	if _, ok := m.GetTenant("new-tenant"); ok {
		t.Error("expected tenant to be removed")
	}

	// Remove non-existent
	if err := m.RemoveTenant("ghost"); err == nil {
		t.Error("expected error removing non-existent tenant")
	}

	// ListTenants should only have "existing"
	all := m.ListTenants()
	if len(all) != 1 {
		t.Errorf("expected 1 tenant, got %d", len(all))
	}
	if _, ok := all["existing"]; !ok {
		t.Error("expected 'existing' tenant in list")
	}
}

func TestManager_CRUD_WorksWithMiddleware(t *testing.T) {
	cfg := config.TenantsConfig{
		Enabled: true,
		Key:     "header:X-Tenant-ID",
		Tenants: map[string]config.TenantConfig{},
	}
	m := NewManager(cfg, nil)
	defer m.Close()

	// Add a tenant dynamically
	m.AddTenant("dynamic", config.TenantConfig{
		Metadata: map[string]string{"env": "test"},
	})

	mw := m.Middleware(nil, false)
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "dynamic")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-Tenant-ID") != "dynamic" {
		t.Errorf("expected X-Tenant-ID=dynamic, got %s", w.Header().Get("X-Tenant-ID"))
	}
}
