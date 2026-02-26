package respbodygen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"text/template"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/internal/tmplutil"
	"github.com/wudi/runway/variables"
)

// TemplateData is the context available to response body generator templates.
type TemplateData struct {
	Body       string
	StatusCode int
	Headers    http.Header
	Parsed     interface{}
	RouteID    string
	Method     string
	Path       string
	PathParams map[string]string
	Query      url.Values
	ClientIP   string
	Variables  map[string]string
}

// RespBodyGen generates response bodies from templates.
type RespBodyGen struct {
	tmpl        *template.Template
	contentType string
	variables   map[string]string
	generated   atomic.Int64
}

// New creates a RespBodyGen from config.
func New(cfg config.ResponseBodyGeneratorConfig) (*RespBodyGen, error) {
	tmpl, err := template.New("respbodygen").Funcs(tmplutil.FuncMap()).Parse(cfg.Template)
	if err != nil {
		return nil, fmt.Errorf("invalid response body generator template: %w", err)
	}

	ct := cfg.ContentType
	if ct == "" {
		ct = "application/json"
	}

	return &RespBodyGen{
		tmpl:        tmpl,
		contentType: ct,
		variables:   cfg.Variables,
	}, nil
}

// Middleware returns a middleware that generates the response body from the template.
func (rbg *RespBodyGen) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &bodyBufferWriter{
				ResponseWriter: w,
				statusCode:     200,
				header:         make(http.Header),
			}
			next.ServeHTTP(bw, r)

			body := bw.body.Bytes()
			varCtx := variables.GetFromRequest(r)

			data := TemplateData{
				Body:       string(body),
				StatusCode: bw.statusCode,
				Headers:    bw.header,
				Method:     r.Method,
				Path:       r.URL.Path,
				Query:      r.URL.Query(),
				ClientIP:   variables.ExtractClientIP(r),
				Variables:  rbg.variables,
			}

			// Try to parse body as JSON
			var parsed interface{}
			if err := json.Unmarshal(body, &parsed); err == nil {
				data.Parsed = parsed
			}

			if varCtx != nil {
				data.RouteID = varCtx.RouteID
				data.PathParams = varCtx.PathParams
			}

			var buf bytes.Buffer
			if err := rbg.tmpl.Execute(&buf, data); err != nil {
				// On template error, pass through original response
				for k, vv := range bw.header {
					for _, v := range vv {
						w.Header().Add(k, v)
					}
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				w.WriteHeader(bw.statusCode)
				w.Write(body)
				return
			}

			rbg.generated.Add(1)

			// Copy captured headers to real writer
			for k, vv := range bw.header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			rendered := buf.Bytes()
			w.Header().Set("Content-Type", rbg.contentType)
			w.Header().Set("Content-Length", strconv.Itoa(len(rendered)))
			w.WriteHeader(bw.statusCode)
			w.Write(rendered)
		})
	}
}

// Generated returns the number of response bodies generated.
func (rbg *RespBodyGen) Generated() int64 {
	return rbg.generated.Load()
}

// bodyBufferWriter captures the response for transformation.
type bodyBufferWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	header     http.Header
}

func (bw *bodyBufferWriter) Header() http.Header {
	return bw.header
}

func (bw *bodyBufferWriter) WriteHeader(code int) {
	bw.statusCode = code
}

func (bw *bodyBufferWriter) Write(b []byte) (int, error) {
	return bw.body.Write(b)
}

// RespBodyGenByRoute manages per-route response body generators.
type RespBodyGenByRoute struct {
	byroute.Manager[*RespBodyGen]
}

// NewRespBodyGenByRoute creates a new per-route response body generator manager.
func NewRespBodyGenByRoute() *RespBodyGenByRoute {
	return &RespBodyGenByRoute{}
}

// AddRoute adds a response body generator for a route.
func (m *RespBodyGenByRoute) AddRoute(routeID string, cfg config.ResponseBodyGeneratorConfig) error {
	rbg, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, rbg)
	return nil
}

// GetGenerator returns the response body generator for a route.
func (m *RespBodyGenByRoute) GetGenerator(routeID string) *RespBodyGen {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route response body generator stats.
func (m *RespBodyGenByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(rbg *RespBodyGen) interface{} {
		return map[string]interface{}{"generated": rbg.Generated(), "content_type": rbg.contentType}
	})
}
