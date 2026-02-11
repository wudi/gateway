package realip

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractNoTrustedProxies(t *testing.T) {
	// No trusted CIDRs = legacy behavior (first XFF entry)
	c, err := New(nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.1:12345"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")

	ip := c.Extract(r)
	if ip != "1.2.3.4" {
		t.Errorf("Expected 1.2.3.4 (legacy: first XFF), got %s", ip)
	}
}

func TestExtractWithTrustedProxies(t *testing.T) {
	// Trust 10.0.0.0/8 and 192.168.0.0/16
	c, err := New([]string{"10.0.0.0/8", "192.168.0.0/16"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// RemoteAddr is trusted, XFF chain: client -> proxy1 -> proxy2
	// XFF: "1.2.3.4, 10.0.0.1, 10.0.0.2"
	// RemoteAddr: 192.168.1.1 (trusted)
	// Walking right-to-left: 10.0.0.2 (trusted), 10.0.0.1 (trusted), 1.2.3.4 (not trusted) -> return 1.2.3.4
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.1:12345"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1, 10.0.0.2")

	ip := c.Extract(r)
	if ip != "1.2.3.4" {
		t.Errorf("Expected 1.2.3.4, got %s", ip)
	}
}

func TestExtractUntrustedRemoteAddr(t *testing.T) {
	// Trust only 10.0.0.0/8
	c, err := New([]string{"10.0.0.0/8"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// RemoteAddr is NOT trusted — don't trust XFF at all
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:12345"
	r.Header.Set("X-Forwarded-For", "5.6.7.8, 10.0.0.1")

	ip := c.Extract(r)
	if ip != "1.2.3.4" {
		t.Errorf("Expected 1.2.3.4 (RemoteAddr, since not trusted), got %s", ip)
	}
}

func TestExtractSpoofedXFF(t *testing.T) {
	// Trust only 10.0.0.0/8
	c, err := New([]string{"10.0.0.0/8"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Client spoofs XFF but RemoteAddr is trusted proxy
	// XFF: "spoofed.ip, real.client, 10.0.0.1"
	// Remote: 10.0.0.2 (trusted)
	// Walk: 10.0.0.1 (trusted), real.client (not trusted) -> return "real.client"
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:12345"
	r.Header.Set("X-Forwarded-For", "spoofed.ip, 8.8.8.8, 10.0.0.1")

	ip := c.Extract(r)
	if ip != "8.8.8.8" {
		t.Errorf("Expected 8.8.8.8 (first untrusted from right), got %s", ip)
	}
}

func TestExtractXRealIP(t *testing.T) {
	// Trust 10.0.0.0/8, use X-Real-IP header
	c, err := New([]string{"10.0.0.0/8"}, []string{"X-Real-IP"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("X-Real-IP", "1.2.3.4")

	ip := c.Extract(r)
	if ip != "1.2.3.4" {
		t.Errorf("Expected 1.2.3.4, got %s", ip)
	}
}

func TestExtractNoHeaders(t *testing.T) {
	c, err := New([]string{"10.0.0.0/8"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Trusted proxy but no XFF headers
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"

	ip := c.Extract(r)
	if ip != "10.0.0.1" {
		t.Errorf("Expected 10.0.0.1 (RemoteAddr), got %s", ip)
	}
}

func TestExtractMaxHops(t *testing.T) {
	// Trust 10.0.0.0/8, max_hops=1
	c, err := New([]string{"10.0.0.0/8"}, nil, 1)
	if err != nil {
		t.Fatal(err)
	}

	// XFF: "1.1.1.1, 2.2.2.2, 10.0.0.1"
	// max_hops=1 means only walk back 1 hop
	// Walk: 10.0.0.1 (1 hop, trusted) -> 2.2.2.2 (2 hops, exceeds max_hops) -> return 2.2.2.2
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:12345"
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2, 10.0.0.1")

	ip := c.Extract(r)
	if ip != "2.2.2.2" {
		t.Errorf("Expected 2.2.2.2 (max_hops=1), got %s", ip)
	}
}

func TestExtractBareIP(t *testing.T) {
	// Test bare IP (no CIDR notation)
	c, err := New([]string{"10.0.0.1"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	ip := c.Extract(r)
	if ip != "1.2.3.4" {
		t.Errorf("Expected 1.2.3.4, got %s", ip)
	}
}

func TestNewInvalidCIDR(t *testing.T) {
	_, err := New([]string{"invalid"}, nil, 0)
	if err == nil {
		t.Error("Expected error for invalid CIDR")
	}
}

func TestMiddleware(t *testing.T) {
	c, err := New([]string{"10.0.0.0/8"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var extractedIP string
	handler := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extractedIP = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if extractedIP != "1.2.3.4" {
		t.Errorf("Expected 1.2.3.4 from context, got %s", extractedIP)
	}
}

func TestFromContextEmpty(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	ip := FromContext(r.Context())
	if ip != "" {
		t.Errorf("Expected empty string from context without middleware, got %s", ip)
	}
}

func TestStats(t *testing.T) {
	c, err := New([]string{"10.0.0.0/8", "172.16.0.0/12"}, []string{"X-Forwarded-For"}, 5)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	c.Extract(r)

	stats := c.Stats()
	if stats.TotalRequests != 1 {
		t.Errorf("Expected TotalRequests=1, got %d", stats.TotalRequests)
	}
	if stats.Extracted != 1 {
		t.Errorf("Expected Extracted=1, got %d", stats.Extracted)
	}
	if stats.TrustedCIDRs != 2 {
		t.Errorf("Expected TrustedCIDRs=2, got %d", stats.TrustedCIDRs)
	}
	if stats.MaxHops != 5 {
		t.Errorf("Expected MaxHops=5, got %d", stats.MaxHops)
	}
}

func TestExtractIPv6TrustedProxy(t *testing.T) {
	c, err := New([]string{"::1/128"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[::1]:12345"
	r.Header.Set("X-Forwarded-For", "2001:db8::1")

	ip := c.Extract(r)
	if ip != "2001:db8::1" {
		t.Errorf("Expected 2001:db8::1, got %s", ip)
	}
}

func TestCustomHeaderOrder(t *testing.T) {
	// Prefer X-Real-IP over X-Forwarded-For
	c, err := New([]string{"10.0.0.0/8"}, []string{"X-Real-IP", "X-Forwarded-For"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("X-Real-IP", "5.5.5.5")
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	ip := c.Extract(r)
	if ip != "5.5.5.5" {
		t.Errorf("Expected 5.5.5.5 (X-Real-IP preferred), got %s", ip)
	}
}
