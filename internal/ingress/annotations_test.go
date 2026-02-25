package ingress

import (
	"testing"
	"time"
)

func TestAnnotationParserGetString(t *testing.T) {
	p := NewAnnotationParser(map[string]string{
		AnnLoadBalancer: "least_conn",
	})
	if v := p.GetString(AnnLoadBalancer, "round_robin"); v != "least_conn" {
		t.Errorf("expected least_conn, got %s", v)
	}
	if v := p.GetString(AnnTimeout, "30s"); v != "30s" {
		t.Errorf("expected default 30s, got %s", v)
	}
}

func TestAnnotationParserGetBool(t *testing.T) {
	p := NewAnnotationParser(map[string]string{
		AnnCORSEnabled:    "true",
		AnnCircuitBreaker: "false",
	})
	if !p.GetBool(AnnCORSEnabled, false) {
		t.Error("expected true for cors-enabled")
	}
	if p.GetBool(AnnCircuitBreaker, true) {
		t.Error("expected false for circuit-breaker")
	}
	if !p.GetBool(AnnAuthRequired, true) {
		t.Error("expected default true for missing annotation")
	}
}

func TestAnnotationParserGetInt(t *testing.T) {
	p := NewAnnotationParser(map[string]string{
		AnnRetryMax: "3",
	})
	if v := p.GetInt(AnnRetryMax, 0); v != 3 {
		t.Errorf("expected 3, got %d", v)
	}
	if v := p.GetInt(AnnRateLimit, 100); v != 100 {
		t.Errorf("expected default 100, got %d", v)
	}
}

func TestAnnotationParserGetDuration(t *testing.T) {
	p := NewAnnotationParser(map[string]string{
		AnnTimeout: "5s",
	})
	if v := p.GetDuration(AnnTimeout, 30*time.Second); v != 5*time.Second {
		t.Errorf("expected 5s, got %v", v)
	}
	if v := p.GetDuration(AnnRateLimit, 10*time.Second); v != 10*time.Second {
		t.Errorf("expected default 10s, got %v", v)
	}
}

func TestAnnotationParserGetBoolInvalid(t *testing.T) {
	p := NewAnnotationParser(map[string]string{
		AnnCORSEnabled: "notabool",
	})
	if p.GetBool(AnnCORSEnabled, false) {
		t.Error("expected default false for invalid bool")
	}
}
