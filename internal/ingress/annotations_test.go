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

func TestAnnotationParserHas(t *testing.T) {
	tests := []struct {
		name   string
		annots map[string]string
		key    string
		want   bool
	}{
		{
			name:   "present key",
			annots: map[string]string{AnnTimeout: "5s"},
			key:    AnnTimeout,
			want:   true,
		},
		{
			name:   "missing key",
			annots: map[string]string{AnnTimeout: "5s"},
			key:    AnnRetryMax,
			want:   false,
		},
		{
			name:   "present with empty value",
			annots: map[string]string{AnnTimeout: ""},
			key:    AnnTimeout,
			want:   true,
		},
		{
			name:   "empty map",
			annots: map[string]string{},
			key:    AnnTimeout,
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewAnnotationParser(tt.annots)
			if got := p.Has(tt.key); got != tt.want {
				t.Errorf("Has(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestAnnotationParserGetIntInvalid(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		defaultVal int
		want       int
	}{
		{
			name:       "non-numeric string",
			value:      "abc",
			defaultVal: 42,
			want:       42,
		},
		{
			name:       "float value",
			value:      "3.14",
			defaultVal: 7,
			want:       7,
		},
		{
			name:       "empty string",
			value:      "",
			defaultVal: 99,
			want:       99,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewAnnotationParser(map[string]string{
				AnnRetryMax: tt.value,
			})
			if got := p.GetInt(AnnRetryMax, tt.defaultVal); got != tt.want {
				t.Errorf("GetInt() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAnnotationParserGetDurationInvalid(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		defaultVal time.Duration
		want       time.Duration
	}{
		{
			name:       "non-duration string",
			value:      "notaduration",
			defaultVal: 30 * time.Second,
			want:       30 * time.Second,
		},
		{
			name:       "bare number without unit",
			value:      "100",
			defaultVal: 5 * time.Second,
			want:       5 * time.Second,
		},
		{
			name:       "empty string",
			value:      "",
			defaultVal: 15 * time.Second,
			want:       15 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewAnnotationParser(map[string]string{
				AnnTimeout: tt.value,
			})
			if got := p.GetDuration(AnnTimeout, tt.defaultVal); got != tt.want {
				t.Errorf("GetDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnnotationParserGetStringEmpty(t *testing.T) {
	tests := []struct {
		name       string
		annots     map[string]string
		key        string
		defaultVal string
		want       string
	}{
		{
			name:       "empty value returns default",
			annots:     map[string]string{AnnLoadBalancer: ""},
			key:        AnnLoadBalancer,
			defaultVal: "round_robin",
			want:       "round_robin",
		},
		{
			name:       "missing key returns default",
			annots:     map[string]string{},
			key:        AnnLoadBalancer,
			defaultVal: "round_robin",
			want:       "round_robin",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewAnnotationParser(tt.annots)
			if got := p.GetString(tt.key, tt.defaultVal); got != tt.want {
				t.Errorf("GetString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewAnnotationParserNilMap(t *testing.T) {
	p := NewAnnotationParser(nil)

	// All methods should return defaults without panicking.
	if p.Has(AnnTimeout) {
		t.Error("Has() on nil map should return false")
	}
	if v := p.GetString(AnnTimeout, "fallback"); v != "fallback" {
		t.Errorf("GetString() on nil map = %q, want %q", v, "fallback")
	}
	if v := p.GetBool(AnnCORSEnabled, true); !v {
		t.Error("GetBool() on nil map should return default true")
	}
	if v := p.GetInt(AnnRetryMax, 5); v != 5 {
		t.Errorf("GetInt() on nil map = %d, want 5", v)
	}
	if v := p.GetDuration(AnnTimeout, 10*time.Second); v != 10*time.Second {
		t.Errorf("GetDuration() on nil map = %v, want 10s", v)
	}
}

func TestAnnotationParserGetBoolEmptyString(t *testing.T) {
	p := NewAnnotationParser(map[string]string{
		AnnCORSEnabled: "",
	})

	tests := []struct {
		name       string
		defaultVal bool
		want       bool
	}{
		{
			name:       "default false",
			defaultVal: false,
			want:       false,
		},
		{
			name:       "default true",
			defaultVal: true,
			want:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.GetBool(AnnCORSEnabled, tt.defaultVal); got != tt.want {
				t.Errorf("GetBool() with empty string = %v, want %v", got, tt.want)
			}
		})
	}
}
