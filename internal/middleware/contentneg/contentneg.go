package contentneg

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/goccy/go-yaml"
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware"
)

// Negotiator handles content negotiation for a route.
type Negotiator struct {
	supported      map[string]bool
	defaultFmt     string
	outputEncoding string // if set, overrides Accept header
	jsonCount      atomic.Int64
	xmlCount       atomic.Int64
	yamlCount      atomic.Int64
	notAcceptable  atomic.Int64
}

// New creates a Negotiator from config. outputEncoding overrides Accept header when set.
func New(cfg config.ContentNegotiationConfig, outputEncoding ...string) (*Negotiator, error) {
	supported := make(map[string]bool, len(cfg.Supported))
	for _, f := range cfg.Supported {
		switch f {
		case "json", "xml", "yaml":
			supported[f] = true
		default:
			return nil, fmt.Errorf("unsupported content negotiation format: %s", f)
		}
	}

	defaultFmt := cfg.Default
	if defaultFmt == "" {
		defaultFmt = "json"
	}
	if !supported[defaultFmt] {
		return nil, fmt.Errorf("default format %q not in supported list", defaultFmt)
	}

	var outEnc string
	if len(outputEncoding) > 0 {
		outEnc = outputEncoding[0]
	}

	return &Negotiator{
		supported:      supported,
		defaultFmt:     defaultFmt,
		outputEncoding: outEnc,
	}, nil
}

// Middleware returns a middleware that re-encodes response based on Accept header or output_encoding.
func (n *Negotiator) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Determine format: output_encoding overrides Accept header
			var format string
			if n.outputEncoding != "" {
				format = n.outputEncoding
			} else {
				format = n.negotiate(r.Header.Get("Accept"))
			}

			if format == "" {
				n.notAcceptable.Add(1)
				http.Error(w, "406 Not Acceptable", http.StatusNotAcceptable)
				return
			}

			if format == "json" {
				n.jsonCount.Add(1)
				next.ServeHTTP(w, r)
				return
			}

			// Buffer the response for re-encoding
			bw := &bodyBufferWriter{
				ResponseWriter: w,
				statusCode:     200,
				header:         make(http.Header),
			}
			next.ServeHTTP(bw, r)

			body := bw.body.Bytes()

			var encoded []byte
			var contentType string
			var err error

			switch format {
			case "xml":
				encoded, err = jsonToXML(body)
				contentType = "application/xml; charset=utf-8"
				n.xmlCount.Add(1)
			case "yaml":
				encoded, err = jsonToYAML(body)
				contentType = "application/yaml; charset=utf-8"
				n.yamlCount.Add(1)
			case "json-collection":
				encoded, err = jsonCollectionExtract(body)
				contentType = "application/json; charset=utf-8"
				n.jsonCount.Add(1)
			case "string":
				encoded = body
				contentType = "text/plain; charset=utf-8"
				n.jsonCount.Add(1)
			default:
				// Unknown output encoding — pass through
				encoded = body
			}

			if err != nil {
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

			for k, vv := range bw.header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
			w.WriteHeader(bw.statusCode)
			w.Write(encoded)
		})
	}
}

// jsonCollectionExtract extracts the inner array from a wrapped collection object.
// If body is {"collection": [...]} or {"items": [...]}, returns the inner array.
// If body is already an array, passes through.
func jsonCollectionExtract(data []byte) ([]byte, error) {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return data, nil
	}

	switch v := raw.(type) {
	case []interface{}:
		return data, nil // already an array
	case map[string]interface{}:
		for _, key := range []string{"collection", "items"} {
			if arr, ok := v[key]; ok {
				if _, isArr := arr.([]interface{}); isArr {
					return json.Marshal(arr)
				}
			}
		}
	}

	return data, nil
}

