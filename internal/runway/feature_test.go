package runway

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/wudi/runway/config"
)

// --- feature.go tests ---

func TestNewFeature(t *testing.T) {
	t.Run("returns correct name", func(t *testing.T) {
		f := newFeature("test-feature", "/test", func(string, config.RouteConfig) error {
			return nil
		}, func() []string { return nil }, nil)

		if got := f.Name(); got != "test-feature" {
			t.Errorf("Name() = %q, want %q", got, "test-feature")
		}
	})

	t.Run("returns different names", func(t *testing.T) {
		names := []string{"cache", "rate-limit", "circuit-breaker", ""}
		for _, name := range names {
			f := newFeature(name, "", func(string, config.RouteConfig) error {
				return nil
			}, func() []string { return nil }, nil)
			if got := f.Name(); got != name {
				t.Errorf("Name() = %q, want %q", got, name)
			}
		}
	})
}

func TestNewFeatureSetup(t *testing.T) {
	t.Run("calls provided closure", func(t *testing.T) {
		var called bool
		var gotRouteID string
		f := newFeature("test", "", func(routeID string, cfg config.RouteConfig) error {
			called = true
			gotRouteID = routeID
			return nil
		}, func() []string { return nil }, nil)

		err := f.Setup("route-1", config.RouteConfig{})
		if err != nil {
			t.Fatalf("Setup() returned unexpected error: %v", err)
		}
		if !called {
			t.Error("Setup() did not call the provided closure")
		}
		if gotRouteID != "route-1" {
			t.Errorf("Setup() passed routeID = %q, want %q", gotRouteID, "route-1")
		}
	})

	t.Run("propagates error from closure", func(t *testing.T) {
		setupErr := errors.New("setup failed")
		f := newFeature("test", "", func(string, config.RouteConfig) error {
			return setupErr
		}, func() []string { return nil }, nil)

		err := f.Setup("route-1", config.RouteConfig{})
		if !errors.Is(err, setupErr) {
			t.Errorf("Setup() error = %v, want %v", err, setupErr)
		}
	})

	t.Run("receives route config", func(t *testing.T) {
		var gotPath string
		f := newFeature("test", "", func(routeID string, cfg config.RouteConfig) error {
			gotPath = cfg.Path
			return nil
		}, func() []string { return nil }, nil)

		err := f.Setup("r1", config.RouteConfig{Path: "/api/v1"})
		if err != nil {
			t.Fatalf("Setup() returned unexpected error: %v", err)
		}
		if gotPath != "/api/v1" {
			t.Errorf("Setup() cfg.Path = %q, want %q", gotPath, "/api/v1")
		}
	})
}

func TestNewFeatureRouteIDs(t *testing.T) {
	t.Run("returns provided list", func(t *testing.T) {
		expected := []string{"route-a", "route-b", "route-c"}
		f := newFeature("test", "", func(string, config.RouteConfig) error {
			return nil
		}, func() []string { return expected }, nil)

		got := f.RouteIDs()
		if len(got) != len(expected) {
			t.Fatalf("RouteIDs() returned %d items, want %d", len(got), len(expected))
		}
		for i, id := range got {
			if id != expected[i] {
				t.Errorf("RouteIDs()[%d] = %q, want %q", i, id, expected[i])
			}
		}
	})

	t.Run("returns nil when no routes", func(t *testing.T) {
		f := newFeature("test", "", func(string, config.RouteConfig) error {
			return nil
		}, func() []string { return nil }, nil)

		got := f.RouteIDs()
		if got != nil {
			t.Errorf("RouteIDs() = %v, want nil", got)
		}
	})

	t.Run("returns empty slice", func(t *testing.T) {
		f := newFeature("test", "", func(string, config.RouteConfig) error {
			return nil
		}, func() []string { return []string{} }, nil)

		got := f.RouteIDs()
		if len(got) != 0 {
			t.Errorf("RouteIDs() returned %d items, want 0", len(got))
		}
	})
}

