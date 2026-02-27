package jmespath

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/jmespath/go-jmespath"
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/internal/middleware/bufutil"
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
			bw := bufutil.New()
			next.ServeHTTP(bw, r)

			body := bw.Body.Bytes()

			// Only transform JSON responses
			ct := bw.Header().Get("Content-Type")
			if !isJSON(ct) {
				bw.FlushToWithLength(w, body)
				return
			}

			// Decode the response body
			var data interface{}
			if err := json.Unmarshal(body, &data); err != nil {
				bw.FlushToWithLength(w, body)
				return
			}

			// Apply the JMESPath expression
			result, err := jp.compiled.Search(data)
			if err != nil {
				bw.FlushToWithLength(w, body)
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
				bw.FlushToWithLength(w, body)
				return
			}

			jp.applied.Add(1)

			bufutil.CopyHeaders(w.Header(), bw.Header())
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
			w.WriteHeader(bw.StatusCode)
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

// JMESPathByRoute manages per-route JMESPath instances.
type JMESPathByRoute = byroute.Factory[*JMESPath, config.JMESPathConfig]

// NewJMESPathByRoute creates a new per-route JMESPath manager.
func NewJMESPathByRoute() *JMESPathByRoute {
	return byroute.NewFactory(New, func(jp *JMESPath) any { return jp.Stats() })
}
