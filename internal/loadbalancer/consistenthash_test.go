package loadbalancer

import (
	"net/http"
	"testing"

	"github.com/wudi/gateway/config"
)

func TestConsistentHashSameKeySameBackend(t *testing.T) {
	backends := []*Backend{
		{URL: "http://a:8080", Weight: 1, Healthy: true},
		{URL: "http://b:8080", Weight: 1, Healthy: true},
		{URL: "http://c:8080", Weight: 1, Healthy: true},
	}
	ch := NewConsistentHash(backends, config.ConsistentHashConfig{
		Key: "header", HeaderName: "X-User-ID",
	})

	req1, _ := http.NewRequest("GET", "/test", nil)
	req1.Header.Set("X-User-ID", "user-42")
	b1, _ := ch.NextForHTTPRequest(req1)

	req2, _ := http.NewRequest("GET", "/other", nil)
	req2.Header.Set("X-User-ID", "user-42")
	b2, _ := ch.NextForHTTPRequest(req2)

	if b1 == nil || b2 == nil {
		t.Fatal("expected non-nil backends")
	}
	if b1.URL != b2.URL {
		t.Fatalf("same key should map to same backend: got %s and %s", b1.URL, b2.URL)
	}
}

func TestConsistentHashDifferentKeysDistribute(t *testing.T) {
	backends := []*Backend{
		{URL: "http://a:8080", Weight: 1, Healthy: true},
		{URL: "http://b:8080", Weight: 1, Healthy: true},
		{URL: "http://c:8080", Weight: 1, Healthy: true},
	}
	ch := NewConsistentHash(backends, config.ConsistentHashConfig{
		Key: "header", HeaderName: "X-User-ID",
	})

	hits := make(map[string]int)
	for i := 0; i < 300; i++ {
		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("X-User-ID", string(rune('A'+i%26))+string(rune('0'+i/26)))
		b, _ := ch.NextForHTTPRequest(req)
		if b != nil {
			hits[b.URL]++
		}
	}

	// Should use at least 2 of 3 backends
	if len(hits) < 2 {
		t.Fatalf("expected distribution across backends, got %v", hits)
	}
}

func TestConsistentHashMinimalRedistribution(t *testing.T) {
	backends := []*Backend{
		{URL: "http://a:8080", Weight: 1, Healthy: true},
		{URL: "http://b:8080", Weight: 1, Healthy: true},
		{URL: "http://c:8080", Weight: 1, Healthy: true},
	}
	ch := NewConsistentHash(backends, config.ConsistentHashConfig{
		Key: "path",
	})

	// Record mappings before removal
	type mapping struct {
		key     string
		backend string
	}
	var before []mapping
	for i := 0; i < 100; i++ {
		path := "/" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		req, _ := http.NewRequest("GET", path, nil)
		b, _ := ch.NextForHTTPRequest(req)
		if b != nil {
			before = append(before, mapping{path, b.URL})
		}
	}

	// Remove one backend
	ch.MarkUnhealthy("http://b:8080")

	// Check how many keys moved
	moved := 0
	for _, m := range before {
		if m.backend == "http://b:8080" {
			moved++ // these must move
			continue
		}
		req, _ := http.NewRequest("GET", m.key, nil)
		b, _ := ch.NextForHTTPRequest(req)
		if b != nil && b.URL != m.backend {
			moved++
		}
	}

	// With consistent hashing, only keys that were on b should move
	// Allow some margin for ring overlap
	maxExpected := len(before)/2 + 10
	if moved > maxExpected {
		t.Fatalf("too many keys moved after removing one backend: %d (max expected %d)", moved, maxExpected)
	}
}

func TestConsistentHashAllUnhealthy(t *testing.T) {
	backends := []*Backend{
		{URL: "http://a:8080", Weight: 1, Healthy: false},
		{URL: "http://b:8080", Weight: 1, Healthy: false},
	}
	ch := NewConsistentHash(backends, config.ConsistentHashConfig{Key: "ip"})

	if b := ch.Next(); b != nil {
		t.Fatalf("expected nil, got %v", b)
	}

	req, _ := http.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	b, _ := ch.NextForHTTPRequest(req)
	if b != nil {
		t.Fatalf("expected nil for all unhealthy, got %v", b)
	}
}

func TestConsistentHashIPMode(t *testing.T) {
	backends := []*Backend{
		{URL: "http://a:8080", Weight: 1, Healthy: true},
		{URL: "http://b:8080", Weight: 1, Healthy: true},
	}
	ch := NewConsistentHash(backends, config.ConsistentHashConfig{Key: "ip"})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	b1, _ := ch.NextForHTTPRequest(req)

	req2, _ := http.NewRequest("GET", "/other", nil)
	req2.RemoteAddr = "10.0.0.1:54321"
	b2, _ := ch.NextForHTTPRequest(req2)

	if b1 == nil || b2 == nil {
		t.Fatal("expected non-nil backends")
	}
	if b1.URL != b2.URL {
		t.Fatalf("same IP should map to same backend: got %s and %s", b1.URL, b2.URL)
	}
}

func TestConsistentHashCookieMode(t *testing.T) {
	backends := []*Backend{
		{URL: "http://a:8080", Weight: 1, Healthy: true},
		{URL: "http://b:8080", Weight: 1, Healthy: true},
	}
	ch := NewConsistentHash(backends, config.ConsistentHashConfig{
		Key: "cookie", HeaderName: "session_id",
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "abc123"})
	b1, _ := ch.NextForHTTPRequest(req)

	req2, _ := http.NewRequest("GET", "/other", nil)
	req2.AddCookie(&http.Cookie{Name: "session_id", Value: "abc123"})
	b2, _ := ch.NextForHTTPRequest(req2)

	if b1 == nil || b2 == nil {
		t.Fatal("expected non-nil backends")
	}
	if b1.URL != b2.URL {
		t.Fatalf("same cookie should map to same backend: got %s and %s", b1.URL, b2.URL)
	}
}

func TestConsistentHashDefaultReplicas(t *testing.T) {
	backends := []*Backend{
		{URL: "http://a:8080", Weight: 1, Healthy: true},
	}
	ch := NewConsistentHash(backends, config.ConsistentHashConfig{Key: "ip"})

	if ch.replicas != 150 {
		t.Fatalf("expected default replicas 150, got %d", ch.replicas)
	}
}