func TestNewFeatureWithStats(t *testing.T) {
	t.Run("implements AdminStatsProvider", func(t *testing.T) {
		statsData := map[string]int{"hits": 42, "misses": 7}
		f := newFeature("cache", "/cache-stats", func(string, config.RouteConfig) error {
			return nil
		}, func() []string { return []string{"r1"} }, func() any {
			return statsData
		})

		asp, ok := f.(AdminStatsProvider)
		if !ok {
			t.Fatal("feature with non-nil stats should implement AdminStatsProvider")
		}

		gotStats, ok := asp.AdminStats().(map[string]int)
		if !ok {
			t.Fatal("AdminStats() returned unexpected type")
		}
		if gotStats["hits"] != 42 {
			t.Errorf("AdminStats()[hits] = %d, want 42", gotStats["hits"])
		}
		if gotStats["misses"] != 7 {
			t.Errorf("AdminStats()[misses] = %d, want 7", gotStats["misses"])
		}
	})

	t.Run("returns correct admin path", func(t *testing.T) {
		f := newFeature("mock", "/mock-responses", func(string, config.RouteConfig) error {
			return nil
		}, func() []string { return nil }, func() any { return nil })

		asp, ok := f.(AdminStatsProvider)
		if !ok {
			t.Fatal("feature with non-nil stats should implement AdminStatsProvider")
		}
		if got := asp.AdminPath(); got != "/mock-responses" {
			t.Errorf("AdminPath() = %q, want %q", got, "/mock-responses")
		}
	})

	t.Run("AdminStats returns dynamic data", func(t *testing.T) {
		counter := 0
		f := newFeature("counter", "/counter", func(string, config.RouteConfig) error {
			return nil
		}, func() []string { return nil }, func() any {
			counter++
			return counter
		})

		asp := f.(AdminStatsProvider)
		first := asp.AdminStats().(int)
		second := asp.AdminStats().(int)
		if first != 1 || second != 2 {
			t.Errorf("AdminStats() should return dynamic data: got %d, %d; want 1, 2", first, second)
		}
	})
}

func TestNewFeatureWithoutStats(t *testing.T) {
	t.Run("does not implement AdminStatsProvider", func(t *testing.T) {
		f := newFeature("plain", "", func(string, config.RouteConfig) error {
			return nil
		}, func() []string { return nil }, nil)

		if _, ok := f.(AdminStatsProvider); ok {
			t.Error("feature with nil stats should NOT implement AdminStatsProvider")
		}
	})

	t.Run("still implements Feature", func(t *testing.T) {
		f := newFeature("plain", "/path", func(string, config.RouteConfig) error {
			return nil
		}, func() []string { return []string{"r1"} }, nil)

		// Feature interface methods still work.
		if f.Name() != "plain" {
			t.Errorf("Name() = %q, want %q", f.Name(), "plain")
		}
		if ids := f.RouteIDs(); len(ids) != 1 || ids[0] != "r1" {
			t.Errorf("RouteIDs() = %v, want [r1]", ids)
		}
	})
}

