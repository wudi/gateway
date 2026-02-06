package loadbalancer

import (
	"hash/fnv"
	"net"
	"net/http"
	"time"

	"github.com/example/gateway/internal/config"
)

// StickyPolicy determines consistent traffic group assignment.
type StickyPolicy struct {
	mode       string // "cookie", "header", "hash"
	cookieName string
	hashKey    string
	ttl        time.Duration
}

// NewStickyPolicy creates a new StickyPolicy from config.
func NewStickyPolicy(cfg config.StickyConfig) *StickyPolicy {
	cookieName := cfg.CookieName
	if cookieName == "" {
		cookieName = "X-Traffic-Group"
	}
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	return &StickyPolicy{
		mode:       cfg.Mode,
		cookieName: cookieName,
		hashKey:    cfg.HashKey,
		ttl:        ttl,
	}
}

// ResolveGroup determines the traffic group for a request.
// Returns the group name if one can be determined, or empty string if not.
func (sp *StickyPolicy) ResolveGroup(r *http.Request, groups []*TrafficGroup) string {
	switch sp.mode {
	case "cookie":
		cookie, err := r.Cookie(sp.cookieName)
		if err != nil || cookie.Value == "" {
			return ""
		}
		// Validate that the cookie value matches an actual group
		for _, g := range groups {
			if g.Name == cookie.Value {
				return cookie.Value
			}
		}
		return ""

	case "header":
		val := r.Header.Get(sp.hashKey)
		if val == "" {
			return ""
		}
		return sp.hashToGroup(val, groups)

	case "hash":
		val := r.Header.Get(sp.hashKey)
		if val == "" {
			// Fallback to client IP for hash mode
			val = clientIP(r)
		}
		if val == "" {
			return ""
		}
		return sp.hashToGroup(val, groups)
	}
	return ""
}

// SetCookie returns a cookie for the given group name (cookie mode only).
// Returns nil for non-cookie modes.
func (sp *StickyPolicy) SetCookie(groupName string) *http.Cookie {
	if sp.mode != "cookie" {
		return nil
	}
	return &http.Cookie{
		Name:     sp.cookieName,
		Value:    groupName,
		Path:     "/",
		MaxAge:   int(sp.ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// hashToGroup uses FNV-32a to deterministically map an input to a traffic group.
func (sp *StickyPolicy) hashToGroup(input string, groups []*TrafficGroup) string {
	totalWeight := 0
	for _, g := range groups {
		totalWeight += g.Weight
	}
	if totalWeight <= 0 {
		return ""
	}

	h := fnv.New32a()
	h.Write([]byte(input))
	slot := int(h.Sum32()) % totalWeight
	if slot < 0 {
		slot = -slot
	}

	cumulative := 0
	for _, g := range groups {
		cumulative += g.Weight
		if slot < cumulative {
			return g.Name
		}
	}
	return groups[len(groups)-1].Name
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
