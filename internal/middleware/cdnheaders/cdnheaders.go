package cdnheaders

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// CDNHeaders injects CDN cache control headers into responses.
type CDNHeaders struct {
	cacheControl     string
	vary             string
	surrogateControl string
	surrogateKey     string
	expires          time.Duration
	expiresHTTPDate  string
	override         bool
	applied          atomic.Int64
}

// Snapshot is a point-in-time copy of CDN headers metrics.
type Snapshot struct {
	Applied          int64  `json:"applied"`
	CacheControl     string `json:"cache_control,omitempty"`
	Vary             string `json:"vary,omitempty"`
	SurrogateControl string `json:"surrogate_control,omitempty"`
	Override         bool   `json:"override"`
}

// New creates a CDNHeaders from config.
func New(cfg config.CDNCacheConfig) *CDNHeaders {
	h := &CDNHeaders{
		override: true, // default
	}

	if cfg.Override != nil {
		h.override = *cfg.Override
	}

	// Build Cache-Control value
	cc := cfg.CacheControl
	if cfg.StaleWhileRevalidate > 0 {
		cc += fmt.Sprintf(", stale-while-revalidate=%d", cfg.StaleWhileRevalidate)
	}
	if cfg.StaleIfError > 0 {
		cc += fmt.Sprintf(", stale-if-error=%d", cfg.StaleIfError)
	}
	h.cacheControl = strings.TrimPrefix(cc, ", ")

	if len(cfg.Vary) > 0 {
		h.vary = strings.Join(cfg.Vary, ", ")
	}

	h.surrogateControl = cfg.SurrogateControl
	h.surrogateKey = cfg.SurrogateKey

	// Parse expires: either a duration or an HTTP-date
	if cfg.Expires != "" {
		if d, err := time.ParseDuration(cfg.Expires); err == nil {
			h.expires = d
		} else {
			h.expiresHTTPDate = cfg.Expires
		}
	}

	return h
}

// Apply injects CDN cache headers into the response.
func (c *CDNHeaders) Apply(h http.Header, override bool) {
	c.applied.Add(1)

	if c.cacheControl != "" {
		if override || h.Get("Cache-Control") == "" {
			h.Set("Cache-Control", c.cacheControl)
		}
	}

	if c.vary != "" {
		h.Set("Vary", c.vary)
	}

	if c.surrogateControl != "" {
		h.Set("Surrogate-Control", c.surrogateControl)
	}

	if c.surrogateKey != "" {
		h.Set("Surrogate-Key", c.surrogateKey)
	}

	if c.expires > 0 {
		h.Set("Expires", time.Now().Add(c.expires).UTC().Format(http.TimeFormat))
	} else if c.expiresHTTPDate != "" {
		h.Set("Expires", c.expiresHTTPDate)
	}
}

// IsOverride returns whether backend Cache-Control should be overridden.
func (c *CDNHeaders) IsOverride() bool {
	return c.override
}

// Stats returns a snapshot of CDN headers metrics.
func (c *CDNHeaders) Stats() Snapshot {
	return Snapshot{
		Applied:          c.applied.Load(),
		CacheControl:     c.cacheControl,
		Vary:             c.vary,
		SurrogateControl: c.surrogateControl,
		Override:         c.override,
	}
}

// MergeCDNCacheConfig merges per-route and global CDN cache configs.
// Per-route takes precedence when enabled.
func MergeCDNCacheConfig(perRoute, global config.CDNCacheConfig) config.CDNCacheConfig {
	if perRoute.Enabled {
		return perRoute
	}
	return global
}

// CDNHeadersByRoute manages CDN headers per route.
type CDNHeadersByRoute struct {
	byroute.Manager[*CDNHeaders]
}

// NewCDNHeadersByRoute creates a new CDN headers manager.
func NewCDNHeadersByRoute() *CDNHeadersByRoute {
	return &CDNHeadersByRoute{}
}

// AddRoute adds a CDN headers handler for a route.
func (br *CDNHeadersByRoute) AddRoute(routeID string, cfg config.CDNCacheConfig) {
	br.Add(routeID, New(cfg))
}

// GetHandler returns the CDN headers handler for a route.
func (br *CDNHeadersByRoute) GetHandler(routeID string) *CDNHeaders {
	v, _ := br.Get(routeID)
	return v
}

// Stats returns CDN headers statistics for all routes.
func (br *CDNHeadersByRoute) Stats() map[string]Snapshot {
	return byroute.CollectStats(&br.Manager, func(h *CDNHeaders) Snapshot { return h.Stats() })
}

// Middleware returns a middleware that injects CDN cache control headers into responses.
func (cdn *CDNHeaders) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cw := &cdnHeadersWriter{
				ResponseWriter: w,
				cdn:            cdn,
			}
			next.ServeHTTP(cw, r)
		})
	}
}

type cdnHeadersWriter struct {
	http.ResponseWriter
	cdn         *CDNHeaders
	wroteHeader bool
}

func (w *cdnHeadersWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.cdn.Apply(w.ResponseWriter.Header(), w.cdn.IsOverride())
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *cdnHeadersWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.cdn.Apply(w.ResponseWriter.Header(), w.cdn.IsOverride())
	}
	return w.ResponseWriter.Write(b)
}

func (w *cdnHeadersWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
