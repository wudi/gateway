package cdnheaders

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func TestCacheControlInjection(t *testing.T) {
	cdn := New(config.CDNCacheConfig{
		Enabled:      true,
		CacheControl: "public, max-age=3600, s-maxage=86400",
	})

	h := make(http.Header)
	cdn.Apply(h, true)

	if got := h.Get("Cache-Control"); got != "public, max-age=3600, s-maxage=86400" {
		t.Errorf("expected Cache-Control, got %q", got)
	}
}

func TestVaryHeaders(t *testing.T) {
	cdn := New(config.CDNCacheConfig{
		Enabled: true,
		Vary:    []string{"Accept", "Accept-Encoding"},
	})

	h := make(http.Header)
	cdn.Apply(h, true)

	got := h.Get("Vary")
	if got != "Accept, Accept-Encoding" {
		t.Errorf("expected Vary=Accept, Accept-Encoding, got %q", got)
	}
}

func TestSurrogateControl(t *testing.T) {
	cdn := New(config.CDNCacheConfig{
		Enabled:          true,
		SurrogateControl: "max-age=86400",
		SurrogateKey:     "product-listing",
	})

	h := make(http.Header)
	cdn.Apply(h, true)

	if got := h.Get("Surrogate-Control"); got != "max-age=86400" {
		t.Errorf("expected Surrogate-Control, got %q", got)
	}
	if got := h.Get("Surrogate-Key"); got != "product-listing" {
		t.Errorf("expected Surrogate-Key, got %q", got)
	}
}

func TestOverrideMode(t *testing.T) {
	cdn := New(config.CDNCacheConfig{
		Enabled:      true,
		CacheControl: "public, max-age=300",
		Override:     boolPtr(true),
	})

	h := make(http.Header)
	h.Set("Cache-Control", "private, no-cache")
	cdn.Apply(h, cdn.IsOverride())

	if got := h.Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("expected override, got %q", got)
	}
}

func TestNoOverrideMode(t *testing.T) {
	cdn := New(config.CDNCacheConfig{
		Enabled:      true,
		CacheControl: "public, max-age=300",
		Override:     boolPtr(false),
	})

	h := make(http.Header)
	h.Set("Cache-Control", "private, no-cache")
	cdn.Apply(h, cdn.IsOverride())

	if got := h.Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("expected no override, got %q", got)
	}
}

func TestNoOverrideMode_NoExisting(t *testing.T) {
	cdn := New(config.CDNCacheConfig{
		Enabled:      true,
		CacheControl: "public, max-age=300",
		Override:     boolPtr(false),
	})

	h := make(http.Header)
	cdn.Apply(h, cdn.IsOverride())

	// When no existing Cache-Control, should set it
	if got := h.Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("expected Cache-Control set when missing, got %q", got)
	}
}

func TestStaleDirectives(t *testing.T) {
	cdn := New(config.CDNCacheConfig{
		Enabled:              true,
		CacheControl:         "public, max-age=3600",
		StaleWhileRevalidate: 300,
		StaleIfError:         600,
	})

	h := make(http.Header)
	cdn.Apply(h, true)

	got := h.Get("Cache-Control")
	if !strings.Contains(got, "stale-while-revalidate=300") {
		t.Errorf("expected stale-while-revalidate, got %q", got)
	}
	if !strings.Contains(got, "stale-if-error=600") {
		t.Errorf("expected stale-if-error, got %q", got)
	}
}

func TestExpiresDuration(t *testing.T) {
	cdn := New(config.CDNCacheConfig{
		Enabled: true,
		Expires: "1h",
	})

	h := make(http.Header)
	cdn.Apply(h, true)

	expires := h.Get("Expires")
	if expires == "" {
		t.Fatal("expected Expires header")
	}

	parsed, err := http.ParseTime(expires)
	if err != nil {
		t.Fatalf("failed to parse Expires: %v", err)
	}

	// Should be ~1 hour from now
	diff := time.Until(parsed)
	if diff < 59*time.Minute || diff > 61*time.Minute {
		t.Errorf("expected Expires ~1h from now, got %v", diff)
	}
}

func TestExpiresHTTPDate(t *testing.T) {
	httpDate := "Thu, 01 Jan 2099 00:00:00 GMT"
	cdn := New(config.CDNCacheConfig{
		Enabled: true,
		Expires: httpDate,
	})

	h := make(http.Header)
	cdn.Apply(h, true)

	if got := h.Get("Expires"); got != httpDate {
		t.Errorf("expected %q, got %q", httpDate, got)
	}
}

func TestMergeConfig(t *testing.T) {
	global := config.CDNCacheConfig{
		Enabled:      true,
		CacheControl: "public, max-age=60",
	}
	perRoute := config.CDNCacheConfig{
		Enabled:      true,
		CacheControl: "public, max-age=3600",
	}

	merged := MergeCDNCacheConfig(perRoute, global)
	if merged.CacheControl != "public, max-age=3600" {
		t.Errorf("expected per-route to take precedence, got %q", merged.CacheControl)
	}

	// Per-route disabled → use global
	disabled := config.CDNCacheConfig{Enabled: false}
	merged2 := MergeCDNCacheConfig(disabled, global)
	if merged2.CacheControl != "public, max-age=60" {
		t.Errorf("expected global when per-route disabled, got %q", merged2.CacheControl)
	}
}

func TestByRoute(t *testing.T) {
	br := NewCDNHeadersByRoute()

	br.AddRoute("route1", config.CDNCacheConfig{
		Enabled:      true,
		CacheControl: "public, max-age=60",
	})
	br.AddRoute("route2", config.CDNCacheConfig{
		Enabled:      true,
		CacheControl: "private",
	})

	h1 := br.GetHandler("route1")
	if h1 == nil {
		t.Fatal("expected handler for route1")
	}

	h3 := br.GetHandler("route3")
	if h3 != nil {
		t.Error("expected nil for non-existent route")
	}

	ids := br.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := br.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats entries, got %d", len(stats))
	}
}

func TestStats(t *testing.T) {
	cdn := New(config.CDNCacheConfig{
		Enabled:      true,
		CacheControl: "public, max-age=60",
	})

	h := make(http.Header)
	cdn.Apply(h, true)
	cdn.Apply(h, true)

	s := cdn.Stats()
	if s.Applied != 2 {
		t.Errorf("expected applied=2, got %d", s.Applied)
	}
}

func TestDefaultOverride(t *testing.T) {
	// Default should be override=true when not specified
	cdn := New(config.CDNCacheConfig{
		Enabled:      true,
		CacheControl: "public, max-age=60",
	})

	if !cdn.IsOverride() {
		t.Error("expected default override=true")
	}
}
