package loadbalancer

import (
	"encoding/base64"
	"net/http"
	"time"

	"github.com/wudi/runway/config"
)

// SessionAffinityBalancer wraps any Balancer with cookie-based backend pinning.
// On first request, the selected backend's URL is encoded into a cookie.
// On subsequent requests, the cookie routes to that same backend if healthy.
type SessionAffinityBalancer struct {
	inner      Balancer
	cookieName string
	ttl        time.Duration
	path       string
	secure     bool
	sameSite   http.SameSite
}

// NewSessionAffinityBalancer wraps a balancer with session affinity.
func NewSessionAffinityBalancer(inner Balancer, cfg config.SessionAffinityConfig) *SessionAffinityBalancer {
	cookieName := cfg.CookieName
	if cookieName == "" {
		cookieName = "X-Session-Backend"
	}
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = time.Hour
	}
	path := cfg.Path
	if path == "" {
		path = "/"
	}
	sameSite := http.SameSiteLaxMode
	switch cfg.SameSite {
	case "strict":
		sameSite = http.SameSiteStrictMode
	case "none":
		sameSite = http.SameSiteNoneMode
	}
	return &SessionAffinityBalancer{
		inner:      inner,
		cookieName: cookieName,
		ttl:        ttl,
		path:       path,
		secure:     cfg.Secure,
		sameSite:   sameSite,
	}
}

// Next delegates to the inner balancer.
func (s *SessionAffinityBalancer) Next() *Backend {
	return s.inner.Next()
}

// UpdateBackends delegates to the inner balancer.
func (s *SessionAffinityBalancer) UpdateBackends(backends []*Backend) {
	s.inner.UpdateBackends(backends)
}

// MarkHealthy delegates to the inner balancer.
func (s *SessionAffinityBalancer) MarkHealthy(url string) {
	s.inner.MarkHealthy(url)
}

// MarkUnhealthy delegates to the inner balancer.
func (s *SessionAffinityBalancer) MarkUnhealthy(url string) {
	s.inner.MarkUnhealthy(url)
}

// GetBackends delegates to the inner balancer.
func (s *SessionAffinityBalancer) GetBackends() []*Backend {
	return s.inner.GetBackends()
}

// HealthyCount delegates to the inner balancer.
func (s *SessionAffinityBalancer) HealthyCount() int {
	return s.inner.HealthyCount()
}

// NextForHTTPRequest implements RequestAwareBalancer. It reads the affinity cookie,
// finds the matching healthy backend, and returns it. If the cookie is absent,
// invalid, or points to an unhealthy backend, it falls through to the inner balancer.
func (s *SessionAffinityBalancer) NextForHTTPRequest(r *http.Request) (*Backend, string) {
	cookie, err := r.Cookie(s.cookieName)
	if err == nil && cookie.Value != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
		if err == nil {
			backendURL := string(decoded)
			for _, b := range s.inner.GetBackends() {
				if b.URL == backendURL && b.Healthy {
					return b, ""
				}
			}
		}
	}
	// Fall through to inner balancer
	if rab, ok := s.inner.(RequestAwareBalancer); ok {
		return rab.NextForHTTPRequest(r)
	}
	return s.inner.Next(), ""
}

// MakeCookie creates an affinity cookie for the given backend URL.
func (s *SessionAffinityBalancer) MakeCookie(backendURL string) *http.Cookie {
	return &http.Cookie{
		Name:     s.cookieName,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(backendURL)),
		Path:     s.path,
		MaxAge:   int(s.ttl.Seconds()),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: s.sameSite,
	}
}

// CookieName returns the cookie name used for affinity.
func (s *SessionAffinityBalancer) CookieName() string {
	return s.cookieName
}

// TTL returns the cookie TTL.
func (s *SessionAffinityBalancer) TTL() time.Duration {
	return s.ttl
}
