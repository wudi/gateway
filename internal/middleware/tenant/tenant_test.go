package tenant

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
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
