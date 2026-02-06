package variables

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// BuiltinVariables provides all built-in variable implementations
type BuiltinVariables struct{}

// NewBuiltinVariables creates a new builtin variables provider
func NewBuiltinVariables() *BuiltinVariables {
	return &BuiltinVariables{}
}

// Get returns the value of a built-in variable
func (b *BuiltinVariables) Get(name string, ctx *Context) (string, bool) {
	// Check dynamic variables first
	if prefix, suffix, ok := ParseDynamic(name); ok {
		return b.getDynamic(prefix, suffix, ctx)
	}

	// Static variables
	switch name {
	// Request variables
	case "request_id":
		return ctx.RequestID, true
	case "request_method":
		if ctx.Request != nil {
			return ctx.Request.Method, true
		}
	case "request_uri":
		if ctx.Request != nil {
			return ctx.Request.RequestURI, true
		}
	case "request_path":
		if ctx.Request != nil {
			return ctx.Request.URL.Path, true
		}
	case "query_string":
		if ctx.Request != nil {
			return ctx.Request.URL.RawQuery, true
		}
	case "remote_addr":
		if ctx.Request != nil {
			host, _, _ := net.SplitHostPort(ctx.Request.RemoteAddr)
			return host, true
		}
	case "remote_port":
		if ctx.Request != nil {
			_, port, _ := net.SplitHostPort(ctx.Request.RemoteAddr)
			return port, true
		}
	case "server_addr":
		if ctx.Request != nil {
			host, _, _ := net.SplitHostPort(ctx.Request.Host)
			if host == "" {
				host = ctx.Request.Host
			}
			return host, true
		}
	case "server_port":
		return strconv.Itoa(ctx.ServerPort), true
	case "scheme":
		if ctx.Request != nil {
			if ctx.Request.TLS != nil {
				return "https", true
			}
			return "http", true
		}
	case "host":
		if ctx.Request != nil {
			return ctx.Request.Host, true
		}
	case "content_type":
		if ctx.Request != nil {
			return ctx.Request.Header.Get("Content-Type"), true
		}
	case "content_length":
		if ctx.Request != nil {
			return strconv.FormatInt(ctx.Request.ContentLength, 10), true
		}

	// Upstream variables
	case "upstream_addr":
		return ctx.UpstreamAddr, true
	case "upstream_status":
		return strconv.Itoa(ctx.UpstreamStatus), true
	case "upstream_response_time":
		return fmt.Sprintf("%.3f", ctx.UpstreamResponseTime.Seconds()*1000), true

	// Response variables
	case "status":
		return strconv.Itoa(ctx.Status), true
	case "body_bytes_sent":
		return strconv.FormatInt(ctx.BodyBytesSent, 10), true
	case "response_time":
		return fmt.Sprintf("%.3f", ctx.ResponseTime.Seconds()*1000), true

	// Time variables
	case "time_iso8601":
		return time.Now().Format(time.RFC3339), true
	case "time_unix":
		return strconv.FormatInt(time.Now().Unix(), 10), true
	case "time_local":
		return time.Now().Format("02/Jan/2006:15:04:05 -0700"), true

	// Route variables
	case "route_id":
		return ctx.RouteID, true

	// Auth variables
	case "auth_client_id":
		if ctx.Identity != nil {
			return ctx.Identity.ClientID, true
		}
		return "", true
	case "auth_type":
		if ctx.Identity != nil {
			return ctx.Identity.AuthType, true
		}
		return "", true

	// Client certificate variables (mTLS)
	case "client_cert_subject":
		if ctx.CertInfo != nil {
			return ctx.CertInfo.Subject, true
		}
		return "", true
	case "client_cert_issuer":
		if ctx.CertInfo != nil {
			return ctx.CertInfo.Issuer, true
		}
		return "", true
	case "client_cert_fingerprint":
		if ctx.CertInfo != nil {
			return ctx.CertInfo.Fingerprint, true
		}
		return "", true
	case "client_cert_serial":
		if ctx.CertInfo != nil {
			return ctx.CertInfo.SerialNumber, true
		}
		return "", true
	case "client_cert_dns_names":
		if ctx.CertInfo != nil {
			return strings.Join(ctx.CertInfo.DNSNames, ","), true
		}
		return "", true
	}

	return "", false
}

