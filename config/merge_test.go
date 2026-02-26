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

	t.Run("nil base map with overlay creates map", func(t *testing.T) {
		type S struct {
			M map[string]string
		}
		got := MergeNonZero(S{}, S{M: map[string]string{"x": "1"}})
		if got.M == nil {
			t.Fatal("expected non-nil map")
		}
		if got.M["x"] != "1" {
			t.Errorf("M[x] = %q, want 1", got.M["x"])
		}
	})

	t.Run("empty map overlay preserves base", func(t *testing.T) {
		type S struct {
			M map[string]string
		}
		got := MergeNonZero(
			S{M: map[string]string{"a": "1"}},
			S{M: map[string]string{}},
		)
		if got.M["a"] != "1" {
			t.Errorf("M[a] = %q, want 1 (empty overlay should not clear)", got.M["a"])
		}
	})

	t.Run("float fields override when non-zero", func(t *testing.T) {
		type S struct {
			Rate    float64
			Percent float64
		}
		got := MergeNonZero(S{Rate: 1.5, Percent: 0.5}, S{Percent: 0.9})
		if got.Rate != 1.5 {
			t.Errorf("Rate = %f, want 1.5", got.Rate)
		}
		if got.Percent != 0.9 {
			t.Errorf("Percent = %f, want 0.9", got.Percent)
		}
	})

	t.Run("float zero preserves base", func(t *testing.T) {
		type S struct {
			X float64
		}
		got := MergeNonZero(S{X: 3.14}, S{X: 0})
		if got.X != 3.14 {
			t.Errorf("X = %f, want 3.14", got.X)
		}
	})

	t.Run("deeply nested structs", func(t *testing.T) {
		type L3 struct {
			Val string
		}
		type L2 struct {
			Nested L3
			Count  int
		}
		type L1 struct {
			Inner L2
		}
		got := MergeNonZero(
			L1{Inner: L2{Nested: L3{Val: "base"}, Count: 5}},
			L1{Inner: L2{Nested: L3{Val: "overlay"}}},
		)
		if got.Inner.Nested.Val != "overlay" {
			t.Errorf("L3.Val = %q, want overlay", got.Inner.Nested.Val)
		}
		if got.Inner.Count != 5 {
			t.Errorf("Count = %d, want 5 (preserved from base)", got.Inner.Count)
		}
	})

	t.Run("map with int keys", func(t *testing.T) {
		type S struct {
			M map[int]string
		}
		got := MergeNonZero(
			S{M: map[int]string{1: "a", 2: "b"}},
			S{M: map[int]string{2: "c", 3: "d"}},
		)
		if got.M[1] != "a" {
			t.Errorf("M[1] = %q, want a", got.M[1])
		}
		if got.M[2] != "c" {
			t.Errorf("M[2] = %q, want c (overlay wins)", got.M[2])
		}
		if got.M[3] != "d" {
			t.Errorf("M[3] = %q, want d", got.M[3])
		}
	})

	t.Run("pointer nil base with non-nil overlay", func(t *testing.T) {
		type S struct {
			P *int
		}
		v := 42
		got := MergeNonZero(S{}, S{P: &v})
		if got.P == nil || *got.P != 42 {
			t.Errorf("P should be 42, got %v", got.P)
		}
	})

	t.Run("both base and overlay zero struct", func(t *testing.T) {
		type S struct {
			A string
			B int
			C bool
		}
		got := MergeNonZero(S{}, S{})
		if got.A != "" || got.B != 0 || got.C != false {
			t.Errorf("expected zero struct, got %+v", got)
		}
	})

	t.Run("slice nil overlay preserves base", func(t *testing.T) {
		type S struct {
			Items []int
		}
		got := MergeNonZero(S{Items: []int{1, 2, 3}}, S{})
		if len(got.Items) != 3 || got.Items[0] != 1 {
			t.Errorf("Items = %v, want [1 2 3]", got.Items)
		}
	})

	t.Run("empty slice overlay preserves base", func(t *testing.T) {
		type S struct {
			Items []int
		}
		got := MergeNonZero(S{Items: []int{1, 2}}, S{Items: []int{}})
		if len(got.Items) != 2 {
			t.Errorf("Items = %v, want [1 2] (empty slice should not override)", got.Items)
		}
	})

	t.Run("bool false overlay overrides true base", func(t *testing.T) {
		type S struct {
			Enabled bool
		}
		got := MergeNonZero(S{Enabled: true}, S{Enabled: false})
		if got.Enabled {
			t.Error("Enabled should be false (bool always overrides from overlay)")
		}
	})

	t.Run("bool true overlay overrides false base", func(t *testing.T) {
		type S struct {
			Enabled bool
		}
		got := MergeNonZero(S{Enabled: false}, S{Enabled: true})
		if !got.Enabled {
			t.Error("Enabled should be true (bool always overrides from overlay)")
		}
	})

	t.Run("mixed field types in single struct", func(t *testing.T) {
		type S struct {
			Name    string
			Count   int
			Rate    float64
			Active  bool
			Items   []string
			Meta    map[string]string
			Timeout time.Duration
			Ref     *bool
		}
		bTrue := true
		base := S{
			Name:    "base",
			Count:   10,
			Rate:    1.5,
			Active:  true,
			Items:   []string{"a"},
			Meta:    map[string]string{"k1": "v1"},
			Timeout: 5 * time.Second,
			Ref:     &bTrue,
		}
		bFalse := false
		overlay := S{
			Name:   "overlay",
			Rate:   2.5,
			Active: false,
			Items:  []string{"b", "c"},
			Meta:   map[string]string{"k2": "v2"},
			Ref:    &bFalse,
		}
		got := MergeNonZero(base, overlay)
		if got.Name != "overlay" {
			t.Errorf("Name = %q, want overlay", got.Name)
		}
		if got.Count != 10 {
			t.Errorf("Count = %d, want 10", got.Count)
		}
		if got.Rate != 2.5 {
			t.Errorf("Rate = %f, want 2.5", got.Rate)
		}
		if got.Active != false {
			t.Error("Active should be false")
		}
		if len(got.Items) != 2 || got.Items[0] != "b" {
			t.Errorf("Items = %v, want [b c]", got.Items)
		}
		if got.Meta["k1"] != "v1" || got.Meta["k2"] != "v2" {
			t.Errorf("Meta = %v, want k1->v1 and k2->v2", got.Meta)
		}
		if got.Timeout != 5*time.Second {
			t.Errorf("Timeout = %v, want 5s", got.Timeout)
		}
		if *got.Ref != false {
			t.Error("Ref should be false")
		}
	})

	t.Run("duration non-zero overlay overrides", func(t *testing.T) {
		type S struct {
			D time.Duration
		}
		got := MergeNonZero(S{D: 0}, S{D: 10 * time.Second})
		if got.D != 10*time.Second {
			t.Errorf("D = %v, want 10s", got.D)
		}
	})

	t.Run("top-level map merge", func(t *testing.T) {
		base := map[string]string{"a": "1", "b": "2"}
		overlay := map[string]string{"b": "3", "c": "4"}
		got := MergeNonZero(base, overlay)
		if got["a"] != "1" {
			t.Errorf("got[a] = %q, want 1", got["a"])
		}
		if got["b"] != "3" {
			t.Errorf("got[b] = %q, want 3", got["b"])
		}
		if got["c"] != "4" {
			t.Errorf("got[c] = %q, want 4", got["c"])
		}
	})

	t.Run("top-level map nil overlay", func(t *testing.T) {
		var overlay map[string]string
		base := map[string]string{"a": "1"}
		got := MergeNonZero(base, overlay)
		if got["a"] != "1" {
			t.Errorf("got[a] = %q, want 1", got["a"])
		}
	})
}
