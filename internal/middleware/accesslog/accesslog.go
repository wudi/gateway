package accesslog

import (
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/variables"
)

// DefaultSensitiveHeaders are always masked unless overridden.
var DefaultSensitiveHeaders = []string{"Authorization", "Cookie", "Set-Cookie", "X-API-Key"}

// StatusRange represents a contiguous range of HTTP status codes.
type StatusRange struct {
	Lo, Hi int
}

// ParseStatusRange parses a status range string like "4xx", "200", "200-299".
func ParseStatusRange(s string) (StatusRange, error) {
	s = strings.TrimSpace(s)
	// Pattern: Nxx (e.g. "4xx", "5xx")
	if len(s) == 3 && s[1] == 'x' && s[2] == 'x' {
		base := int(s[0]-'0') * 100
		if base < 100 || base > 500 {
			return StatusRange{}, &ParseError{Input: s}
		}
		return StatusRange{Lo: base, Hi: base + 99}, nil
	}
	// Pattern: N-M (e.g. "200-299")
	if parts := strings.SplitN(s, "-", 2); len(parts) == 2 {
		lo, err1 := strconv.Atoi(parts[0])
		hi, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || lo < 100 || hi > 599 || lo > hi {
			return StatusRange{}, &ParseError{Input: s}
		}
		return StatusRange{Lo: lo, Hi: hi}, nil
	}
	// Pattern: single code (e.g. "200")
	code, err := strconv.Atoi(s)
	if err != nil || code < 100 || code > 599 {
		return StatusRange{}, &ParseError{Input: s}
	}
	return StatusRange{Lo: code, Hi: code}, nil
}

// ParseError is returned when a status range string is invalid.
type ParseError struct {
	Input string
}

func (e *ParseError) Error() string {
	return "invalid status range: " + e.Input
}

// CompiledAccessLog holds pre-compiled per-route access log settings.
type CompiledAccessLog struct {
	Enabled          *bool
	Format           string
	headersInclude   map[string]bool
	headersExclude   map[string]bool
	sensitiveHeaders map[string]bool
	Body             config.AccessLogBodyConfig
	statusRanges     []StatusRange
	methods          map[string]bool
	sampleRate       float64
	contentTypes     map[string]bool
}

// New compiles an AccessLogConfig into a CompiledAccessLog.
func New(cfg config.AccessLogConfig) (*CompiledAccessLog, error) {
	c := &CompiledAccessLog{
		Enabled:    cfg.Enabled,
		Format:     cfg.Format,
		Body:       cfg.Body,
		sampleRate: cfg.Conditions.SampleRate,
	}

	// Default body max size
	if c.Body.Enabled && c.Body.MaxSize <= 0 {
		c.Body.MaxSize = 4096
	}

	// Compile header include set
	if len(cfg.HeadersInclude) > 0 {
		c.headersInclude = make(map[string]bool, len(cfg.HeadersInclude))
		for _, h := range cfg.HeadersInclude {
			c.headersInclude[http.CanonicalHeaderKey(h)] = true
		}
	}

	// Compile header exclude set
	if len(cfg.HeadersExclude) > 0 {
		c.headersExclude = make(map[string]bool, len(cfg.HeadersExclude))
		for _, h := range cfg.HeadersExclude {
			c.headersExclude[http.CanonicalHeaderKey(h)] = true
		}
	}

	// Compile sensitive headers (merge defaults + user list)
	c.sensitiveHeaders = make(map[string]bool)
	for _, h := range DefaultSensitiveHeaders {
		c.sensitiveHeaders[http.CanonicalHeaderKey(h)] = true
	}
	for _, h := range cfg.SensitiveHeaders {
		c.sensitiveHeaders[http.CanonicalHeaderKey(h)] = true
	}

	// Compile status ranges
	for _, sc := range cfg.Conditions.StatusCodes {
		sr, err := ParseStatusRange(sc)
		if err != nil {
			return nil, err
		}
		c.statusRanges = append(c.statusRanges, sr)
	}

	// Compile methods
	if len(cfg.Conditions.Methods) > 0 {
		c.methods = make(map[string]bool, len(cfg.Conditions.Methods))
		for _, m := range cfg.Conditions.Methods {
			c.methods[strings.ToUpper(m)] = true
		}
	}

	// Compile content types for body capture
	if len(cfg.Body.ContentTypes) > 0 {
		c.contentTypes = make(map[string]bool, len(cfg.Body.ContentTypes))
		for _, ct := range cfg.Body.ContentTypes {
			c.contentTypes[strings.ToLower(ct)] = true
		}
	}

	return c, nil
}