// getDynamic handles dynamic variable prefixes
func (b *BuiltinVariables) getDynamic(prefix, suffix string, ctx *Context) (string, bool) {
	switch prefix {
	case "http":
		// $http_x_custom_header -> X-Custom-Header
		if ctx.Request != nil {
			headerName := NormalizeHeaderName(suffix)
			return ctx.Request.Header.Get(headerName), true
		}
	case "arg":
		// $arg_page -> query parameter "page"
		if ctx.Request != nil {
			return ctx.Request.URL.Query().Get(suffix), true
		}
	case "cookie":
		// $cookie_session_id -> cookie "session_id"
		if ctx.Request != nil {
			cookie, err := ctx.Request.Cookie(suffix)
			if err == nil {
				return cookie.Value, true
			}
			return "", true
		}
	case "route_param":
		// $route_param_user_id -> path parameter "user_id"
		if ctx.PathParams != nil {
			return ctx.PathParams[suffix], true
		}
	case "jwt_claim":
		// $jwt_claim_sub -> JWT claim "sub"
		if ctx.Identity != nil && ctx.Identity.Claims != nil {
			if val, ok := ctx.Identity.Claims[suffix]; ok {
				return fmt.Sprintf("%v", val), true
			}
		}
		return "", true
	}

	return "", false
}

// AllVariables returns a list of all built-in variable names
func (b *BuiltinVariables) AllVariables() []string {
	return []string{
		// Request
		"request_id",
		"request_method",
		"request_uri",
		"request_path",
		"query_string",
		"remote_addr",
		"remote_port",
		"server_addr",
		"server_port",
		"scheme",
		"host",
		"content_type",
		"content_length",

		// Dynamic (examples)
		"http_<name>",
		"arg_<name>",
		"cookie_<name>",
		"route_param_<name>",
		"jwt_claim_<name>",

		// Upstream
		"upstream_addr",
		"upstream_status",
		"upstream_response_time",

		// Response
		"status",
		"body_bytes_sent",
		"response_time",

		// Time
		"time_iso8601",
		"time_unix",
		"time_local",

		// Route
		"route_id",

		// Auth
		"auth_client_id",
		"auth_type",

		// Client certificate (mTLS)
		"client_cert_subject",
		"client_cert_issuer",
		"client_cert_fingerprint",
		"client_cert_serial",
		"client_cert_dns_names",
	}
}

// Identity represents an authenticated identity
type Identity struct {
	ClientID string
	AuthType string // "jwt", "api_key"
	Claims   map[string]interface{}
}

// CertInfo holds extracted client certificate information for mTLS.
type CertInfo struct {
	Subject      string
	Issuer       string
	SerialNumber string
	Fingerprint  string
	DNSNames     []string
}

// Context holds the context for variable resolution
type Context struct {
	Request              *http.Request
	Response             *http.Response
	RequestID            string
	RouteID              string
	PathParams           map[string]string
	Identity             *Identity
	CertInfo             *CertInfo
	UpstreamAddr         string
	UpstreamStatus       int
	UpstreamResponseTime time.Duration
	StartTime            time.Time
	ResponseTime         time.Duration
	Status               int
	BodyBytesSent        int64
	ServerPort           int

	// Traffic management
	TrafficGroup string

	// Custom values
	Custom map[string]string
}

// NewContext creates a new variable context
func NewContext(r *http.Request) *Context {
	return &Context{
		Request:   r,
		StartTime: time.Now(),
		Custom:    make(map[string]string),
	}
}

// Clone creates a copy of the context
func (c *Context) Clone() *Context {
	newCtx := &Context{
		Request:              c.Request,
		Response:             c.Response,
		RequestID:            c.RequestID,
		RouteID:              c.RouteID,
		Identity:             c.Identity,
		CertInfo:             c.CertInfo,
		UpstreamAddr:         c.UpstreamAddr,
		UpstreamStatus:       c.UpstreamStatus,
		UpstreamResponseTime: c.UpstreamResponseTime,
		StartTime:            c.StartTime,
		ResponseTime:         c.ResponseTime,
		Status:               c.Status,
		BodyBytesSent:        c.BodyBytesSent,
		ServerPort:           c.ServerPort,
		Custom:               make(map[string]string),
	}

	if c.PathParams != nil {
		newCtx.PathParams = make(map[string]string)
		for k, v := range c.PathParams {
			newCtx.PathParams[k] = v
		}
	}

	for k, v := range c.Custom {
		newCtx.Custom[k] = v
	}

	return newCtx
}

// SetCustom sets a custom variable value
func (c *Context) SetCustom(name, value string) {
	c.Custom[name] = value
}

// GetCustom returns a custom variable value
func (c *Context) GetCustom(name string) (string, bool) {
	v, ok := c.Custom[name]
	return v, ok
}

// RequestContextKey is the context key for storing variable context
type RequestContextKey struct{}

// GetFromRequest extracts the variable context from an HTTP request
func GetFromRequest(r *http.Request) *Context {
	if ctx, ok := r.Context().Value(RequestContextKey{}).(*Context); ok {
		return ctx
	}
	return NewContext(r)
}

// FormatBytes formats bytes as human readable
func FormatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// ExtractClientIP extracts the real client IP from headers or RemoteAddr
func ExtractClientIP(r *http.Request) string {
	// Check X-Forwarded-For first
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
