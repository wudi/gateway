package mtls

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/variables"
)

// Middleware returns a middleware that extracts client certificate info
// from TLS peer certificates and stores it on the variables.Context.
func Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
				cert := r.TLS.PeerCertificates[0]
				fp := sha256.Sum256(cert.Raw)
				info := &variables.CertInfo{
					Subject:      cert.Subject.String(),
					Issuer:       cert.Issuer.String(),
					SerialNumber: cert.SerialNumber.String(),
					Fingerprint:  hex.EncodeToString(fp[:]),
					DNSNames:     cert.DNSNames,
				}
				varCtx := variables.GetFromRequest(r)
				varCtx.CertInfo = info
			}
			next.ServeHTTP(w, r)
		})
	}
}
