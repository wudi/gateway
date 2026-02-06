package rules

import (
	"net/http"
	"time"

	"github.com/example/gateway/internal/variables"
)

// RequestEnv is the expression environment for request-phase rules.
// Field names use Cloudflare-style dot notation via expr struct tags.
type RequestEnv struct {
	HTTP  HTTPEnv  `expr:"http"`
	IP    IPEnv    `expr:"ip"`
	Route RouteEnv `expr:"route"`
	Auth  AuthEnv  `expr:"auth"`
}

// HTTPEnv groups HTTP-related fields.
type HTTPEnv struct {
	Request  HTTPRequestEnv  `expr:"request"`
	Response HTTPResponseEnv `expr:"response"`
}

// HTTPRequestEnv provides request fields.
type HTTPRequestEnv struct {
	Method   string            `expr:"method"`
	URI      URIEnv            `expr:"uri"`
	Headers  map[string]string `expr:"headers"`
	Cookies  map[string]string `expr:"cookies"`
	Host     string            `expr:"host"`
	Scheme   string            `expr:"scheme"`
	BodySize int64             `expr:"body_size"`
}

// URIEnv provides URI components.
type URIEnv struct {
	Path  string            `expr:"path"`
	Query string            `expr:"query"`
	Full  string            `expr:"full"`
	Args  map[string]string `expr:"args"`
}

// IPEnv provides IP-related fields.
type IPEnv struct {
	Src string `expr:"src"`
}

// RouteEnv provides route context.
type RouteEnv struct {
	ID     string            `expr:"id"`
	Params map[string]string `expr:"params"`
}

// AuthEnv provides authentication context.
type AuthEnv struct {
	ClientID string         `expr:"client_id"`
	Type     string         `expr:"type"`
	Claims   map[string]any `expr:"claims"`
}

// HTTPResponseEnv provides response fields (only populated in response phase).
type HTTPResponseEnv struct {
	Code         int               `expr:"code"`
	Headers      map[string]string `expr:"headers"`
	ResponseTime float64           `expr:"response_time"`
}

// ResponseEnv is the expression environment for response-phase rules.
// It embeds RequestEnv so all request fields remain available.
type ResponseEnv = RequestEnv

// NewRequestEnv builds a RequestEnv from the current request and variable context.
func NewRequestEnv(r *http.Request, varCtx *variables.Context) RequestEnv {
	// Build headers map (first value, canonical keys)
	headers := make(map[string]string, len(r.Header))
	for k := range r.Header {
		headers[k] = r.Header.Get(k)
	}

	// Build query args map (first value)
	args := make(map[string]string)
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			args[k] = v[0]
		}
	}

	// Build cookies map
	cookies := make(map[string]string, len(r.Cookies()))
	for _, c := range r.Cookies() {
		cookies[c.Name] = c.Value
	}

	// Determine scheme
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	// Auth fields
	var clientID, authType string
	var claims map[string]any
	if varCtx != nil && varCtx.Identity != nil {
		clientID = varCtx.Identity.ClientID
		authType = varCtx.Identity.AuthType
		claims = varCtx.Identity.Claims
	}
	if claims == nil {
		claims = make(map[string]any)
	}

	// Route fields
	var routeID string
	var pathParams map[string]string
	if varCtx != nil {
		routeID = varCtx.RouteID
		pathParams = varCtx.PathParams
	}
	if pathParams == nil {
		pathParams = make(map[string]string)
	}

	return RequestEnv{
		HTTP: HTTPEnv{
			Request: HTTPRequestEnv{
				Method: r.Method,
				URI: URIEnv{
					Path:  r.URL.Path,
					Query: r.URL.RawQuery,
					Full:  r.RequestURI,
					Args:  args,
				},
				Headers:  headers,
				Cookies:  cookies,
				Host:     r.Host,
				Scheme:   scheme,
				BodySize: r.ContentLength,
			},
		},
		IP: IPEnv{
			Src: variables.ExtractClientIP(r),
		},
		Route: RouteEnv{
			ID:     routeID,
			Params: pathParams,
		},
		Auth: AuthEnv{
			ClientID: clientID,
			Type:     authType,
			Claims:   claims,
		},
	}
}

// NewResponseEnv builds a ResponseEnv that includes both request and response data.
func NewResponseEnv(r *http.Request, varCtx *variables.Context, statusCode int, respHeaders http.Header) ResponseEnv {
	env := NewRequestEnv(r, varCtx)

	// Build response headers map
	rh := make(map[string]string, len(respHeaders))
	for k := range respHeaders {
		rh[k] = respHeaders.Get(k)
	}

	var responseTimeMs float64
	if varCtx != nil && !varCtx.StartTime.IsZero() {
		responseTimeMs = float64(time.Since(varCtx.StartTime).Milliseconds())
	}

	env.HTTP.Response = HTTPResponseEnv{
		Code:         statusCode,
		Headers:      rh,
		ResponseTime: responseTimeMs,
	}

	return env
}
