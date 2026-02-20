package jmespath

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/jmespath/go-jmespath"
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
)

// JMESPath applies a pre-compiled JMESPath expression to JSON response bodies.
type JMESPath struct {
	compiled        *jmespath.JMESPath
	wrapCollections bool
	applied         atomic.Int64
}

// New creates a JMESPath from config, compiling the expression at init time.
func New(cfg config.JMESPathConfig) (*JMESPath, error) {
	if cfg.Expression == "" {
		return nil, fmt.Errorf("jmespath: expression is required")
	}
	compiled, err := jmespath.Compile(cfg.Expression)
	if err != nil {
		return nil, fmt.Errorf("jmespath: invalid expression %q: %w", cfg.Expression, err)
	}
	return &JMESPath{
		compiled:        compiled,
		wrapCollections: cfg.WrapCollections,
	}, nil
}

// Middleware returns a middleware that buffers the response body, applies the
// JMESPath expression, and re-encodes the result as JSON.
func (jp *JMESPath) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &bodyBufferWriter{
				ResponseWriter: w,
				statusCode:     200,
				header:         make(http.Header),
			}
			next.ServeHTTP(bw, r)

			body := bw.body.Bytes()

			// Only transform JSON responses
			ct := bw.header.Get("Content-Type")
			if !isJSON(ct) {
				flushOriginal(w, bw, body)
				return
			}

			// Decode the response body
			var data interface{}
			if err := json.Unmarshal(body, &data); err != nil {
				flushOriginal(w, bw, body)
				return
			}

			// Apply the JMESPath expression
			result, err := jp.compiled.Search(data)
			if err != nil {
				flushOriginal(w, bw, body)
				return
			}

			// Wrap array results if configured
			if jp.wrapCollections {
				if arr, ok := result.([]interface{}); ok {
					result = map[string]interface{}{"collection": arr}
				}
			}

			// Re-encode to JSON
			encoded, err := json.Marshal(result)
			if err != nil {
				flushOriginal(w, bw, body)
				return
			}

			jp.applied.Add(1)

			// Copy captured headers to real writer
			for k, vv := range bw.header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
			w.WriteHeader(bw.statusCode)
			w.Write(encoded)
		})
	}
}

// Applied returns the number of successful JMESPath transformations.
func (jp *JMESPath) Applied() int64 {
	return jp.applied.Load()
}

// Stats returns stats for this JMESPath instance.
func (jp *JMESPath) Stats() map[string]interface{} {
	return map[string]interface{}{
		"applied":          jp.applied.Load(),
		"wrap_collections": jp.wrapCollections,
	}
}

// isJSON returns true if the Content-Type header indicates a JSON response.
func isJSON(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/json")
}

// flushOriginal writes the buffered response unchanged.
func flushOriginal(w http.ResponseWriter, bw *bodyBufferWriter, body []byte) {
	for k, vv := range bw.header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(bw.statusCode)
	w.Write(body)
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

// JMESPathByRoute manages per-route JMESPath instances.
type JMESPathByRoute struct {
	byroute.Manager[*JMESPath]
}

// NewJMESPathByRoute creates a new per-route JMESPath manager.
func NewJMESPathByRoute() *JMESPathByRoute {
	return &JMESPathByRoute{}
}

// AddRoute adds a JMESPath instance for a route.
func (m *JMESPathByRoute) AddRoute(routeID string, cfg config.JMESPathConfig) error {
	jp, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, jp)
	return nil
}

// GetJMESPath returns the JMESPath instance for a route.
func (m *JMESPathByRoute) GetJMESPath(routeID string) *JMESPath {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route JMESPath stats.
func (m *JMESPathByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(jp *JMESPath) interface{} { return jp.Stats() })
}