func TestNoOpFeature(t *testing.T) {
	t.Run("Setup returns nil", func(t *testing.T) {
		f := noOpFeature("noop", "", func() []string { return nil }, nil)

		err := f.Setup("any-route", config.RouteConfig{Path: "/anything"})
		if err != nil {
			t.Errorf("noOpFeature Setup() = %v, want nil", err)
		}
	})

	t.Run("returns correct name", func(t *testing.T) {
		f := noOpFeature("my-noop", "", func() []string { return nil }, nil)
		if got := f.Name(); got != "my-noop" {
			t.Errorf("Name() = %q, want %q", got, "my-noop")
		}
	})

	t.Run("returns route IDs from closure", func(t *testing.T) {
		expected := []string{"r1", "r2"}
		f := noOpFeature("noop", "", func() []string { return expected }, nil)

		got := f.RouteIDs()
		if len(got) != len(expected) {
			t.Fatalf("RouteIDs() returned %d items, want %d", len(got), len(expected))
		}
		for i, id := range got {
			if id != expected[i] {
				t.Errorf("RouteIDs()[%d] = %q, want %q", i, id, expected[i])
			}
		}
	})

	t.Run("with stats implements AdminStatsProvider", func(t *testing.T) {
		f := noOpFeature("noop", "/noop-stats", func() []string { return nil }, func() any {
			return "stats-data"
		})

		asp, ok := f.(AdminStatsProvider)
		if !ok {
			t.Fatal("noOpFeature with non-nil stats should implement AdminStatsProvider")
		}
		if got := asp.AdminStats(); got != "stats-data" {
			t.Errorf("AdminStats() = %v, want %q", got, "stats-data")
		}
		if got := asp.AdminPath(); got != "/noop-stats" {
			t.Errorf("AdminPath() = %q, want %q", got, "/noop-stats")
		}
	})

	t.Run("without stats does not implement AdminStatsProvider", func(t *testing.T) {
		f := noOpFeature("noop", "", func() []string { return nil }, nil)

		if _, ok := f.(AdminStatsProvider); ok {
			t.Error("noOpFeature with nil stats should NOT implement AdminStatsProvider")
		}
	})

	t.Run("Setup is truly a no-op for multiple routes", func(t *testing.T) {
		f := noOpFeature("noop", "", func() []string { return nil }, nil)

		for i := 0; i < 10; i++ {
			if err := f.Setup(fmt.Sprintf("route-%d", i), config.RouteConfig{}); err != nil {
				t.Errorf("Setup(route-%d) = %v, want nil", i, err)
			}
		}
	})
}

// --- rwcaps.go tests ---

// mockStatusCapture is a test type that implements StatusCapture.
type mockStatusCapture struct {
	code int
}

func (m *mockStatusCapture) StatusCode() int { return m.code }

func TestStatusCaptureInterface(t *testing.T) {
	t.Run("mock implements StatusCapture", func(t *testing.T) {
		var sc StatusCapture = &mockStatusCapture{code: 200}
		if got := sc.StatusCode(); got != 200 {
			t.Errorf("StatusCode() = %d, want 200", got)
		}
	})

	t.Run("different status codes", func(t *testing.T) {
		codes := []int{200, 201, 301, 400, 404, 500, 502, 503}
		for _, code := range codes {
			sc := &mockStatusCapture{code: code}
			if got := sc.StatusCode(); got != code {
				t.Errorf("StatusCode() = %d, want %d", got, code)
			}
		}
	})
}

// --- external.go tests ---

func TestExternalOptions(t *testing.T) {
	t.Run("zero value", func(t *testing.T) {
		var opts ExternalOptions
		if opts.UseDefaults {
			t.Error("zero ExternalOptions.UseDefaults should be false")
		}
		if opts.CustomSlots != nil {
			t.Error("zero ExternalOptions.CustomSlots should be nil")
		}
		if opts.CustomGlobal != nil {
			t.Error("zero ExternalOptions.CustomGlobal should be nil")
		}
		if opts.ExternalFeatures != nil {
			t.Error("zero ExternalOptions.ExternalFeatures should be nil")
		}
	})

	t.Run("fields set correctly", func(t *testing.T) {
		opts := ExternalOptions{
			UseDefaults: true,
			CustomSlots: []CustomSlot{
				{Name: "slot-1", After: "auth", Before: "cache"},
			},
			CustomGlobal: []CustomGlobalSlot{
				{Name: "global-1", After: "logging"},
			},
			ExternalFeatures: []ExternalFeature{
				{Feature: &mockFeature{name: "ext-1"}},
			},
		}

		if !opts.UseDefaults {
			t.Error("UseDefaults should be true")
		}
		if len(opts.CustomSlots) != 1 {
			t.Fatalf("CustomSlots length = %d, want 1", len(opts.CustomSlots))
		}
		if opts.CustomSlots[0].Name != "slot-1" {
			t.Errorf("CustomSlots[0].Name = %q, want %q", opts.CustomSlots[0].Name, "slot-1")
		}
		if len(opts.CustomGlobal) != 1 {
			t.Fatalf("CustomGlobal length = %d, want 1", len(opts.CustomGlobal))
		}
		if opts.CustomGlobal[0].Name != "global-1" {
			t.Errorf("CustomGlobal[0].Name = %q, want %q", opts.CustomGlobal[0].Name, "global-1")
		}
		if len(opts.ExternalFeatures) != 1 {
			t.Fatalf("ExternalFeatures length = %d, want 1", len(opts.ExternalFeatures))
		}
		if opts.ExternalFeatures[0].Feature.Name() != "ext-1" {
			t.Errorf("ExternalFeatures[0].Feature.Name() = %q, want %q",
				opts.ExternalFeatures[0].Feature.Name(), "ext-1")
		}
	})
}

