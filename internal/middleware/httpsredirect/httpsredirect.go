package httpsredirect

import (
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/wudi/gateway/config"
)

// CompiledHTTPSRedirect holds compiled HTTPS redirect state.
type CompiledHTTPSRedirect struct {
	port      int
	permanent bool
	redirects atomic.Int64
}

// New creates a new compiled HTTPS redirect from config.
func New(cfg config.HTTPSRedirectConfig) *CompiledHTTPSRedirect {
	port := cfg.Port
	if port == 0 {
		port = 443
	}
	return &CompiledHTTPSRedirect{
		port:      port,
		permanent: cfg.Permanent,
	}
}

// Middleware returns an http middleware that redirects HTTP to HTTPS.
func (h *CompiledHTTPSRedirect) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			h.redirects.Add(1)

			host := r.Host
			// Strip existing port from host if present
			for i := len(host) - 1; i >= 0; i-- {
				if host[i] == ':' {
					host = host[:i]
					break
				}
				if host[i] < '0' || host[i] > '9' {
					break
				}
			}

			var target string
			pathAndQuery := r.URL.RequestURI()
			if h.port == 443 {
				target = fmt.Sprintf("https://%s%s", host, pathAndQuery)
			} else {
				target = fmt.Sprintf("https://%s:%d%s", host, h.port, pathAndQuery)
			}

			code := http.StatusFound // 302
			if h.permanent {
				code = http.StatusMovedPermanently // 301
			}

			http.Redirect(w, r, target, code)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Stats returns redirect statistics.
func (h *CompiledHTTPSRedirect) Stats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":   true,
		"port":      h.port,
		"permanent": h.permanent,
		"redirects": h.redirects.Load(),
	}
}
