package fieldreplacer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
)

// compiledOp holds a pre-compiled replacement operation.
type compiledOp struct {
	field   string         // gjson field path
	opType  string         // "regexp", "literal", "upper", "lower", "trim"
	find    string         // pattern/chars to find
	replace string         // replacement string
	re      *regexp.Regexp // compiled regex (only for "regexp" type)
}

// FieldReplacer applies field-level content replacement on JSON response bodies.
type FieldReplacer struct {
	ops       []compiledOp
	processed atomic.Int64
}

// New creates a FieldReplacer from config, compiling all operations upfront.
func New(cfg config.FieldReplacerConfig) (*FieldReplacer, error) {
	ops := make([]compiledOp, 0, len(cfg.Operations))
	for i, op := range cfg.Operations {
		cop := compiledOp{
			field:   op.Field,
			opType:  op.Type,
			find:    op.Find,
			replace: op.Replace,
		}
		if op.Type == "regexp" {
			re, err := regexp.Compile(op.Find)
			if err != nil {
				return nil, fmt.Errorf("field_replacer operation %d: invalid regexp %q: %w", i, op.Find, err)
			}
			cop.re = re
		}
		ops = append(ops, cop)
	}
	return &FieldReplacer{ops: ops}, nil
}

// Middleware returns a middleware that buffers the response body and applies
// field replacement operations on JSON responses.
func (fr *FieldReplacer) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &bodyBufferWriter{
				ResponseWriter: w,
				statusCode:     200,
				header:         make(http.Header),
			}
			next.ServeHTTP(bw, r)

			body := bw.body.Bytes()
			result := fr.apply(body)

			// Copy captured headers to real writer.
			for k, vv := range bw.header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(result)))
			w.WriteHeader(bw.statusCode)
			w.Write(result)
		})
	}
}

// apply runs all compiled operations on the JSON body and returns the
// transformed body. Non-JSON or non-string fields are left untouched.
func (fr *FieldReplacer) apply(body []byte) []byte {
	if !json.Valid(body) {
		return body
	}

	current := string(body)
	modified := false

	for _, op := range fr.ops {
		val := gjson.Get(current, op.field)
		if !val.Exists() || val.Type != gjson.String {
			continue
		}

		original := val.Str
		var replaced string

		switch op.opType {
		case "regexp":
			replaced = op.re.ReplaceAllString(original, op.replace)
		case "literal":
			replaced = strings.ReplaceAll(original, op.find, op.replace)
		case "upper":
			replaced = strings.ToUpper(original)
		case "lower":
			replaced = strings.ToLower(original)
		case "trim":
			if op.find == "" {
				replaced = strings.TrimSpace(original)
			} else {
				replaced = strings.Trim(original, op.find)
			}
		default:
			continue
		}

		if replaced != original {
			out, err := sjson.Set(current, op.field, replaced)
			if err == nil {
				current = out
				modified = true
			}
		}
	}

	if modified {
		fr.processed.Add(1)
		return []byte(current)
	}
	return body
}

// Processed returns the number of responses that were modified.
func (fr *FieldReplacer) Processed() int64 {
	return fr.processed.Load()
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

// FieldReplacerByRoute manages per-route field replacers.
type FieldReplacerByRoute struct {
	byroute.Manager[*FieldReplacer]
}

// NewFieldReplacerByRoute creates a new per-route field replacer manager.
func NewFieldReplacerByRoute() *FieldReplacerByRoute {
	return &FieldReplacerByRoute{}
}

// AddRoute adds a field replacer for a route.
func (m *FieldReplacerByRoute) AddRoute(routeID string, cfg config.FieldReplacerConfig) error {
	fr, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, fr)
	return nil
}

// GetReplacer returns the field replacer for a route.
func (m *FieldReplacerByRoute) GetReplacer(routeID string) *FieldReplacer {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route field replacer stats.
func (m *FieldReplacerByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(fr *FieldReplacer) interface{} {
		return map[string]interface{}{
			"operations": len(fr.ops),
			"processed":  fr.Processed(),
		}
	})
}
