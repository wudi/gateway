package geo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

// mockProvider returns deterministic results for testing.
type mockProvider struct {
	results map[string]*GeoResult
}

func (m *mockProvider) Lookup(ip string) (*GeoResult, error) {
	if r, ok := m.results[ip]; ok {
		return r, nil
	}
	return &GeoResult{}, nil
}

func (m *mockProvider) Close() error { return nil }

func newMockProvider() *mockProvider {
	return &mockProvider{
		results: map[string]*GeoResult{
			"1.2.3.4":   {CountryCode: "US", CountryName: "United States", City: "New York"},
			"5.6.7.8":   {CountryCode: "CN", CountryName: "China", City: "Beijing"},
			"9.10.11.12": {CountryCode: "DE", CountryName: "Germany", City: "Berlin"},
			"13.14.15.16": {CountryCode: "US", CountryName: "United States", City: "Los Angeles"},
		},
	}
}

func makeRequest(ip string) *http.Request {
	r := httptest.NewRequest("GET", "/test", nil)
	r.RemoteAddr = ip + ":12345"
	return r
}

func TestDenyFirstDenyCountry(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:       true,
		DenyCountries: []string{"CN"},
		InjectHeaders: true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	// CN should be denied
	w := httptest.NewRecorder()
	r := makeRequest("5.6.7.8")
	if _, allowed := g.Handle(w, r); allowed {
		t.Error("expected CN to be denied")
	}
	if w.Code != http.StatusUnavailableForLegalReasons {
		t.Errorf("expected 451, got %d", w.Code)
	}

	var body map[string]interface{}
	json.NewDecoder(w.Body).Decode(&body)
	if body["error"] != "geo_restricted" {
		t.Errorf("expected geo_restricted error, got %v", body["error"])
	}

	// US should be allowed
	w = httptest.NewRecorder()
	r = makeRequest("1.2.3.4")
	if _, allowed := g.Handle(w, r); !allowed {
		t.Error("expected US to be allowed")
	}
}

func TestDenyFirstAllowCountry(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:        true,
		AllowCountries: []string{"US"},
		InjectHeaders:  true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	// US should be allowed
	w := httptest.NewRecorder()
	r := makeRequest("1.2.3.4")
	if _, allowed := g.Handle(w, r); !allowed {
		t.Error("expected US to be allowed")
	}

	// DE not in allow list → denied (deny_first: allow list present but not matched)
	w = httptest.NewRecorder()
	r = makeRequest("9.10.11.12")
	if _, allowed := g.Handle(w, r); allowed {
		t.Error("expected DE to be denied (not in allow list)")
	}
}

func TestAllowFirstDenyCountry(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:       true,
		DenyCountries: []string{"CN"},
		Order:         "allow_first",
		InjectHeaders: true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	// CN should be denied
	w := httptest.NewRecorder()
	r := makeRequest("5.6.7.8")
	if _, allowed := g.Handle(w, r); allowed {
		t.Error("expected CN to be denied")
	}

	// US should be allowed (not in deny list)
	w = httptest.NewRecorder()
	r = makeRequest("1.2.3.4")
	if _, allowed := g.Handle(w, r); !allowed {
		t.Error("expected US to be allowed")
	}
}

func TestAllowFirstAllowAndDenyCountry(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:        true,
		AllowCountries: []string{"US"},
		DenyCountries:  []string{"US"},
		Order:          "allow_first",
		InjectHeaders:  true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	// allow_first: US in allow list → allowed (takes priority)
	w := httptest.NewRecorder()
	r := makeRequest("1.2.3.4")
	if _, allowed := g.Handle(w, r); !allowed {
		t.Error("expected US to be allowed (allow_first, in allow list)")
	}
}

func TestDenyCities(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:       true,
		DenyCities:    []string{"Beijing"},
		InjectHeaders: true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	// Beijing denied
	w := httptest.NewRecorder()
	r := makeRequest("5.6.7.8")
	if _, allowed := g.Handle(w, r); allowed {
		t.Error("expected Beijing to be denied")
	}

	// New York allowed
	w = httptest.NewRecorder()
	r = makeRequest("1.2.3.4")
	if _, allowed := g.Handle(w, r); !allowed {
		t.Error("expected New York to be allowed")
	}
}

func TestAllowCities(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:     true,
		AllowCities: []string{"new york"},
		InjectHeaders: true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	// New York allowed
	w := httptest.NewRecorder()
	r := makeRequest("1.2.3.4")
	if _, allowed := g.Handle(w, r); !allowed {
		t.Error("expected New York to be allowed")
	}

	// Berlin not in allow list → denied
	w = httptest.NewRecorder()
	r = makeRequest("9.10.11.12")
	if _, allowed := g.Handle(w, r); allowed {
		t.Error("expected Berlin to be denied (not in allow list)")
	}
}

func TestShadowMode(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:       true,
		DenyCountries: []string{"CN"},
		ShadowMode:    true,
		InjectHeaders: true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	// CN would be denied, but shadow mode allows it
	w := httptest.NewRecorder()
	r := makeRequest("5.6.7.8")
	if _, allowed := g.Handle(w, r); !allowed {
		t.Error("expected CN to be allowed in shadow mode")
	}

	// Verify metrics: should be counted as allowed (shadow mode)
	if g.metrics.Allowed.Load() != 1 {
		t.Errorf("expected allowed=1, got %d", g.metrics.Allowed.Load())
	}
	if g.metrics.Denied.Load() != 0 {
		t.Errorf("expected denied=0 (shadow mode), got %d", g.metrics.Denied.Load())
	}
}

