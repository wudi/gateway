package variables

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wudi/runway/internal/middleware/realip"
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
	case "api_version":
		return ctx.APIVersion, true

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
		"api_version",

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

// SkipFlags is a bitfield controlling which middleware to skip for a request.
// Set by rule actions (e.g. skip_auth, skip_rate_limit) and checked inline
// at the top of each middleware handler.
type SkipFlags uint32

const (
	SkipAuth                SkipFlags = 1 << iota
	SkipRateLimit
	SkipThrottle
	SkipCircuitBreaker
	SkipWAF
	SkipValidation
	SkipCompression
	SkipAdaptiveConcurrency
	SkipBodyLimit
	SkipMirror
	SkipAccessLog
	SkipCacheStore
)

// ValueOverrides holds per-request override values set by rule actions.
// Allocated lazily (nil for 99%+ of requests with no override rules).
type ValueOverrides struct {
	RateLimitTier     string
	TimeoutOverride   time.Duration
	PriorityOverride  int
	BandwidthOverride int64
	BodyLimitOverride int64
	SwitchBackend     string
	CacheTTLOverride  time.Duration
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

	// API versioning
	APIVersion string

	// Tenant identification
	TenantID string

	// Access log config (interface{} to avoid import cycle)
	AccessLogConfig interface{}

	// Trace propagation flag (set by baggage middleware, read by proxy)
	PropagateTrace bool

	// Rule-driven middleware control
	SkipFlags SkipFlags
	Overrides *ValueOverrides // nil when no overrides active

	// Custom values
	Custom map[string]string
}

var contextPool = sync.Pool{
	New: func() any { return &Context{} },
}

// AcquireContext gets a Context from the pool and initialises it for r.
func AcquireContext(r *http.Request) *Context {
	c := contextPool.Get().(*Context)
	c.Request = r
	c.StartTime = time.Now()
	return c
}

// ReleaseContext zeroes all fields and returns c to the pool.
// The caller must ensure no goroutine reads from c after this call.
func ReleaseContext(c *Context) {
	if c == nil {
		return
	}
	c.Request = nil
	c.Response = nil
	c.RequestID = ""
	c.RouteID = ""
	c.PathParams = nil
	c.Identity = nil
	c.CertInfo = nil
	c.UpstreamAddr = ""
	c.UpstreamStatus = 0
	c.UpstreamResponseTime = 0
	c.StartTime = time.Time{}
	c.ResponseTime = 0
	c.Status = 0
	c.BodyBytesSent = 0
	c.ServerPort = 0
	c.TrafficGroup = ""
	c.APIVersion = ""
	c.TenantID = ""
	c.AccessLogConfig = nil
	c.PropagateTrace = false
	c.SkipFlags = 0
	c.Overrides = nil
	c.Custom = nil
	contextPool.Put(c)
}

// NewContext creates a new variable context
func NewContext(r *http.Request) *Context {
	return AcquireContext(r)
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
		TrafficGroup:         c.TrafficGroup,
		APIVersion:           c.APIVersion,
		TenantID:             c.TenantID,
		AccessLogConfig:      c.AccessLogConfig,
		PropagateTrace:       c.PropagateTrace,
		SkipFlags:            c.SkipFlags,
	}

	if c.Overrides != nil {
		copied := *c.Overrides
		newCtx.Overrides = &copied
	}

	if c.PathParams != nil {
		newCtx.PathParams = make(map[string]string, len(c.PathParams))
		for k, v := range c.PathParams {
			newCtx.PathParams[k] = v
		}
	}

	if len(c.Custom) > 0 {
		newCtx.Custom = make(map[string]string, len(c.Custom))
		for k, v := range c.Custom {
			newCtx.Custom[k] = v
		}
	}

	return newCtx
}

// SetCustom sets a custom variable value
func (c *Context) SetCustom(name, value string) {
	if c.Custom == nil {
		c.Custom = make(map[string]string)
	}
	c.Custom[name] = value
}

// GetCustom returns a custom variable value
func (c *Context) GetCustom(name string) (string, bool) {
	if c.Custom == nil {
		return "", false
	}
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
	return AcquireContext(r)
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

// ExtractClientIP extracts the real client IP from the request.
// If the realip middleware has stored a trusted-proxy-aware IP in the
// request context, that value is returned. Otherwise falls back to
// X-Forwarded-For, X-Real-IP, and finally RemoteAddr.
func ExtractClientIP(r *http.Request) string {
	// Check for realip middleware result in context
	if ip := realip.FromContext(r.Context()); ip != "" {
		return ip
	}

	// Fallback: legacy behavior (no trusted proxy config)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
