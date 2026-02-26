package runway

import (
	"fmt"
	"net/http"
	"slices"
)

// Middleware is a function that wraps an http.Handler.
type Middleware = func(http.Handler) http.Handler

// MiddlewareSlot defines a custom per-route middleware and its position
// relative to named built-in middleware.
//
// Positioning rules:
//   - If only After is set: insert immediately after that anchor
//   - If only Before is set: insert immediately before that anchor
//   - If both set: insert between the two anchors (After must precede Before)
//   - If neither set: append to the end of the chain (just before the proxy)
//   - Multiple custom middleware with the same anchor are ordered by registration order
//   - Invalid anchor names fail at Build() time with a clear error
type MiddlewareSlot struct {
	Name   string                                            // unique name for this middleware
	After  string                                            // insert after this named middleware
	Before string                                            // insert before this named middleware
	Build  func(routeID string, cfg RouteConfig) Middleware  // return nil to skip for this route
}

// GlobalMiddlewareSlot defines a custom global middleware and its position
// in the global handler chain.
type GlobalMiddlewareSlot struct {
	Name   string                     // unique name for this middleware
	After  string                     // insert after this named middleware
	Before string                     // insert before this named middleware
	Build  func(cfg *Config) Middleware // return nil to skip
}

// NamedSlot is a named middleware builder used internally to compose
// the middleware chain with anchor-based insertion.
type NamedSlot struct {
	Name  string
	Build func() Middleware
}

// ResolveCustomSlots inserts custom middleware slots into the named slot list
// based on their After/Before anchors. Returns an error if an anchor name
// is not found or if After comes after Before in the chain.
func ResolveCustomSlots(slots []NamedSlot, custom []MiddlewareSlot, routeID string, cfg RouteConfig) ([]NamedSlot, error) {
	for _, cs := range custom {
		idx, err := resolveAnchor(slots, cs.After, cs.Before, cs.Name)
		if err != nil {
			return nil, err
		}
		capturedSlot := cs // capture for closure
		slots = slices.Insert(slots, idx, NamedSlot{
			Name: cs.Name,
			Build: func() Middleware {
				return capturedSlot.Build(routeID, cfg)
			},
		})
	}
	return slots, nil
}

// ResolveCustomGlobalSlots inserts custom global middleware slots into the
// named slot list based on their After/Before anchors.
func ResolveCustomGlobalSlots(slots []NamedSlot, custom []GlobalMiddlewareSlot, cfg *Config) ([]NamedSlot, error) {
	for _, cs := range custom {
		idx, err := resolveAnchor(slots, cs.After, cs.Before, cs.Name)
		if err != nil {
			return nil, err
		}
		capturedSlot := cs
		slots = slices.Insert(slots, idx, NamedSlot{
			Name: cs.Name,
			Build: func() Middleware {
				return capturedSlot.Build(cfg)
			},
		})
	}
	return slots, nil
}

func resolveAnchor(slots []NamedSlot, after, before, name string) (int, error) {
	findIndex := func(anchor string) (int, error) {
		for i, s := range slots {
			if s.Name == anchor {
				return i, nil
			}
		}
		return -1, fmt.Errorf("middleware %q: anchor %q not found in chain", name, anchor)
	}

	if after != "" && before != "" {
		afterIdx, err := findIndex(after)
		if err != nil {
			return 0, err
		}
		beforeIdx, err := findIndex(before)
		if err != nil {
			return 0, err
		}
		if afterIdx >= beforeIdx {
			return 0, fmt.Errorf("middleware %q: anchor %q (pos %d) must come before %q (pos %d)",
				name, after, afterIdx, before, beforeIdx)
		}
		return afterIdx + 1, nil
	}

	if after != "" {
		idx, err := findIndex(after)
		if err != nil {
			return 0, err
		}
		return idx + 1, nil
	}

	if before != "" {
		idx, err := findIndex(before)
		if err != nil {
			return 0, err
		}
		return idx, nil
	}

	// Neither set: append to end
	return len(slots), nil
}
