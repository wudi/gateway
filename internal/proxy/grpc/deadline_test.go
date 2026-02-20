package grpc

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseGRPCTimeout(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
		ok    bool
	}{
		{"5H", 5 * time.Hour, true},
		{"30M", 30 * time.Minute, true},
		{"10S", 10 * time.Second, true},
		{"500m", 500 * time.Millisecond, true},
		{"100u", 100 * time.Microsecond, true},
		{"250n", 250 * time.Nanosecond, true},
		{"0S", 0, true},
		{"", 0, false},
		{"5", 0, false},
		{"S", 0, false},
		{"-1S", 0, false},
		{"abcS", 0, false},
		{"5X", 0, false},
	}

	for _, tt := range tests {
		d, ok := ParseGRPCTimeout(tt.input)
		if ok != tt.ok {
			t.Errorf("ParseGRPCTimeout(%q): ok=%v, want %v", tt.input, ok, tt.ok)
		}
		if d != tt.want {
			t.Errorf("ParseGRPCTimeout(%q): got %v, want %v", tt.input, d, tt.want)
		}
	}
}

func TestFormatGRPCTimeout(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  string
	}{
		{5 * time.Hour, "5H"},
		{30 * time.Minute, "30M"},
		{10 * time.Second, "10S"},
		{500 * time.Millisecond, "500m"},
		{100 * time.Microsecond, "100u"},
		{250 * time.Nanosecond, "250n"},
		{0, "0n"},
		{-1, "0n"},
		// Mixed durations use the best fit
		{90 * time.Second, "90S"},
		{1500 * time.Millisecond, "1500m"},
	}

	for _, tt := range tests {
		got := FormatGRPCTimeout(tt.input)
		if got != tt.want {
			t.Errorf("FormatGRPCTimeout(%v): got %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPropagateDeadline(t *testing.T) {
	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)
	r.Header.Set("grpc-timeout", "5S")

	newR, cancel := PropagateDeadline(r)
	defer cancel()

	deadline, ok := newR.Context().Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	remaining := time.Until(deadline)
	if remaining < 4*time.Second || remaining > 6*time.Second {
		t.Errorf("expected ~5s remaining, got %v", remaining)
	}
}

func TestPropagateDeadlineNoHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)

	newR, cancel := PropagateDeadline(r)
	defer cancel()

	if _, ok := newR.Context().Deadline(); ok {
		t.Error("expected no deadline when no grpc-timeout header")
	}
}

func TestPropagateDeadlineUsesExistingDeadline(t *testing.T) {
	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)
	r.Header.Set("grpc-timeout", "10S")

	// Set a shorter existing deadline
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	newR, cancel2 := PropagateDeadline(r)
	defer cancel2()

	deadline, ok := newR.Context().Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	remaining := time.Until(deadline)
	if remaining > 3*time.Second {
		t.Errorf("expected shorter deadline to be used, got %v remaining", remaining)
	}
}

func TestSetRemainingTimeout(t *testing.T) {
	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	SetRemainingTimeout(r)

	timeout := r.Header.Get("grpc-timeout")
	if timeout == "" {
		t.Fatal("expected grpc-timeout header to be set")
	}
	d, ok := ParseGRPCTimeout(timeout)
	if !ok {
		t.Fatalf("expected valid timeout, got %q", timeout)
	}
	if d < 4*time.Second || d > 6*time.Second {
		t.Errorf("expected ~5s timeout, got %v", d)
	}
}
