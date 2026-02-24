package backendenc

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/goccy/go-yaml"
	"github.com/mmcdole/gofeed"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
)

// Encoder decodes backend responses from a non-JSON format to JSON.
type Encoder struct {
	encoding string
	encoded  atomic.Int64
	errors   atomic.Int64
}

// Snapshot is a point-in-time copy of encoder metrics.
type Snapshot struct {
	Encoding string `json:"encoding"`
	Encoded  int64  `json:"encoded"`
	Errors   int64  `json:"errors"`
}

// New creates an Encoder for the given encoding type.
func New(cfg config.BackendEncodingConfig) *Encoder {
	return &Encoder{
		encoding: cfg.Encoding,
	}
}

// Encoding returns the configured encoding type.
func (e *Encoder) Encoding() string {
	return e.encoding
}

// Decode converts the input bytes from the configured encoding to JSON.
// Returns the JSON bytes and true on success, or original bytes and false on failure.
func (e *Encoder) Decode(data []byte, contentType string) ([]byte, bool) {
	switch e.encoding {
	case "xml":
		if !isXML(contentType) {
			return data, false
		}
		result, err := xmlToJSON(data)
		if err != nil {
			e.errors.Add(1)
			return data, false
		}
		e.encoded.Add(1)
		return result, true
	case "yaml":
		if !isYAML(contentType) {
			return data, false
		}
		result, err := yamlToJSON(data)
		if err != nil {
			e.errors.Add(1)
			return data, false
		}
		e.encoded.Add(1)
		return result, true
	case "safejson":
		result, err := safeJSONDecode(data)
		if err != nil {
			e.errors.Add(1)
			return data, false
		}
		e.encoded.Add(1)
		return result, true
	case "rss":
		result, err := rssDecode(data)
		if err != nil {
			e.errors.Add(1)
			return data, false
		}
		e.encoded.Add(1)
		return result, true
	case "string":
		result, err := stringDecode(data)
		if err != nil {
			e.errors.Add(1)
			return data, false
		}
		e.encoded.Add(1)
		return result, true
	default:
		return data, false
	}
}

// DecodeBytes is a standalone function for decoding bytes with a named encoding.
// Used by aggregate/sequential for per-backend mixed encodings.
func DecodeBytes(data []byte, encoding string) ([]byte, error) {
	switch encoding {
	case "xml":
		return xmlToJSON(data)
	case "yaml":
		return yamlToJSON(data)
	case "safejson":
		return safeJSONDecode(data)
	case "rss":
		return rssDecode(data)
	case "string":
		return stringDecode(data)
	default:
		return data, nil
	}
}

// safeJSONDecode attempts to parse JSON and normalizes the result to always be an object.
func safeJSONDecode(data []byte) ([]byte, error) {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		// Not valid JSON — wrap raw body as string content
		return json.Marshal(map[string]interface{}{"content": string(data)})
	}
	switch v := raw.(type) {
	case []interface{}:
		return json.Marshal(map[string]interface{}{"collection": v})
	case map[string]interface{}:
		return json.Marshal(v) // already an object
	default:
		// string, number, bool — wrap as content
		return json.Marshal(map[string]interface{}{"content": v})
	}
}

// rssDecode parses RSS/Atom/JSON Feed and converts to JSON.
func rssDecode(data []byte) ([]byte, error) {
	fp := gofeed.NewParser()
	feed, err := fp.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return json.Marshal(feed)
}

// stringDecode wraps a raw response body as a JSON string content.
func stringDecode(data []byte) ([]byte, error) {
	return json.Marshal(map[string]interface{}{"content": string(data)})
}

// Stats returns a snapshot of encoder metrics.
func (e *Encoder) Stats() Snapshot {
	return Snapshot{
		Encoding: e.encoding,
		Encoded:  e.encoded.Load(),
		Errors:   e.errors.Load(),
	}
}

// xmlToJSON converts XML bytes to JSON.
func xmlToJSON(data []byte) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	result, err := decodeXMLElement(decoder)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

// decodeXMLElement recursively decodes XML into a Go structure suitable for JSON.
func decodeXMLElement(decoder *xml.Decoder) (interface{}, error) {
	// Find the root element
	for {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if se, ok := tok.(xml.StartElement); ok {
			return decodeElement(decoder, se)
		}
	}
}