func TestHeaderInjection(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:       true,
		InjectHeaders: true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := makeRequest("1.2.3.4")
	r, _ = g.Handle(w, r)

	if r.Header.Get("X-Geo-Country") != "US" {
		t.Errorf("expected X-Geo-Country=US, got %q", r.Header.Get("X-Geo-Country"))
	}
	if r.Header.Get("X-Geo-City") != "New York" {
		t.Errorf("expected X-Geo-City=New York, got %q", r.Header.Get("X-Geo-City"))
	}
}

func TestHeaderInjectionDisabled(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:       true,
		InjectHeaders: false,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := makeRequest("1.2.3.4")
	r, _ = g.Handle(w, r)

	if r.Header.Get("X-Geo-Country") != "" {
		t.Errorf("expected no X-Geo-Country header, got %q", r.Header.Get("X-Geo-Country"))
	}
}

func TestNoRulesAllowAll(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:       true,
		InjectHeaders: true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	// No allow/deny rules → everything allowed
	w := httptest.NewRecorder()
	r := makeRequest("5.6.7.8")
	if _, allowed := g.Handle(w, r); !allowed {
		t.Error("expected all traffic to be allowed with no rules")
	}
}

func TestMetrics(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:       true,
		DenyCountries: []string{"CN"},
		InjectHeaders: true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	// Allow one
	w := httptest.NewRecorder()
	g.Handle(w, makeRequest("1.2.3.4"))

	// Deny one
	w = httptest.NewRecorder()
	g.Handle(w, makeRequest("5.6.7.8"))

	// Allow another
	w = httptest.NewRecorder()
	g.Handle(w, makeRequest("9.10.11.12"))

	if g.metrics.TotalRequests.Load() != 3 {
		t.Errorf("expected total=3, got %d", g.metrics.TotalRequests.Load())
	}
	if g.metrics.Allowed.Load() != 2 {
		t.Errorf("expected allowed=2, got %d", g.metrics.Allowed.Load())
	}
	if g.metrics.Denied.Load() != 1 {
		t.Errorf("expected denied=1, got %d", g.metrics.Denied.Load())
	}
}

func TestGeoByRoute(t *testing.T) {
	provider := newMockProvider()
	m := NewGeoByRoute()

	cfg1 := config.GeoConfig{
		Enabled:       true,
		DenyCountries: []string{"CN"},
		InjectHeaders: true,
	}
	cfg2 := config.GeoConfig{
		Enabled:        true,
		AllowCountries: []string{"DE"},
		InjectHeaders:  true,
	}

	if err := m.AddRoute("r1", cfg1, provider); err != nil {
		t.Fatal(err)
	}
	if err := m.AddRoute("r2", cfg2, provider); err != nil {
		t.Fatal(err)
	}

	if g := m.GetGeo("r1"); g == nil {
		t.Error("expected geo for r1")
	}
	if g := m.GetGeo("r2"); g == nil {
		t.Error("expected geo for r2")
	}
	if g := m.GetGeo("r3"); g != nil {
		t.Error("expected nil for r3")
	}

	ids := m.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := m.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats, got %d", len(stats))
	}
}

func TestHandleStoresGeoResultInContext(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:       true,
		InjectHeaders: true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := makeRequest("1.2.3.4")
	r, _ = g.Handle(w, r)

	result := GeoResultFromContext(r.Context())
	if result == nil {
		t.Fatal("expected GeoResult in context")
	}
	if result.CountryCode != "US" {
		t.Errorf("expected country US, got %q", result.CountryCode)
	}
	if result.CountryName != "United States" {
		t.Errorf("expected country name United States, got %q", result.CountryName)
	}
	if result.City != "New York" {
		t.Errorf("expected city New York, got %q", result.City)
	}
}

func TestGeoResultFromContext_Nil(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	result := GeoResultFromContext(r.Context())
	if result != nil {
		t.Error("expected nil GeoResult from empty context")
	}
}

func TestMergeGeoConfig(t *testing.T) {
	global := config.GeoConfig{
		Enabled:        true,
		Database:       "/path/to/db.mmdb",
		InjectHeaders:  true,
		AllowCountries: []string{"US", "CA"},
		Order:          "deny_first",
	}
	perRoute := config.GeoConfig{
		Enabled:        true,
		AllowCountries: []string{"DE"},
		Order:          "allow_first",
	}

	merged := MergeGeoConfig(perRoute, global)

	// Database should come from global
	if merged.Database != "/path/to/db.mmdb" {
		t.Errorf("expected database from global, got %q", merged.Database)
	}

	// AllowCountries should be overridden by per-route
	if len(merged.AllowCountries) != 1 || merged.AllowCountries[0] != "DE" {
		t.Errorf("expected AllowCountries=[DE], got %v", merged.AllowCountries)
	}

	// Order should be overridden by per-route
	if merged.Order != "allow_first" {
		t.Errorf("expected order=allow_first, got %q", merged.Order)
	}
}

func TestStatus(t *testing.T) {
	provider := newMockProvider()
	cfg := config.GeoConfig{
		Enabled:        true,
		AllowCountries: []string{"US"},
		DenyCities:     []string{"Beijing"},
		InjectHeaders:  true,
		ShadowMode:     true,
	}
	g, err := New("route1", cfg, provider)
	if err != nil {
		t.Fatal(err)
	}

	snap := g.Status()
	if !snap.Enabled {
		t.Error("expected enabled=true")
	}
	if !snap.ShadowMode {
		t.Error("expected shadow_mode=true")
	}
	if !snap.InjectHeaders {
		t.Error("expected inject_headers=true")
	}
	if snap.Order != "deny_first" {
		t.Errorf("expected order=deny_first, got %q", snap.Order)
	}
}
