package luautil

import (
	"net/http/httptest"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/wudi/gateway/internal/middleware/geo"
	"github.com/wudi/gateway/internal/variables"
)

func TestContextUserData_RouteID(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{RouteID: "my-route"}

	L.SetGlobal("ctx", NewContextUserData(L, r, varCtx))
	if err := L.DoString(`result = ctx:route_id()`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "my-route" {
		t.Errorf("expected 'my-route', got %s", L.GetGlobal("result").String())
	}
}

func TestContextUserData_RequestID(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{RequestID: "req-123"}

	L.SetGlobal("ctx", NewContextUserData(L, r, varCtx))
	if err := L.DoString(`result = ctx:request_id()`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "req-123" {
		t.Errorf("expected 'req-123', got %s", L.GetGlobal("result").String())
	}
}

func TestContextUserData_TenantID(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{TenantID: "tenant-abc"}

	L.SetGlobal("ctx", NewContextUserData(L, r, varCtx))
	if err := L.DoString(`result = ctx:tenant_id()`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "tenant-abc" {
		t.Errorf("expected 'tenant-abc', got %s", L.GetGlobal("result").String())
	}
}

func TestContextUserData_Auth(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{
		Identity: &variables.Identity{
			ClientID: "client-1",
			AuthType: "jwt",
			Claims:   map[string]interface{}{"sub": "user-42", "admin": true, "level": float64(5)},
		},
	}

	L.SetGlobal("ctx", NewContextUserData(L, r, varCtx))
	if err := L.DoString(`
		client_id = ctx:client_id()
		auth_type = ctx:auth_type()
		claim_sub = ctx:claim("sub")
		claim_admin = ctx:claim("admin")
		claim_level = ctx:claim("level")
		claim_missing = ctx:claim("nonexistent")
	`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("client_id").String() != "client-1" {
		t.Errorf("expected client_id 'client-1', got %s", L.GetGlobal("client_id").String())
	}
	if L.GetGlobal("auth_type").String() != "jwt" {
		t.Errorf("expected auth_type 'jwt', got %s", L.GetGlobal("auth_type").String())
	}
	if L.GetGlobal("claim_sub").String() != "user-42" {
		t.Errorf("expected claim sub 'user-42', got %s", L.GetGlobal("claim_sub").String())
	}
	if L.GetGlobal("claim_admin") != lua.LTrue {
		t.Errorf("expected claim admin true, got %v", L.GetGlobal("claim_admin"))
	}
	if L.GetGlobal("claim_level").(lua.LNumber) != 5 {
		t.Errorf("expected claim level 5, got %v", L.GetGlobal("claim_level"))
	}
	if L.GetGlobal("claim_missing").String() != "" {
		t.Errorf("expected empty string for missing claim, got %s", L.GetGlobal("claim_missing").String())
	}
}

func TestContextUserData_Geo(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	ctx := geo.WithGeoResult(r.Context(), &geo.GeoResult{
		CountryCode: "US",
		City:        "New York",
	})
	r = r.WithContext(ctx)
	varCtx := &variables.Context{}

	L.SetGlobal("ctx", NewContextUserData(L, r, varCtx))
	if err := L.DoString(`
		country = ctx:geo_country()
		city = ctx:geo_city()
	`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("country").String() != "US" {
		t.Errorf("expected country 'US', got %s", L.GetGlobal("country").String())
	}
	if L.GetGlobal("city").String() != "New York" {
		t.Errorf("expected city 'New York', got %s", L.GetGlobal("city").String())
	}
}

func TestContextUserData_PathParam(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{
		PathParams: map[string]string{"id": "42", "name": "test"},
	}

	L.SetGlobal("ctx", NewContextUserData(L, r, varCtx))
	if err := L.DoString(`
		id = ctx:path_param("id")
		name = ctx:path_param("name")
		missing = ctx:path_param("nonexistent")
	`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("id").String() != "42" {
		t.Errorf("expected id '42', got %s", L.GetGlobal("id").String())
	}
	if L.GetGlobal("name").String() != "test" {
		t.Errorf("expected name 'test', got %s", L.GetGlobal("name").String())
	}
	if L.GetGlobal("missing").String() != "" {
		t.Errorf("expected empty for missing param, got %s", L.GetGlobal("missing").String())
	}
}

func TestContextUserData_CustomVars(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{
		Custom: map[string]string{"existing": "value"},
	}

	L.SetGlobal("ctx", NewContextUserData(L, r, varCtx))
	if err := L.DoString(`
		existing = ctx:get_var("existing")
		ctx:set_var("new_key", "new_value")
		new_val = ctx:get_var("new_key")
	`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("existing").String() != "value" {
		t.Errorf("expected existing 'value', got %s", L.GetGlobal("existing").String())
	}
	if L.GetGlobal("new_val").String() != "new_value" {
		t.Errorf("expected new_val 'new_value', got %s", L.GetGlobal("new_val").String())
	}
	// Verify Go-side change
	if varCtx.Custom["new_key"] != "new_value" {
		t.Errorf("expected varCtx.Custom['new_key'] = 'new_value', got %s", varCtx.Custom["new_key"])
	}
}

func TestContextUserData_NilVarCtx(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)

	L.SetGlobal("ctx", NewContextUserData(L, r, nil))
	if err := L.DoString(`
		route_id = ctx:route_id()
		client_id = ctx:client_id()
		claim = ctx:claim("sub")
	`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("route_id").String() != "" {
		t.Errorf("expected empty route_id, got %s", L.GetGlobal("route_id").String())
	}
	if L.GetGlobal("client_id").String() != "" {
		t.Errorf("expected empty client_id, got %s", L.GetGlobal("client_id").String())
	}
}

func TestContextUserData_SetVarNilCustom(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := &variables.Context{} // Custom is nil

	L.SetGlobal("ctx", NewContextUserData(L, r, varCtx))
	if err := L.DoString(`ctx:set_var("key", "val")`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if varCtx.Custom["key"] != "val" {
		t.Errorf("expected Custom['key'] = 'val', got %s", varCtx.Custom["key"])
	}
}