// decodeElement decodes a single XML element and its children.
func decodeElement(decoder *xml.Decoder, start xml.StartElement) (interface{}, error) {
	result := make(map[string]interface{})

	// Add attributes prefixed with @
	for _, attr := range start.Attr {
		result["@"+attr.Name.Local] = inferXMLType(attr.Value)
	}

	var textContent strings.Builder
	children := make(map[string][]interface{})

	for {
		tok, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			child, err := decodeElement(decoder, t)
			if err != nil {
				return nil, err
			}
			children[t.Name.Local] = append(children[t.Name.Local], child)

		case xml.CharData:
			text := strings.TrimSpace(string(t))
			if text != "" {
				textContent.WriteString(text)
			}

		case xml.EndElement:
			goto done
		}
	}

done:
	// If there's only text content and no children/attributes, return the text value
	if len(children) == 0 && len(result) == 0 {
		text := textContent.String()
		if text == "" {
			return "", nil
		}
		return inferXMLType(text), nil
	}

	// Add children to result
	for name, vals := range children {
		if len(vals) == 1 {
			result[name] = vals[0]
		} else {
			result[name] = vals
		}
	}

	// If there's text content alongside children/attributes, store as #text
	if text := textContent.String(); text != "" {
		result["#text"] = inferXMLType(text)
	}

	return result, nil
}

// inferXMLType tries to detect numbers and booleans in XML text values.
func inferXMLType(s string) interface{} {
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// yamlToJSON converts YAML bytes to JSON.
func yamlToJSON(data []byte) ([]byte, error) {
	var parsed interface{}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	// Normalize map types from map[interface{}]interface{} to map[string]interface{}
	normalized := normalizeYAML(parsed)
	return json.Marshal(normalized)
}

// normalizeYAML recursively normalizes YAML parsed values for JSON compatibility.
func normalizeYAML(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(val))
		for k, v := range val {
			result[k] = normalizeYAML(v)
		}
		return result
	case map[interface{}]interface{}:
		result := make(map[string]interface{}, len(val))
		for k, v := range val {
			key, _ := k.(string)
			result[key] = normalizeYAML(v)
		}
		return result
	case []interface{}:
		for i, item := range val {
			val[i] = normalizeYAML(item)
		}
		return val
	default:
		return v
	}
}

// isXML checks if content type indicates XML.
func isXML(contentType string) bool {
	return strings.Contains(contentType, "xml")
}

// isYAML checks if content type indicates YAML.
func isYAML(contentType string) bool {
	return strings.Contains(contentType, "yaml") || strings.Contains(contentType, "x-yaml")
}

// EncoderByRoute manages encoders per route.
type EncoderByRoute struct {
	byroute.Manager[*Encoder]
}

// NewEncoderByRoute creates a new encoder manager.
func NewEncoderByRoute() *EncoderByRoute {
	return &EncoderByRoute{}
}

// AddRoute adds an encoder for a route.
func (br *EncoderByRoute) AddRoute(routeID string, cfg config.BackendEncodingConfig) {
	br.Add(routeID, New(cfg))
}

// GetEncoder returns the encoder for a route.
func (br *EncoderByRoute) GetEncoder(routeID string) *Encoder {
	v, _ := br.Get(routeID)
	return v
}

// Stats returns encoder statistics for all routes.
func (br *EncoderByRoute) Stats() map[string]Snapshot {
	return byroute.CollectStats(&br.Manager, func(e *Encoder) Snapshot { return e.Stats() })
}

// Middleware returns a middleware that decodes XML/YAML backend responses to JSON.
func (enc *Encoder) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &backendEncWriter{
				ResponseWriter: w,
				header:         make(http.Header),
				statusCode:     200,
			}
			next.ServeHTTP(bw, r)

			body := bw.body.Bytes()
			ct := bw.header.Get("Content-Type")

			decoded, ok := enc.Decode(body, ct)
			if ok {
				bw.header.Set("Content-Type", "application/json")
				body = decoded
			}

			for k, vv := range bw.header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(bw.statusCode)
			w.Write(body)
		})
	}
}

type backendEncWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	header     http.Header
}

func (w *backendEncWriter) Header() http.Header { return w.header }
func (w *backendEncWriter) WriteHeader(code int) { w.statusCode = code }
func (w *backendEncWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
