package transform

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/example/gateway/internal/middleware"
	"github.com/example/gateway/internal/variables"
)

// BodyTransformer transforms request/response bodies
type BodyTransformer struct {
	resolver *variables.Resolver
}

// NewBodyTransformer creates a new body transformer
func NewBodyTransformer() *BodyTransformer {
	return &BodyTransformer{
		resolver: variables.NewResolver(),
	}
}

// JSONFieldAdd adds fields to a JSON body
type JSONFieldAdd struct {
	Path  string // JSON path (e.g., "metadata.request_id")
	Value string // Value template with variables
}

// JSONBodyTransformConfig configures JSON body transformation
type JSONBodyTransformConfig struct {
	AddFields    []JSONFieldAdd
	RemoveFields []string
}

// TransformJSONRequest transforms a JSON request body
func (t *BodyTransformer) TransformJSONRequest(r *http.Request, cfg JSONBodyTransformConfig, varCtx *variables.Context) error {
	if r.Body == nil {
		return nil
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body.Close()

	// Parse JSON
	var data map[string]interface{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &data); err != nil {
			// Not valid JSON, restore original body
			r.Body = io.NopCloser(bytes.NewReader(body))
			return nil
		}
	} else {
		data = make(map[string]interface{})
	}

	// Add fields
	for _, field := range cfg.AddFields {
		value := t.resolver.Resolve(field.Value, varCtx)
		setNestedField(data, field.Path, value)
	}

	// Remove fields
	for _, path := range cfg.RemoveFields {
		deleteNestedField(data, path)
	}

	// Marshal back to JSON
	newBody, err := json.Marshal(data)
	if err != nil {
		return err
	}

	r.Body = io.NopCloser(bytes.NewReader(newBody))
	r.ContentLength = int64(len(newBody))
	r.Header.Set("Content-Length", string(rune(len(newBody))))

	return nil
}

// setNestedField sets a field in a nested map using dot notation
func setNestedField(data map[string]interface{}, path string, value interface{}) {
	keys := splitPath(path)
	current := data

	for i, key := range keys {
		if i == len(keys)-1 {
			current[key] = value
			return
		}

		if next, ok := current[key].(map[string]interface{}); ok {
			current = next
		} else {
			newMap := make(map[string]interface{})
			current[key] = newMap
			current = newMap
		}
	}
}

// deleteNestedField deletes a field from a nested map using dot notation
func deleteNestedField(data map[string]interface{}, path string) {
	keys := splitPath(path)
	current := data

	for i, key := range keys {
		if i == len(keys)-1 {
			delete(current, key)
			return
		}

		if next, ok := current[key].(map[string]interface{}); ok {
			current = next
		} else {
			return // Path doesn't exist
		}
	}
}

// splitPath splits a dot-notation path into keys
func splitPath(path string) []string {
	var keys []string
	var current string

	for _, c := range path {
		if c == '.' {
			if current != "" {
				keys = append(keys, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}

	if current != "" {
		keys = append(keys, current)
	}

	return keys
}

// JSONRequestTransformMiddleware creates a middleware for JSON request body transformation
func JSONRequestTransformMiddleware(cfg JSONBodyTransformConfig) middleware.Middleware {
	transformer := NewBodyTransformer()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only transform JSON requests
			contentType := r.Header.Get("Content-Type")
			if contentType == "application/json" || contentType == "application/json; charset=utf-8" {
				varCtx := variables.GetFromRequest(r)
				transformer.TransformJSONRequest(r, cfg, varCtx)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// bodyBufferWriter buffers the response body for transformation
type bodyBufferWriter struct {
	http.ResponseWriter
	body       bytes.Buffer
	statusCode int
}

func (w *bodyBufferWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *bodyBufferWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

// JSONResponseTransformMiddleware creates a middleware for JSON response body transformation
func JSONResponseTransformMiddleware(cfg JSONBodyTransformConfig) middleware.Middleware {
	transformer := NewBodyTransformer()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Buffer the response
			bw := &bodyBufferWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(bw, r)

			// Check if response is JSON
			contentType := w.Header().Get("Content-Type")
			body := bw.body.Bytes()

			if (contentType == "application/json" || contentType == "application/json; charset=utf-8") && len(body) > 0 {
				// Transform JSON response
				var data map[string]interface{}
				if err := json.Unmarshal(body, &data); err == nil {
					varCtx := variables.GetFromRequest(r)

					// Add fields
					for _, field := range cfg.AddFields {
						value := transformer.resolver.Resolve(field.Value, varCtx)
						setNestedField(data, field.Path, value)
					}

					// Remove fields
					for _, path := range cfg.RemoveFields {
						deleteNestedField(data, path)
					}

					// Marshal back
					if newBody, err := json.Marshal(data); err == nil {
						body = newBody
					}
				}
			}

			// Write the response
			w.WriteHeader(bw.statusCode)
			w.Write(body)
		})
	}
}
