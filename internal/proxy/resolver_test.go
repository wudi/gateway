package proxy

import (
	"testing"
	"time"
)

func TestNewResolverNil(t *testing.T) {
	r := NewResolver(nil, 0)
	if r != nil {
		t.Fatal("expected nil resolver for empty nameservers")
	}

	r = NewResolver([]string{}, 5*time.Second)
	if r != nil {
		t.Fatal("expected nil resolver for empty nameservers slice")
	}
}

func TestNewResolverCreated(t *testing.T) {
	r := NewResolver([]string{"10.0.0.53:53"}, 2*time.Second)
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
	if !r.PreferGo {
		t.Error("expected PreferGo to be true")
	}
}

func TestNewResolverDefaultTimeout(t *testing.T) {
	// Timeout <= 0 should default to 5s; just verify it doesn't panic
	r := NewResolver([]string{"10.0.0.53:53"}, 0)
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}

	r = NewResolver([]string{"10.0.0.53:53"}, -1*time.Second)
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
}

func TestNewResolverRoundRobin(t *testing.T) {
	// We can't easily test actual DNS resolution without a server,
	// but we can verify the Dial func is set by calling it against
	// a known-bad address and checking the nameserver it tried.
	// Instead, we verify structural correctness.
	servers := []string{"10.0.0.1:53", "10.0.0.2:53", "10.0.0.3:53"}
	r := NewResolver(servers, 1*time.Second)
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
	if r.Dial == nil {
		t.Fatal("expected Dial func to be set")
	}
}