func TestCustomSlot(t *testing.T) {
	t.Run("fields", func(t *testing.T) {
		buildCalled := false
		cs := CustomSlot{
			Name:   "my-middleware",
			After:  "auth",
			Before: "cache",
			Build: func(routeID string, cfg config.RouteConfig) func(http.Handler) http.Handler {
				buildCalled = true
				return func(next http.Handler) http.Handler { return next }
			},
		}

		if cs.Name != "my-middleware" {
			t.Errorf("Name = %q, want %q", cs.Name, "my-middleware")
		}
		if cs.After != "auth" {
			t.Errorf("After = %q, want %q", cs.After, "auth")
		}
		if cs.Before != "cache" {
			t.Errorf("Before = %q, want %q", cs.Before, "cache")
		}

		mw := cs.Build("route-1", config.RouteConfig{})
		if !buildCalled {
			t.Error("Build was not called")
		}
		if mw == nil {
			t.Error("Build returned nil middleware")
		}
	})

	t.Run("Build receives route ID and config", func(t *testing.T) {
		var gotRouteID string
		var gotPath string
		cs := CustomSlot{
			Name: "test",
			Build: func(routeID string, cfg config.RouteConfig) func(http.Handler) http.Handler {
				gotRouteID = routeID
				gotPath = cfg.Path
				return func(next http.Handler) http.Handler { return next }
			},
		}

		cs.Build("api-route", config.RouteConfig{Path: "/api"})
		if gotRouteID != "api-route" {
			t.Errorf("Build received routeID = %q, want %q", gotRouteID, "api-route")
		}
		if gotPath != "/api" {
			t.Errorf("Build received cfg.Path = %q, want %q", gotPath, "/api")
		}
	})

	t.Run("Build can return nil middleware", func(t *testing.T) {
		cs := CustomSlot{
			Name: "conditional",
			Build: func(routeID string, cfg config.RouteConfig) func(http.Handler) http.Handler {
				return nil // no middleware for this route
			},
		}

		mw := cs.Build("route-1", config.RouteConfig{})
		if mw != nil {
			t.Error("Build should return nil for conditional skip")
		}
	})
}

func TestCustomGlobalSlot(t *testing.T) {
	t.Run("fields", func(t *testing.T) {
		cgs := CustomGlobalSlot{
			Name:   "global-mw",
			After:  "recovery",
			Before: "logging",
			Build: func(cfg *config.Config) func(http.Handler) http.Handler {
				return func(next http.Handler) http.Handler { return next }
			},
		}

		if cgs.Name != "global-mw" {
			t.Errorf("Name = %q, want %q", cgs.Name, "global-mw")
		}
		if cgs.After != "recovery" {
			t.Errorf("After = %q, want %q", cgs.After, "recovery")
		}
		if cgs.Before != "logging" {
			t.Errorf("Before = %q, want %q", cgs.Before, "logging")
		}
	})

	t.Run("Build receives config", func(t *testing.T) {
		var buildCalled bool
		cgs := CustomGlobalSlot{
			Name: "test",
			Build: func(cfg *config.Config) func(http.Handler) http.Handler {
				buildCalled = true
				return func(next http.Handler) http.Handler { return next }
			},
		}

		mw := cgs.Build(&config.Config{})
		if !buildCalled {
			t.Error("Build was not called")
		}
		if mw == nil {
			t.Error("Build returned nil middleware")
		}
	})
}

