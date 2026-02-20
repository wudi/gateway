package ipblocklist

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func TestParseEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		count   int
		err     bool
	}{
		{"empty", nil, 0, false},
		{"single IP", []string{"1.2.3.4"}, 1, false},
		{"CIDR", []string{"10.0.0.0/8"}, 1, false},
		{"mixed", []string{"1.2.3.4", "10.0.0.0/8"}, 2, false},
		{"IPv6", []string{"::1"}, 1, false},
		{"IPv6 CIDR", []string{"fc00::/7"}, 1, false},
		{"invalid", []string{"not-an-ip"}, 0, true},
		{"invalid CIDR", []string{"1.2.3.4/abc"}, 0, true},
		{"whitespace", []string{"  1.2.3.4  ", ""}, 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nets, err := parseEntries(tt.entries)
			if tt.err {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(nets) != tt.count {
				t.Errorf("expected %d nets, got %d", tt.count, len(nets))
			}
		})
	}
}

func TestParseTextFeed(t *testing.T) {
	input := `# Comment line
1.2.3.4
10.0.0.0/8

# Another comment
192.168.1.1
`
	entries := parseTextFeed(strings.NewReader(input))
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d: %v", len(entries), entries)
	}
}

func TestCheck(t *testing.T) {
	bl, err := New(config.IPBlocklistConfig{
		Enabled: true,
		Static:  []string{"1.2.3.4", "10.0.0.0/8"},
		Action:  "block",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bl.Close()

	tests := []struct {
		ip      string
		blocked bool
	}{
		{"1.2.3.4", true},
		{"1.2.3.5", false},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"192.168.1.1", false},
		{"8.8.8.8", false},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		got := bl.Check(ip)
		if got != tt.blocked {
			t.Errorf("Check(%s) = %v, want %v", tt.ip, got, tt.blocked)
		}
	}
}

func TestMiddlewareBlock(t *testing.T) {
	bl, _ := New(config.IPBlocklistConfig{
		Enabled: true,
		Static:  []string{"192.168.1.0/24"},
		Action:  "block",
	})
	defer bl.Close()

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	handler := bl.Middleware()(backend)

	// Blocked IP
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.RemoteAddr = "192.168.1.50:1234"
	handler.ServeHTTP(w, r)

	if w.Code != 403 {
		t.Errorf("expected 403, got %d", w.Code)
	}

	// Allowed IP
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/test", nil)
	r2.RemoteAddr = "8.8.8.8:1234"
	handler.ServeHTTP(w2, r2)

	if w2.Code != 200 {
		t.Errorf("expected 200, got %d", w2.Code)
	}
}

func TestMiddlewareLogMode(t *testing.T) {
	bl, _ := New(config.IPBlocklistConfig{
		Enabled: true,
		Static:  []string{"192.168.1.0/24"},
		Action:  "log",
	})
	defer bl.Close()

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	handler := bl.Middleware()(backend)

	// Matched IP in log mode - should pass through
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.RemoteAddr = "192.168.1.50:1234"
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200 in log mode, got %d", w.Code)
	}

	if bl.metrics.LoggedHits.Load() != 1 {
		t.Errorf("expected 1 logged hit, got %d", bl.metrics.LoggedHits.Load())
	}
}