// negotiate parses Accept header and returns the best matching format.
func (n *Negotiator) negotiate(accept string) string {
	if accept == "" {
		return n.defaultFmt
	}

	type mediaType struct {
		format  string
		quality float64
	}

	var candidates []mediaType

	for _, part := range strings.Split(accept, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		mt := part
		q := 1.0

		if idx := strings.Index(part, ";"); idx >= 0 {
			mt = strings.TrimSpace(part[:idx])
			params := strings.TrimSpace(part[idx+1:])
			if strings.HasPrefix(params, "q=") {
				if v, err := strconv.ParseFloat(params[2:], 64); err == nil {
					q = v
				}
			}
		}

		mt = strings.ToLower(mt)

		var format string
		switch mt {
		case "application/json", "text/json":
			format = "json"
		case "application/xml", "text/xml":
			format = "xml"
		case "application/yaml", "text/yaml", "application/x-yaml":
			format = "yaml"
		case "*/*":
			format = n.defaultFmt
		default:
			continue
		}

		if n.supported[format] {
			candidates = append(candidates, mediaType{format: format, quality: q})
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	// Sort by quality descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].quality > candidates[j].quality
	})

	return candidates[0].format
}

// jsonToXML converts JSON bytes to XML.
func jsonToXML(data []byte) ([]byte, error) {
	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.WriteString("<response>")
	writeXMLValue(&buf, parsed)
	buf.WriteString("</response>")
	return buf.Bytes(), nil
}

func writeXMLValue(buf *bytes.Buffer, v interface{}) {
	switch val := v.(type) {
	case map[string]interface{}:
		for k, child := range val {
			safeName := xmlSafeName(k)
			buf.WriteString("<" + safeName + ">")
			writeXMLValue(buf, child)
			buf.WriteString("</" + safeName + ">")
		}
	case []interface{}:
		for _, child := range val {
			buf.WriteString("<item>")
			writeXMLValue(buf, child)
			buf.WriteString("</item>")
		}
	case string:
		xml.EscapeText(buf, []byte(val))
	case float64:
		if val == float64(int64(val)) {
			buf.WriteString(strconv.FormatInt(int64(val), 10))
		} else {
			buf.WriteString(strconv.FormatFloat(val, 'f', -1, 64))
		}
	case bool:
		buf.WriteString(strconv.FormatBool(val))
	case nil:
		// empty element
	default:
		buf.WriteString(fmt.Sprintf("%v", val))
	}
}

// xmlSafeName ensures the name is a valid XML element name.
func xmlSafeName(name string) string {
	if name == "" {
		return "element"
	}
	// Replace invalid XML name chars with underscore
	var b strings.Builder
	for i, r := range name {
		if i == 0 && (r >= '0' && r <= '9') {
			b.WriteRune('_')
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// jsonToYAML converts JSON bytes to YAML.
func jsonToYAML(data []byte) ([]byte, error) {
	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}

	return yaml.Marshal(parsed)
}

// bodyBufferWriter captures the response for re-encoding.
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

// Stats returns negotiator stats.
func (n *Negotiator) Stats() map[string]interface{} {
	return map[string]interface{}{
		"json_count":      n.jsonCount.Load(),
		"xml_count":       n.xmlCount.Load(),
		"yaml_count":      n.yamlCount.Load(),
		"not_acceptable":  n.notAcceptable.Load(),
	}
}

// NegotiatorByRoute manages per-route negotiators.
type NegotiatorByRoute struct {
	byroute.Manager[*Negotiator]
}

// NewNegotiatorByRoute creates a new per-route negotiator manager.
func NewNegotiatorByRoute() *NegotiatorByRoute {
	return &NegotiatorByRoute{}
}

// AddRoute adds a negotiator for a route. Optional outputEncoding overrides Accept header.
func (m *NegotiatorByRoute) AddRoute(routeID string, cfg config.ContentNegotiationConfig, outputEncoding ...string) error {
	n, err := New(cfg, outputEncoding...)
	if err != nil {
		return err
	}
	m.Add(routeID, n)
	return nil
}

// GetNegotiator returns the negotiator for a route.
func (m *NegotiatorByRoute) GetNegotiator(routeID string) *Negotiator {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route negotiator stats.
func (m *NegotiatorByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(n *Negotiator) interface{} { return n.Stats() })
}
