package specmock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/wudi/gateway/internal/middleware"
)

// SpecMocker serves mock responses derived from an OpenAPI spec.
type SpecMocker struct {
	doc           *openapi3.T
	defaultStatus int
	gen           *generator
	headers       map[string]string
	served        atomic.Int64
}

// New creates a SpecMocker from an OpenAPI spec document.
func New(doc *openapi3.T, defaultStatus int, seed int64, headers map[string]string) *SpecMocker {
	if defaultStatus == 0 {
		defaultStatus = 200
	}
	return &SpecMocker{
		doc:           doc,
		defaultStatus: defaultStatus,
		gen:           newGenerator(seed),
		headers:       headers,
	}
}

// Middleware returns a middleware that serves mock responses from the spec.
func (s *SpecMocker) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.served.Add(1)

			// Set custom headers
			for k, v := range s.headers {
				w.Header().Set(k, v)
			}

			// Determine desired status from Prefer header
			status := s.defaultStatus
			if prefer := r.Header.Get("Prefer"); prefer != "" {
				if st := parsePreferStatus(prefer); st != 0 {
					status = st
				}
			}

			// Find matching operation
			body, contentType, err := s.generateResponse(r, status)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(map[string]string{"mock": "no matching operation", "error": err.Error()})
				return
			}

			w.Header().Set("Content-Type", contentType)
			w.Header().Set("X-Mock-Source", "openapi-spec")
			w.WriteHeader(status)
			w.Write(body)
		})
	}
}

// Served returns the number of mock responses served.
func (s *SpecMocker) Served() int64 {
	return s.served.Load()
}

// generateResponse finds the matching operation and generates a mock response body.
func (s *SpecMocker) generateResponse(r *http.Request, status int) ([]byte, string, error) {
	// Find matching path + method in spec
	path, method := r.URL.Path, r.Method

	for specPath, pathItem := range s.doc.Paths.Map() {
		if !pathMatches(specPath, path) {
			continue
		}
		op := pathItem.GetOperation(method)
		if op == nil {
			continue
		}

		return s.generateFromOperation(r, op, status)
	}

	return nil, "", fmt.Errorf("no operation found for %s %s", method, path)
}

// generateFromOperation generates a response body from an operation's response schemas.
func (s *SpecMocker) generateFromOperation(r *http.Request, op *openapi3.Operation, status int) ([]byte, string, error) {
	if op.Responses == nil {
		return []byte("{}"), "application/json", nil
	}

	statusStr := strconv.Itoa(status)
	resp := op.Responses.Value(statusStr)
	if resp == nil {
		// Try "default" response
		resp = op.Responses.Value("default")
	}
	if resp == nil {
		// Try any 2xx response
		for code, r := range op.Responses.Map() {
			if strings.HasPrefix(code, "2") {
				resp = r
				break
			}
		}
	}
	if resp == nil {
		return []byte("{}"), "application/json", nil
	}

	// Check for Prefer: example=<name> header
	exampleName := parsePreferExample(r.Header.Get("Prefer"))

	// Find content by Accept header or default to JSON
	content := resp.Value.Content
	if content == nil {
		return []byte("{}"), "application/json", nil
	}

	// Prefer JSON content type
	mediaType := content.Get("application/json")
	contentType := "application/json"
	if mediaType == nil {
		// Try any content type
		for ct, mt := range content {
			mediaType = mt
			contentType = ct
			break
		}
	}
	if mediaType == nil {
		return []byte("{}"), "application/json", nil
	}

	// 1. Check named example
	if exampleName != "" && len(mediaType.Examples) > 0 {
		if ex, ok := mediaType.Examples[exampleName]; ok && ex.Value != nil {
			body, err := json.MarshalIndent(ex.Value.Value, "", "  ")
			return body, contentType, err
		}
	}

	// 2. Use schema example
	if mediaType.Example != nil {
		body, err := json.MarshalIndent(mediaType.Example, "", "  ")
		return body, contentType, err
	}

	// 3. Use first named example
	if len(mediaType.Examples) > 0 {
		for _, ex := range mediaType.Examples {
			if ex.Value != nil && ex.Value.Value != nil {
				body, err := json.MarshalIndent(ex.Value.Value, "", "  ")
				return body, contentType, err
			}
		}
	}

	// 4. Generate from schema
	if mediaType.Schema != nil && mediaType.Schema.Value != nil {
		value := s.gen.generateValue(mediaType.Schema.Value, 0)
		body, err := json.MarshalIndent(value, "", "  ")
		return body, contentType, err
	}

	return []byte("{}"), contentType, nil
}

// pathMatches checks if a request path matches an OpenAPI path template.
func pathMatches(specPath, requestPath string) bool {
	specParts := strings.Split(strings.Trim(specPath, "/"), "/")
	reqParts := strings.Split(strings.Trim(requestPath, "/"), "/")

	if len(specParts) != len(reqParts) {
		return false
	}

	for i, sp := range specParts {
		if strings.HasPrefix(sp, "{") && strings.HasSuffix(sp, "}") {
			continue // path parameter, matches anything
		}
		if sp != reqParts[i] {
			return false
		}
	}
	return true
}

// parsePreferStatus extracts status=NNN from the Prefer header.
func parsePreferStatus(prefer string) int {
	for _, part := range strings.Split(prefer, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "status=") {
			if s, err := strconv.Atoi(strings.TrimPrefix(part, "status=")); err == nil {
				return s
			}
		}
	}
	return 0
}

// parsePreferExample extracts example=<name> from the Prefer header.
func parsePreferExample(prefer string) string {
	for _, part := range strings.Split(prefer, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "example=") {
			return strings.TrimPrefix(part, "example=")
		}
	}
	return ""
}
