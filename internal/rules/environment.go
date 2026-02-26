package rules

import (
	"net/http"
	"sync"
	"time"

	"github.com/wudi/runway/internal/middleware/geo"
	"github.com/wudi/runway/variables"
)

var requestEnvPool = sync.Pool{
	New: func() any {
		return &RequestEnv{
			HTTP: HTTPEnv{
				Request: HTTPRequestEnv{
					URI: URIEnv{
						Args: make(map[string]string, 8),
					},
					Headers: make(map[string]string, 16),
					Cookies: make(map[string]string, 4),
				},
				Response: HTTPResponseEnv{
					Headers: make(map[string]string, 8),
				},
			},
			Route: RouteEnv{
				Params: make(map[string]string, 4),
			},
			Auth: AuthEnv{
				Claims: make(map[string]any, 4),
			},
		}
	},
}

// AcquireRequestEnv returns a pooled RequestEnv populated from the request.
func AcquireRequestEnv(r *http.Request, varCtx *variables.Context) *RequestEnv {
	env := requestEnvPool.Get().(*RequestEnv)
	populateRequestEnv(env, r, varCtx)
	return env
}

// AcquireResponseEnv returns a pooled RequestEnv populated with both request and response data.
func AcquireResponseEnv(r *http.Request, varCtx *variables.Context, statusCode int, respHeaders http.Header) *RequestEnv {
	env := requestEnvPool.Get().(*RequestEnv)
	populateRequestEnv(env, r, varCtx)

	// Populate response fields
	rh := env.HTTP.Response.Headers
	for k := range respHeaders {
		rh[k] = respHeaders.Get(k)
	}

	var responseTimeMs float64
	if varCtx != nil && !varCtx.StartTime.IsZero() {
		responseTimeMs = float64(time.Since(varCtx.StartTime).Milliseconds())
	}

	env.HTTP.Response.Code = statusCode
	env.HTTP.Response.ResponseTime = responseTimeMs

	return env
}

// ReleaseRequestEnv returns env to the pool. Caller must not use env after this call.
func ReleaseRequestEnv(env *RequestEnv) {
	if env == nil {
		return
	}
	// Clear maps (keeps backing arrays)
	clear(env.HTTP.Request.Headers)
	clear(env.HTTP.Request.URI.Args)
	clear(env.HTTP.Request.Cookies)
	clear(env.HTTP.Response.Headers)
	clear(env.Route.Params)
	clear(env.Auth.Claims)
	requestEnvPool.Put(env)
}

// populateRequestEnv fills env from the request. Maps are reused from the pool.
func populateRequestEnv(env *RequestEnv, r *http.Request, varCtx *variables.Context) {
	// Reuse existing maps
	headers := env.HTTP.Request.Headers
	for k := range r.Header {
		headers[k] = r.Header.Get(k)
	}

	args := env.HTTP.Request.URI.Args
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			args[k] = v[0]
		}
	}

	cookies := env.HTTP.Request.Cookies
	for _, c := range r.Cookies() {
		cookies[c.Name] = c.Value
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	env.HTTP.Request.Method = r.Method
	env.HTTP.Request.URI.Path = r.URL.Path
	env.HTTP.Request.URI.Query = r.URL.RawQuery
	env.HTTP.Request.URI.Full = r.RequestURI
	env.HTTP.Request.Host = r.Host
	env.HTTP.Request.Scheme = scheme
	env.HTTP.Request.BodySize = r.ContentLength

	env.IP.Src = variables.ExtractClientIP(r)

	// Geo
	if result := geo.GeoResultFromContext(r.Context()); result != nil {
		env.Geo = GeoEnv{
			Country:     result.CountryCode,
			CountryName: result.CountryName,
			City:        result.City,
		}
	} else {
		env.Geo = GeoEnv{}
	}

	// Auth
	var clientID, authType string
	if varCtx != nil && varCtx.Identity != nil {
		clientID = varCtx.Identity.ClientID
		authType = varCtx.Identity.AuthType
		// Copy claims into the pooled map
		claims := env.Auth.Claims
		for k, v := range varCtx.Identity.Claims {
			claims[k] = v
		}
		env.Auth.Claims = claims
	}
	env.Auth.ClientID = clientID
	env.Auth.Type = authType

	// Route
	var routeID string
	if varCtx != nil {
		routeID = varCtx.RouteID
		params := env.Route.Params
		for k, v := range varCtx.PathParams {
			params[k] = v
		}
		env.Route.Params = params
	}
	env.Route.ID = routeID

	// Clear response fields (may be set from prior pool usage)
	env.HTTP.Response.Code = 0
	env.HTTP.Response.ResponseTime = 0
}

// GeoEnv provides geolocation fields populated by the geo middleware.
type GeoEnv struct {
	Country     string `expr:"country"`      // ISO 3166-1 alpha-2 code
	CountryName string `expr:"country_name"` // full country name
	City        string `expr:"city"`
}

// RequestEnv is the expression environment for request-phase rules.
// Field names use Cloudflare-style dot notation via expr struct tags.
type RequestEnv struct {
	HTTP  HTTPEnv  `expr:"http"`
	IP    IPEnv    `expr:"ip"`
	Geo   GeoEnv   `expr:"geo"`
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
	q := r.URL.Query()
	args := make(map[string]string, len(q))
	for k, v := range q {
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

	// Geo fields (populated by geoMW via request context)
	var geoEnv GeoEnv
	if result := geo.GeoResultFromContext(r.Context()); result != nil {
		geoEnv = GeoEnv{
			Country:     result.CountryCode,
			CountryName: result.CountryName,
			City:        result.City,
		}
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
		Geo: geoEnv,
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
