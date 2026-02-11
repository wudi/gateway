package altsvc

import (
	"fmt"
	"net/http"
)

// Middleware returns a middleware that sets the Alt-Svc header to advertise HTTP/3
// on the given port. The header is only set on HTTP/1.x and HTTP/2 responses;
// HTTP/3 responses already know they support the protocol.
func Middleware(h3Port string) func(http.Handler) http.Handler {
	altSvcValue := fmt.Sprintf(`h3=":%s"; ma=2592000`, h3Port)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor < 3 {
				w.Header().Set("Alt-Svc", altSvcValue)
			}
			next.ServeHTTP(w, r)
		})
	}
}
