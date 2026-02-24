package rules

import (
	"context"
	"net/http"
)

type cacheBypassKey struct{}

// SetCacheBypass marks the request to bypass cache.
func SetCacheBypass(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), cacheBypassKey{}, true)
	return r.WithContext(ctx)
}

// IsCacheBypass returns true if the request has been marked to bypass cache.
func IsCacheBypass(r *http.Request) bool {
	v, _ := r.Context().Value(cacheBypassKey{}).(bool)
	return v
}
