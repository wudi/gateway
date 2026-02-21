package clientmtls

import (
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/middleware"
)

// ClientMTLSVerifier verifies client certificates against a route-specific CA pool.
type ClientMTLSVerifier struct {
	caPool       *x509.CertPool
	clientAuth   string // "request", "require", "verify"
	allowExpired bool
	verified     atomic.Int64
	rejected     atomic.Int64
}

// New creates a ClientMTLSVerifier from config, loading CA files into a cert pool.
func New(cfg config.ClientMTLSConfig) (*ClientMTLSVerifier, error) {
	v := &ClientMTLSVerifier{
		clientAuth:   cfg.ClientAuth,
		allowExpired: cfg.AllowExpired,
	}
	if v.clientAuth == "" {
		v.clientAuth = "verify"
	}

	// Build CA pool for verify mode
	if v.clientAuth == "verify" {
		pool := x509.NewCertPool()
		files := cfg.ClientCAs
		if cfg.ClientCAFile != "" {
			files = append([]string{cfg.ClientCAFile}, files...)
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("client_mtls: verify mode requires client_ca_file or client_cas")
		}
		for _, f := range files {
			pem, err := os.ReadFile(f)
			if err != nil {
				return nil, fmt.Errorf("client_mtls: reading CA file %q: %w", f, err)
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("client_mtls: no valid certificates in %q", f)
			}
		}
		v.caPool = pool
	}

	return v, nil
}

// Middleware returns a middleware that verifies client TLS certificates.
func (v *ClientMTLSVerifier) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
				if v.clientAuth == "request" {
					// request mode: no cert is OK
					next.ServeHTTP(w, r)
					return
				}
				// require or verify: cert is mandatory
				v.rejected.Add(1)
				errors.ErrForbidden.WithDetails("Client certificate required").WriteJSON(w)
				return
			}

			if v.clientAuth == "require" {
				// require mode: cert present is enough, no CA verification
				v.verified.Add(1)
				next.ServeHTTP(w, r)
				return
			}

			// verify mode: validate the cert chain against our CA pool
			cert := r.TLS.PeerCertificates[0]

			intermediates := x509.NewCertPool()
			for _, ic := range r.TLS.PeerCertificates[1:] {
				intermediates.AddCert(ic)
			}

			opts := x509.VerifyOptions{
				Roots:         v.caPool,
				Intermediates: intermediates,
				KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}
			if v.allowExpired {
				opts.CurrentTime = cert.NotAfter.Add(-time.Second)
			}

			if _, err := cert.Verify(opts); err != nil {
				v.rejected.Add(1)
				errors.ErrForbidden.WithDetails("Client certificate verification failed").WriteJSON(w)
				return
			}

			v.verified.Add(1)
			next.ServeHTTP(w, r)
		})
	}
}

// Verified returns the number of successfully verified requests.
func (v *ClientMTLSVerifier) Verified() int64 {
	return v.verified.Load()
}

// Rejected returns the number of rejected requests.
func (v *ClientMTLSVerifier) Rejected() int64 {
	return v.rejected.Load()
}

// MergeClientMTLSConfig merges per-route config with global, preferring per-route when set.
func MergeClientMTLSConfig(route, global config.ClientMTLSConfig) config.ClientMTLSConfig {
	merged := config.MergeNonZero(global, route)
	merged.Enabled = true
	return merged
}

// ClientMTLSByRoute manages per-route client mTLS verifiers.
type ClientMTLSByRoute struct {
	byroute.Manager[*ClientMTLSVerifier]
}

// NewClientMTLSByRoute creates a new per-route client mTLS manager.
func NewClientMTLSByRoute() *ClientMTLSByRoute {
	return &ClientMTLSByRoute{}
}

// AddRoute adds a client mTLS verifier for a route.
func (m *ClientMTLSByRoute) AddRoute(routeID string, cfg config.ClientMTLSConfig) error {
	v, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, v)
	return nil
}

// GetVerifier returns the client mTLS verifier for a route.
func (m *ClientMTLSByRoute) GetVerifier(routeID string) *ClientMTLSVerifier {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route verification metrics.
func (m *ClientMTLSByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(v *ClientMTLSVerifier) interface{} {
		return map[string]interface{}{
			"verified": v.Verified(),
			"rejected": v.Rejected(),
		}
	})
}
