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
	"github.com/wudi/runway/internal/middleware/bufutil"
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
			bw := bufutil.New()
			next.ServeHTTP(bw, r)

			body := bw.Body.Bytes()
			varCtx := variables.GetFromRequest(r)

			data := TemplateData{
				Body:       string(body),
				StatusCode: bw.StatusCode,
				Headers:    bw.Header(),
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
				bw.FlushToWithLength(w, body)
				return
			}

			rbg.generated.Add(1)

			rendered := buf.Bytes()
			bufutil.CopyHeaders(w.Header(), bw.Header())
			w.Header().Set("Content-Type", rbg.contentType)
			w.Header().Set("Content-Length", strconv.Itoa(len(rendered)))
			w.WriteHeader(bw.StatusCode)
			w.Write(rendered)
		})
	}
}

// Generated returns the number of response bodies generated.
func (rbg *RespBodyGen) Generated() int64 {
	return rbg.generated.Load()
}

// RespBodyGenByRoute manages per-route response body generators.
type RespBodyGenByRoute = byroute.Factory[*RespBodyGen, config.ResponseBodyGeneratorConfig]

// NewRespBodyGenByRoute creates a new per-route response body generator manager.
func NewRespBodyGenByRoute() *RespBodyGenByRoute {
	return byroute.NewFactory(New, func(rbg *RespBodyGen) any {
		return map[string]interface{}{"generated": rbg.Generated(), "content_type": rbg.contentType}
	})
}