// mockFeature implements the Feature interface for testing ExternalFeature.
type mockFeature struct {
	name     string
	routeIDs []string
	setupErr error
	setupLog []string
}

func (m *mockFeature) Name() string { return m.name }
func (m *mockFeature) Setup(routeID string, cfg config.RouteConfig) error {
	m.setupLog = append(m.setupLog, routeID)
	return m.setupErr
}
func (m *mockFeature) RouteIDs() []string { return m.routeIDs }

func TestExternalFeature(t *testing.T) {
	t.Run("wraps Feature interface", func(t *testing.T) {
		mock := &mockFeature{
			name:     "ext-cache",
			routeIDs: []string{"r1", "r2"},
		}
		ef := ExternalFeature{Feature: mock}

		if got := ef.Feature.Name(); got != "ext-cache" {
			t.Errorf("Name() = %q, want %q", got, "ext-cache")
		}

		ids := ef.Feature.RouteIDs()
		if len(ids) != 2 {
			t.Fatalf("RouteIDs() length = %d, want 2", len(ids))
		}
		if ids[0] != "r1" || ids[1] != "r2" {
			t.Errorf("RouteIDs() = %v, want [r1 r2]", ids)
		}
	})

	t.Run("Setup delegates to wrapped feature", func(t *testing.T) {
		mock := &mockFeature{name: "ext"}
		ef := ExternalFeature{Feature: mock}

		err := ef.Feature.Setup("route-x", config.RouteConfig{Path: "/x"})
		if err != nil {
			t.Fatalf("Setup() = %v, want nil", err)
		}
		if len(mock.setupLog) != 1 || mock.setupLog[0] != "route-x" {
			t.Errorf("Setup log = %v, want [route-x]", mock.setupLog)
		}
	})

	t.Run("Setup propagates error", func(t *testing.T) {
		setupErr := errors.New("external setup failed")
		mock := &mockFeature{name: "ext", setupErr: setupErr}
		ef := ExternalFeature{Feature: mock}

		err := ef.Feature.Setup("route-y", config.RouteConfig{})
		if !errors.Is(err, setupErr) {
			t.Errorf("Setup() error = %v, want %v", err, setupErr)
		}
	})

	t.Run("ExternalAdminStatsProvider type assertion", func(t *testing.T) {
		// A feature that also provides admin stats.
		mock := &mockFeatureWithStats{
			mockFeature: mockFeature{name: "ext-stats", routeIDs: []string{"r1"}},
			stats:       map[string]int{"hits": 10},
			path:        "/ext-stats",
		}
		ef := ExternalFeature{Feature: mock}

		asp, ok := ef.Feature.(ExternalAdminStatsProvider)
		if !ok {
			t.Fatal("feature with stats should implement ExternalAdminStatsProvider")
		}
		if got := asp.AdminPath(); got != "/ext-stats" {
			t.Errorf("AdminPath() = %q, want %q", got, "/ext-stats")
		}
		gotStats, ok := asp.AdminStats().(map[string]int)
		if !ok {
			t.Fatal("AdminStats() returned unexpected type")
		}
		if gotStats["hits"] != 10 {
			t.Errorf("AdminStats()[hits] = %d, want 10", gotStats["hits"])
		}
	})

	t.Run("feature without stats does not implement ExternalAdminStatsProvider", func(t *testing.T) {
		mock := &mockFeature{name: "plain"}
		ef := ExternalFeature{Feature: mock}

		if _, ok := ef.Feature.(ExternalAdminStatsProvider); ok {
			t.Error("plain feature should NOT implement ExternalAdminStatsProvider")
		}
	})
}

// mockFeatureWithStats extends mockFeature with ExternalAdminStatsProvider.
type mockFeatureWithStats struct {
	mockFeature
	stats any
	path  string
}

func (m *mockFeatureWithStats) AdminStats() any  { return m.stats }
func (m *mockFeatureWithStats) AdminPath() string { return m.path }
