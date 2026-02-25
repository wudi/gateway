package extract

import (
	"net/http"
	"strings"

	"github.com/wudi/gateway/variables"
)

// Func extracts a value from a request.
type Func func(r *http.Request) string

// Build returns an extractor for the given source specification.
// Supported prefixes: header:, jwt_claim:, query:, cookie:, static:.
func Build(source string) Func {
	switch {
	case strings.HasPrefix(source, "header:"):
		hdr := source[len("header:"):]
		return func(r *http.Request) string {
			return r.Header.Get(hdr)
		}
	case strings.HasPrefix(source, "jwt_claim:"):
		claim := source[len("jwt_claim:"):]
		return func(r *http.Request) string {
			vc := variables.GetFromRequest(r)
			if vc.Identity == nil || vc.Identity.Claims == nil {
				return ""
			}
			if v, ok := vc.Identity.Claims[claim]; ok {
				if s, ok := v.(string); ok {
					return s
				}
			}
			return ""
		}
	case strings.HasPrefix(source, "query:"):
		param := source[len("query:"):]
		return func(r *http.Request) string {
			return r.URL.Query().Get(param)
		}
	case strings.HasPrefix(source, "cookie:"):
		name := source[len("cookie:"):]
		return func(r *http.Request) string {
			if c, err := r.Cookie(name); err == nil {
				return c.Value
			}
			return ""
		}
	case strings.HasPrefix(source, "static:"):
		val := source[len("static:"):]
		return func(r *http.Request) string {
			return val
		}
	default:
		return func(r *http.Request) string { return "" }
	}
}
