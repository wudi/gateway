package bodygen

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"text/template"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/tmplutil"
	"github.com/wudi/gateway/internal/variables"
)

// TemplateData is the context available to body generator templates.
type TemplateData struct {
	Method     string
	URL        string
	Host       string
	Path       string
	PathParams map[string]string
	Query      url.Values
	Headers    http.Header
	Body       string
	ClientIP   string
	RouteID    string
	Variables  map[string]string
}

// BodyGen generates request bodies from templates.
type BodyGen struct {
	tmpl        *template.Template
	contentType string
	variables   map[string]string
	generated   atomic.Int64
}

// New creates a BodyGen from config.
func New(cfg config.BodyGeneratorConfig) (*BodyGen, error) {
	tmpl, err := template.New("bodygen").Funcs(tmplutil.FuncMap()).Parse(cfg.Template)
	if err != nil {
		return nil, fmt.Errorf("invalid body generator template: %w", err)
	}

	ct := cfg.ContentType
	if ct == "" {
		ct = "application/json"
	}

	return &BodyGen{
		tmpl:        tmpl,
		contentType: ct,
		variables:   cfg.Variables,
	}, nil
}

// Middleware returns a middleware that generates the request body from the template.
func (bg *BodyGen) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)

			// Read original body if present
			var origBody string
			if r.Body != nil {
				bodyBytes, err := io.ReadAll(r.Body)
				r.Body.Close()
				if err == nil {
					origBody = string(bodyBytes)
				}
			}

			data := TemplateData{
				Method:     r.Method,
				URL:        r.URL.String(),
				Host:       r.Host,
				Path:       r.URL.Path,
				PathParams: varCtx.PathParams,
				Query:      r.URL.Query(),
				Headers:    r.Header,
				Body:       origBody,
				ClientIP:   variables.ExtractClientIP(r),
				RouteID:    varCtx.RouteID,
				Variables:  bg.variables,
			}

			var buf bytes.Buffer
			if err := bg.tmpl.Execute(&buf, data); err != nil {
				http.Error(w, "body generation failed", http.StatusInternalServerError)
				return
			}

			bg.generated.Add(1)

			r.Body = io.NopCloser(&buf)
			r.ContentLength = int64(buf.Len())
			r.Header.Set("Content-Type", bg.contentType)
			r.Header.Set("Content-Length", strconv.Itoa(buf.Len()))

			next.ServeHTTP(w, r)
		})
	}
}

// Generated returns the number of bodies generated.
func (bg *BodyGen) Generated() int64 {
	return bg.generated.Load()
}

// BodyGenByRoute manages per-route body generators.
type BodyGenByRoute struct {
	byroute.Manager[*BodyGen]
}

// NewBodyGenByRoute creates a new per-route body generator manager.
func NewBodyGenByRoute() *BodyGenByRoute {
	return &BodyGenByRoute{}
}

// AddRoute adds a body generator for a route.
func (m *BodyGenByRoute) AddRoute(routeID string, cfg config.BodyGeneratorConfig) error {
	bg, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, bg)
	return nil
}

// GetGenerator returns the body generator for a route.
func (m *BodyGenByRoute) GetGenerator(routeID string) *BodyGen {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route body generator stats.
func (m *BodyGenByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(bg *BodyGen) interface{} {
		return map[string]interface{}{"generated": bg.Generated(), "content_type": bg.contentType}
	})
}
