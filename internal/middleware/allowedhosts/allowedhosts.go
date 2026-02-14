package allowedhosts

import (
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/wudi/gateway/internal/config"
)

// CompiledAllowedHosts holds pre-compiled host matching state.
type CompiledAllowedHosts struct {
	exactHosts map[string]bool // exact hostname matches (lowercased)
	suffixes   []string        // wildcard suffixes (e.g. ".example.com" from "*.example.com")
	rejected   atomic.Int64
}

// New creates a new compiled allowed hosts checker from config.
func New(cfg config.AllowedHostsConfig) *CompiledAllowedHosts {
	ah := &CompiledAllowedHosts{
		exactHosts: make(map[string]bool),
	}
	for _, h := range cfg.Hosts {
		h = strings.ToLower(h)
		if strings.HasPrefix(h, "*.") {
			// *.example.com matches anything.example.com
			ah.suffixes = append(ah.suffixes, h[1:]) // store ".example.com"
		} else {
			ah.exactHosts[h] = true
		}
	}
	return ah
}

// Check returns true if the request host is allowed.
func (ah *CompiledAllowedHosts) Check(r *http.Request) bool {
	host := strings.ToLower(r.Host)
	// Strip port
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// Check exact match
	if ah.exactHosts[host] {
		return true
	}

	// Check wildcard suffixes
	for _, suffix := range ah.suffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}

	return false
}

// Middleware returns an http middleware that rejects requests to non-allowed hosts.
func (ah *CompiledAllowedHosts) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ah.Check(r) {
			ah.rejected.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMisdirectedRequest) // 421
			w.Write([]byte(`{"error":"misdirected_request","message":"Host not allowed"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Stats returns allowed hosts statistics.
func (ah *CompiledAllowedHosts) Stats() map[string]interface{} {
	hosts := make([]string, 0, len(ah.exactHosts)+len(ah.suffixes))
	for h := range ah.exactHosts {
		hosts = append(hosts, h)
	}
	for _, s := range ah.suffixes {
		hosts = append(hosts, "*"+s)
	}
	return map[string]interface{}{
		"enabled":  true,
		"hosts":    hosts,
		"rejected": ah.rejected.Load(),
	}
}