// ShouldLog returns true if the request/response should be logged given status and method.
func (c *CompiledAccessLog) ShouldLog(status int, method string) bool {
	// Check sampling first
	if c.sampleRate > 0 && c.sampleRate < 1.0 {
		if rand.Float64() >= c.sampleRate {
			return false
		}
	}

	// Check method filter
	if c.methods != nil {
		if !c.methods[method] {
			return false
		}
	}

	// Check status code filter
	if len(c.statusRanges) > 0 {
		matched := false
		for _, sr := range c.statusRanges {
			if status >= sr.Lo && status <= sr.Hi {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

// MaskHeaderValue returns "***" if the header name is sensitive, otherwise returns the value.
func (c *CompiledAccessLog) MaskHeaderValue(name, value string) string {
	if c.sensitiveHeaders[http.CanonicalHeaderKey(name)] {
		return "***"
	}
	return value
}

// CaptureRequestHeaders returns a filtered+masked map of request headers.
func (c *CompiledAccessLog) CaptureRequestHeaders(r *http.Request) map[string]string {
	return c.captureHeaders(r.Header)
}

// CaptureResponseHeaders returns a filtered+masked map of response headers.
func (c *CompiledAccessLog) CaptureResponseHeaders(h http.Header) map[string]string {
	return c.captureHeaders(h)
}

func (c *CompiledAccessLog) captureHeaders(h http.Header) map[string]string {
	result := make(map[string]string)
	for name, vals := range h {
		canonical := http.CanonicalHeaderKey(name)
		if c.headersInclude != nil && !c.headersInclude[canonical] {
			continue
		}
		if c.headersExclude != nil && c.headersExclude[canonical] {
			continue
		}
		result[canonical] = c.MaskHeaderValue(canonical, strings.Join(vals, ", "))
	}
	return result
}

// ShouldCaptureBody returns true if body capture is applicable for the given content type.
func (c *CompiledAccessLog) ShouldCaptureBody(contentType string) bool {
	if !c.Body.Enabled {
		return false
	}
	if c.contentTypes == nil {
		return true
	}
	// Extract mime type (strip parameters like charset)
	ct := strings.ToLower(contentType)
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	return c.contentTypes[ct]
}

// HasHeaderCapture returns true if any header capture is configured.
func (c *CompiledAccessLog) HasHeaderCapture() bool {
	return c.headersInclude != nil || c.headersExclude != nil
}

// Middleware returns a middleware that stores access log config on the variable context
// and optionally captures request/response bodies for the global logging middleware.
func (cfg *CompiledAccessLog) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)
			varCtx.AccessLogConfig = cfg

			if cfg.Body.Enabled && cfg.Body.Request {
				if cfg.ShouldCaptureBody(r.Header.Get("Content-Type")) {
					body, err := io.ReadAll(io.LimitReader(r.Body, int64(cfg.Body.MaxSize)+1))
					if err == nil {
						truncated := len(body) > cfg.Body.MaxSize
						if truncated {
							body = body[:cfg.Body.MaxSize]
						}
						varCtx.Custom["_al_req_body"] = string(body)
						r.Body = io.NopCloser(io.MultiReader(
							strings.NewReader(string(body)),
							r.Body,
						))
					}
				}
			}

			if cfg.Body.Enabled && cfg.Body.Response {
				bcw := NewBodyCapturingWriter(w, cfg.Body.MaxSize)
				next.ServeHTTP(bcw, r)
				if cfg.ShouldCaptureBody(bcw.Header().Get("Content-Type")) {
					varCtx.Custom["_al_resp_body"] = bcw.CapturedBody()
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