func TestFeedRefresh(t *testing.T) {
	// Create a test feed server
	feedEntries := []string{"1.2.3.0/24", "5.6.7.8"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, e := range feedEntries {
			fmt.Fprintln(w, e)
		}
	}))
	defer srv.Close()

	bl, err := New(config.IPBlocklistConfig{
		Enabled: true,
		Feeds: []config.IPBlocklistFeed{{
			URL:             srv.URL,
			RefreshInterval: time.Second,
			Format:          "text",
		}},
		Action: "block",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bl.Close()

	// Wait for initial feed refresh
	time.Sleep(200 * time.Millisecond)

	if !bl.Check(net.ParseIP("1.2.3.4")) {
		t.Error("expected 1.2.3.4 to be blocked (from feed)")
	}
	if !bl.Check(net.ParseIP("5.6.7.8")) {
		t.Error("expected 5.6.7.8 to be blocked (from feed)")
	}
	if bl.Check(net.ParseIP("9.9.9.9")) {
		t.Error("expected 9.9.9.9 to not be blocked")
	}
}

func TestFeedJSON(t *testing.T) {
	entries := []string{"1.2.3.4", "10.0.0.0/8"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	bl, err := New(config.IPBlocklistConfig{
		Enabled: true,
		Feeds: []config.IPBlocklistFeed{{
			URL:             srv.URL,
			RefreshInterval: time.Second,
			Format:          "json",
		}},
		Action: "block",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bl.Close()

	// Wait for initial feed refresh
	time.Sleep(200 * time.Millisecond)

	if !bl.Check(net.ParseIP("1.2.3.4")) {
		t.Error("expected 1.2.3.4 to be blocked (from JSON feed)")
	}
}

func TestForceRefresh(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		fmt.Fprintln(w, "1.2.3.4")
	}))
	defer srv.Close()

	bl, _ := New(config.IPBlocklistConfig{
		Enabled: true,
		Feeds: []config.IPBlocklistFeed{{
			URL:             srv.URL,
			RefreshInterval: time.Hour, // long interval
			Format:          "text",
		}},
	})
	defer bl.Close()

	// Wait for initial refresh
	time.Sleep(200 * time.Millisecond)
	initial := callCount

	// Force refresh
	bl.ForceRefresh()
	if callCount <= initial {
		t.Error("expected force refresh to trigger fetch")
	}
}

func TestStatus(t *testing.T) {
	bl, _ := New(config.IPBlocklistConfig{
		Enabled: true,
		Static:  []string{"1.2.3.4"},
		Action:  "block",
	})
	defer bl.Close()

	st := bl.Status()
	if st.Action != "block" {
		t.Errorf("expected action=block, got %s", st.Action)
	}
	if st.StaticEntries != 1 {
		t.Errorf("expected 1 static entry, got %d", st.StaticEntries)
	}
}

func TestMergeIPBlocklistConfig(t *testing.T) {
	global := config.IPBlocklistConfig{
		Enabled: true,
		Static:  []string{"1.2.3.4"},
		Action:  "block",
		Feeds: []config.IPBlocklistFeed{{
			URL: "http://example.com/global",
		}},
	}

	perRoute := config.IPBlocklistConfig{
		Enabled: true,
		Static:  []string{"5.6.7.8"},
		Action:  "log",
		Feeds: []config.IPBlocklistFeed{{
			URL: "http://example.com/route",
		}},
	}

	merged := MergeIPBlocklistConfig(perRoute, global)

	if len(merged.Static) != 2 {
		t.Errorf("expected 2 static entries, got %d", len(merged.Static))
	}
	if len(merged.Feeds) != 2 {
		t.Errorf("expected 2 feeds, got %d", len(merged.Feeds))
	}
	if merged.Action != "log" {
		t.Errorf("expected action=log (per-route override), got %s", merged.Action)
	}
}

func TestBlocklistByRoute(t *testing.T) {
	m := NewBlocklistByRoute()

	err := m.AddRoute("route-1", config.IPBlocklistConfig{
		Enabled: true,
		Static:  []string{"1.2.3.4"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if bl := m.GetBlocklist("route-1"); bl == nil {
		t.Error("expected blocklist for route-1")
	}
	if bl := m.GetBlocklist("route-2"); bl != nil {
		t.Error("expected nil for non-existent route-2")
	}

	stats := m.Stats()
	if _, ok := stats["route-1"]; !ok {
		t.Error("expected stats for route-1")
	}
}

func TestDefaultAction(t *testing.T) {
	bl, _ := New(config.IPBlocklistConfig{
		Enabled: true,
		Static:  []string{"1.2.3.4"},
	})
	defer bl.Close()

	if bl.action != "block" {
		t.Errorf("expected default action=block, got %s", bl.action)
	}
}
