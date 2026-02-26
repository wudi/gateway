package loadbalancer

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestStickyPolicyCookieMode(t *testing.T) {
	sp := NewStickyPolicy(config.StickyConfig{
		Enabled:    true,
		Mode:       "cookie",
		CookieName: "X-Traffic-Group",
	})

	groups := []*TrafficGroup{
		{Name: "stable", Weight: 90},
		{Name: "canary", Weight: 10},
	}

	// No cookie — should return empty
	r := httptest.NewRequest("GET", "/", nil)
	if got := sp.ResolveGroup(r, groups); got != "" {
		t.Errorf("expected empty group without cookie, got %q", got)
	}

	// Valid cookie
	r = httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "X-Traffic-Group", Value: "canary"})
	if got := sp.ResolveGroup(r, groups); got != "canary" {
		t.Errorf("expected canary, got %q", got)
	}

	// Invalid cookie value (not a real group)
	r = httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "X-Traffic-Group", Value: "nonexistent"})
	if got := sp.ResolveGroup(r, groups); got != "" {
		t.Errorf("expected empty for invalid group, got %q", got)
	}
}

func TestStickyPolicyHashMode(t *testing.T) {
	sp := NewStickyPolicy(config.StickyConfig{
		Enabled: true,
		Mode:    "hash",
		HashKey: "X-User-ID",
	})

	groups := []*TrafficGroup{
		{Name: "stable", Weight: 90},
		{Name: "canary", Weight: 10},
	}

	// Same header value should always map to the same group
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-User-ID", "user-123")

	first := sp.ResolveGroup(r, groups)
	if first == "" {
		t.Fatal("expected non-empty group from hash")
	}

	for i := 0; i < 100; i++ {
		r2 := httptest.NewRequest("GET", "/", nil)
		r2.Header.Set("X-User-ID", "user-123")
		if got := sp.ResolveGroup(r2, groups); got != first {
			t.Fatalf("hash mode not deterministic: got %q, want %q", got, first)
		}
	}
}

func TestStickyPolicyHashFallbackToIP(t *testing.T) {
	sp := NewStickyPolicy(config.StickyConfig{
		Enabled: true,
		Mode:    "hash",
		HashKey: "X-User-ID",
	})

	groups := []*TrafficGroup{
		{Name: "stable", Weight: 50},
		{Name: "canary", Weight: 50},
	}

	// No header — falls back to RemoteAddr
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"

	got := sp.ResolveGroup(r, groups)
	if got == "" {
		t.Fatal("expected non-empty group from IP fallback")
	}

	// Same IP should get same group
	for i := 0; i < 50; i++ {
		r2 := httptest.NewRequest("GET", "/", nil)
		r2.RemoteAddr = "10.0.0.1:12345"
		if g := sp.ResolveGroup(r2, groups); g != got {
			t.Fatalf("IP fallback not deterministic: got %q, want %q", g, got)
		}
	}
}

func TestStickyPolicyHeaderMode(t *testing.T) {
	sp := NewStickyPolicy(config.StickyConfig{
		Enabled: true,
		Mode:    "header",
		HashKey: "X-Session",
	})

	groups := []*TrafficGroup{
		{Name: "a", Weight: 50},
		{Name: "b", Weight: 50},
	}

	// No header — should return empty
	r := httptest.NewRequest("GET", "/", nil)
	if got := sp.ResolveGroup(r, groups); got != "" {
		t.Errorf("expected empty without header, got %q", got)
	}

	// With header — deterministic
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Session", "sess-abc")
	first := sp.ResolveGroup(r, groups)
	if first == "" {
		t.Fatal("expected non-empty group")
	}

	for i := 0; i < 50; i++ {
		r2 := httptest.NewRequest("GET", "/", nil)
		r2.Header.Set("X-Session", "sess-abc")
		if got := sp.ResolveGroup(r2, groups); got != first {
			t.Fatalf("header mode not deterministic")
		}
	}
}

func TestStickyPolicySetCookie(t *testing.T) {
	sp := NewStickyPolicy(config.StickyConfig{
		Enabled:    true,
		Mode:       "cookie",
		CookieName: "my-group",
	})

	cookie := sp.SetCookie("canary")
	if cookie == nil {
		t.Fatal("expected cookie for cookie mode")
	}
	if cookie.Name != "my-group" {
		t.Errorf("expected cookie name my-group, got %s", cookie.Name)
	}
	if cookie.Value != "canary" {
		t.Errorf("expected cookie value canary, got %s", cookie.Value)
	}

	// Non-cookie mode should return nil
	sp2 := NewStickyPolicy(config.StickyConfig{
		Enabled: true,
		Mode:    "hash",
		HashKey: "X-User-ID",
	})
	if sp2.SetCookie("canary") != nil {
		t.Error("expected nil cookie for hash mode")
	}
}

func TestWeightedBalancerWithSticky(t *testing.T) {
	splits := []config.TrafficSplitConfig{
		{
			Name:   "stable",
			Weight: 90,
			Backends: []config.BackendConfig{
				{URL: "http://stable:8080", Weight: 1},
			},
		},
		{
			Name:   "canary",
			Weight: 10,
			Backends: []config.BackendConfig{
				{URL: "http://canary:8080", Weight: 1},
			},
		},
	}

	stickyCfg := config.StickyConfig{
		Enabled:    true,
		Mode:       "cookie",
		CookieName: "X-Traffic-Group",
	}

	wb := NewWeightedBalancerWithSticky(splits, stickyCfg)

	if !wb.HasStickyPolicy() {
		t.Fatal("expected sticky policy")
	}

	// Request with cookie should go to specified group
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "X-Traffic-Group", Value: "canary"})

	for i := 0; i < 100; i++ {
		backend, groupName := wb.NextForHTTPRequest(r)
		if backend == nil {
			t.Fatal("got nil backend")
		}
		if groupName != "canary" {
			t.Errorf("expected canary group, got %q", groupName)
		}
		if backend.URL != "http://canary:8080" {
			t.Errorf("expected canary backend, got %s", backend.URL)
		}
	}

	// Request without cookie should get random (but valid) group
	r2 := httptest.NewRequest("GET", "/", nil)
	backend, groupName := wb.NextForHTTPRequest(r2)
	if backend == nil {
		t.Fatal("got nil backend")
	}
	if groupName != "stable" && groupName != "canary" {
		t.Errorf("unexpected group name %q", groupName)
	}
}

func TestWeightedBalancerWithStickyHash(t *testing.T) {
	splits := []config.TrafficSplitConfig{
		{
			Name:   "stable",
			Weight: 50,
			Backends: []config.BackendConfig{
				{URL: "http://stable:8080", Weight: 1},
			},
		},
		{
			Name:   "canary",
			Weight: 50,
			Backends: []config.BackendConfig{
				{URL: "http://canary:8080", Weight: 1},
			},
		},
	}

	stickyCfg := config.StickyConfig{
		Enabled: true,
		Mode:    "hash",
		HashKey: "X-User-ID",
	}

	wb := NewWeightedBalancerWithSticky(splits, stickyCfg)

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-User-ID", "user-42")

	_, firstGroup := wb.NextForHTTPRequest(r)

	// Same user should always get the same group
	for i := 0; i < 100; i++ {
		r2 := httptest.NewRequest("GET", "/", nil)
		r2.Header.Set("X-User-ID", "user-42")
		_, g := wb.NextForHTTPRequest(r2)
		if g != firstGroup {
			t.Fatalf("hash sticky not deterministic: got %q, want %q", g, firstGroup)
		}
	}
}
