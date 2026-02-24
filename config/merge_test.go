package config

import (
	"testing"
	"time"
)

func TestMergeNonZero(t *testing.T) {
	t.Run("strings override when non-empty", func(t *testing.T) {
		type S struct {
			A string
			B string
		}
		base := S{A: "base_a", B: "base_b"}
		overlay := S{A: "overlay_a"}
		got := MergeNonZero(base, overlay)
		if got.A != "overlay_a" {
			t.Errorf("A = %q, want %q", got.A, "overlay_a")
		}
		if got.B != "base_b" {
			t.Errorf("B = %q, want %q", got.B, "base_b")
		}
	})

	t.Run("ints override when non-zero", func(t *testing.T) {
		type S struct {
			X int
			Y int
		}
		got := MergeNonZero(S{X: 10, Y: 20}, S{X: 0, Y: 30})
		if got.X != 10 {
			t.Errorf("X = %d, want 10", got.X)
		}
		if got.Y != 30 {
			t.Errorf("Y = %d, want 30", got.Y)
		}
	})

	t.Run("bools always override", func(t *testing.T) {
		type S struct {
			Enabled bool
			Flag    bool
		}
		got := MergeNonZero(S{Enabled: true, Flag: true}, S{Enabled: false, Flag: false})
		if got.Enabled != false {
			t.Error("Enabled should be false (overlay overrides)")
		}
		if got.Flag != false {
			t.Error("Flag should be false (overlay overrides)")
		}
	})

	t.Run("slices override when non-empty", func(t *testing.T) {
		type S struct {
			Items  []string
			Others []string
		}
		got := MergeNonZero(
			S{Items: []string{"a"}, Others: []string{"x"}},
			S{Items: []string{"b", "c"}},
		)
		if len(got.Items) != 2 || got.Items[0] != "b" {
			t.Errorf("Items = %v, want [b c]", got.Items)
		}
		if len(got.Others) != 1 || got.Others[0] != "x" {
			t.Errorf("Others = %v, want [x]", got.Others)
		}
	})

	t.Run("maps are merged", func(t *testing.T) {
		type S struct {
			M map[string]string
		}
		got := MergeNonZero(
			S{M: map[string]string{"a": "1", "b": "2"}},
			S{M: map[string]string{"b": "3", "c": "4"}},
		)
		if got.M["a"] != "1" {
			t.Errorf("M[a] = %q, want 1", got.M["a"])
		}
		if got.M["b"] != "3" {
			t.Errorf("M[b] = %q, want 3 (overlay wins)", got.M["b"])
		}
		if got.M["c"] != "4" {
			t.Errorf("M[c] = %q, want 4", got.M["c"])
		}
	})

	t.Run("nil map overlay does not clear base", func(t *testing.T) {
		type S struct {
			M map[string]string
		}
		got := MergeNonZero(
			S{M: map[string]string{"a": "1"}},
			S{},
		)
		if got.M["a"] != "1" {
			t.Errorf("M[a] = %q, want 1", got.M["a"])
		}
	})

	t.Run("durations override when non-zero", func(t *testing.T) {
		type S struct {
			Timeout time.Duration
			Idle    time.Duration
		}
		got := MergeNonZero(
			S{Timeout: 5 * time.Second, Idle: 10 * time.Second},
			S{Timeout: 0, Idle: 30 * time.Second},
		)
		if got.Timeout != 5*time.Second {
			t.Errorf("Timeout = %v, want 5s", got.Timeout)
		}
		if got.Idle != 30*time.Second {
			t.Errorf("Idle = %v, want 30s", got.Idle)
		}
	})

	t.Run("nested structs are recursed", func(t *testing.T) {
		type Inner struct {
			X int
			Y int
		}
		type S struct {
			Inner Inner
		}
		got := MergeNonZero(
			S{Inner: Inner{X: 1, Y: 2}},
			S{Inner: Inner{Y: 3}},
		)
		if got.Inner.X != 1 {
			t.Errorf("Inner.X = %d, want 1", got.Inner.X)
		}
		if got.Inner.Y != 3 {
			t.Errorf("Inner.Y = %d, want 3", got.Inner.Y)
		}
	})

	t.Run("pointer fields override when non-nil", func(t *testing.T) {
		type S struct {
			P *bool
			Q *bool
		}
		bTrue := true
		bFalse := false
		got := MergeNonZero(
			S{P: &bTrue, Q: &bTrue},
			S{P: &bFalse, Q: nil},
		)
		if *got.P != false {
			t.Error("P should be false (overlay overrides)")
		}
		if *got.Q != true {
			t.Error("Q should be true (overlay nil, keeps base)")
		}
	})

	t.Run("real config type SecurityHeadersConfig", func(t *testing.T) {
		base := SecurityHeadersConfig{
			Enabled:                 true,
			StrictTransportSecurity: "max-age=31536000",
			XContentTypeOptions:     "nosniff",
			CustomHeaders:           map[string]string{"X-A": "1", "X-B": "2"},
		}
		overlay := SecurityHeadersConfig{
			Enabled:             true,
			XContentTypeOptions: "nosniff-override",
			CustomHeaders:       map[string]string{"X-B": "3", "X-C": "4"},
		}
		got := MergeNonZero(base, overlay)
		if got.StrictTransportSecurity != "max-age=31536000" {
			t.Errorf("HSTS = %q, want base value", got.StrictTransportSecurity)
		}
		if got.XContentTypeOptions != "nosniff-override" {
			t.Errorf("XCTO = %q, want overlay", got.XContentTypeOptions)
		}
		if got.CustomHeaders["X-A"] != "1" {
			t.Error("X-A should be preserved from base")
		}
		if got.CustomHeaders["X-B"] != "3" {
			t.Error("X-B should be overlay value")
		}
		if got.CustomHeaders["X-C"] != "4" {
			t.Error("X-C should come from overlay")
		}
	})

	t.Run("base map not mutated", func(t *testing.T) {
		type S struct {
			M map[string]string
		}
		baseMap := map[string]string{"a": "1"}
		base := S{M: baseMap}
		overlay := S{M: map[string]string{"b": "2"}}
		got := MergeNonZero(base, overlay)
		if _, ok := baseMap["b"]; ok {
			t.Error("base map should not be mutated")
		}
		if got.M["a"] != "1" || got.M["b"] != "2" {
			t.Errorf("merged map incorrect: %v", got.M)
		}
	})
}
