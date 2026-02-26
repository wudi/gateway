package grpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/wudi/runway/config"
)

// restMapper handles REST-to-gRPC method mapping.
type restMapper struct {
	service  string
	mappings []compiledMapping
}

// compiledMapping is a pre-compiled mapping rule.
type compiledMapping struct {
	httpMethod string
	pattern    *regexp.Regexp
	paramNames []string
	grpcMethod string
	body       string // "*" = whole body, "field" = nested, "" = no body
}

// matchResult contains the matched mapping and extracted parameters.
type matchResult struct {
	grpcMethod string
	pathParams map[string]string
	body       string
}

// newRESTMapper creates a new REST mapper from config.
func newRESTMapper(service string, mappings []config.GRPCMethodMapping) (*restMapper, error) {
	if len(mappings) == 0 {
		return nil, nil
	}

	compiled := make([]compiledMapping, 0, len(mappings))
	for _, m := range mappings {
		pattern, paramNames, err := compilePathPattern(m.HTTPPath)
		if err != nil {
			return nil, fmt.Errorf("invalid path pattern %q: %w", m.HTTPPath, err)
		}

		compiled = append(compiled, compiledMapping{
			httpMethod: m.HTTPMethod,
			pattern:    pattern,
			paramNames: paramNames,
			grpcMethod: m.GRPCMethod,
			body:       m.Body,
		})
	}

	return &restMapper{
		service:  service,
		mappings: compiled,
	}, nil
}

// compilePathPattern converts a path pattern like /users/:user_id or /users/{user_id}
// into a regex and extracts parameter names.
func compilePathPattern(pattern string) (*regexp.Regexp, []string, error) {
	var paramNames []string
	regexStr := "^"

	// Handle both :param and {param} styles
	parts := strings.Split(pattern, "/")
	for i, part := range parts {
		if i > 0 {
			regexStr += "/"
		}
		if part == "" {
			continue
		}

		if strings.HasPrefix(part, ":") {
			// :param style
			paramName := strings.TrimPrefix(part, ":")
			paramNames = append(paramNames, paramName)
			regexStr += `([^/]+)`
		} else if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			// {param} style
			paramName := strings.TrimPrefix(strings.TrimSuffix(part, "}"), "{")
			paramNames = append(paramNames, paramName)
			regexStr += `([^/]+)`
		} else {
			// Literal part - escape regex special chars
			regexStr += regexp.QuoteMeta(part)
		}
	}
	regexStr += "$"

	re, err := regexp.Compile(regexStr)
	if err != nil {
		return nil, nil, err
	}

	return re, paramNames, nil
}

// match tries to match a request against the configured mappings.
// Returns nil if no mapping matches.
func (m *restMapper) match(method, path string) *matchResult {
	// Strip any trailing slash for consistent matching
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}

	for _, mapping := range m.mappings {
		if mapping.httpMethod != method {
			continue
		}

		matches := mapping.pattern.FindStringSubmatch(path)
		if matches == nil {
			continue
		}

		// Extract path parameters
		params := make(map[string]string)
		for i, name := range mapping.paramNames {
			if i+1 < len(matches) {
				params[name] = matches[i+1]
			}
		}

		return &matchResult{
			grpcMethod: mapping.grpcMethod,
			pathParams: params,
			body:       mapping.body,
		}
	}

	return nil
}

// buildRequestBody builds the gRPC request body from path params, query params, and request body.
func (m *restMapper) buildRequestBody(r *http.Request, result *matchResult, requestBody []byte) ([]byte, error) {
	data := make(map[string]interface{})

	// 1. Add path parameters
	for k, v := range result.pathParams {
		setNestedField(data, k, v)
	}

	// 2. Add query parameters (for GET requests or as additional params)
	for key, values := range r.URL.Query() {
		if len(values) == 1 {
			setNestedField(data, key, values[0])
		} else {
			setNestedField(data, key, values)
		}
	}

	// 3. Handle request body
	if len(requestBody) > 0 && result.body != "" {
		var bodyData interface{}
		if err := json.Unmarshal(requestBody, &bodyData); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}

		if result.body == "*" {
			// Merge body fields into root
			if bodyMap, ok := bodyData.(map[string]interface{}); ok {
				for k, v := range bodyMap {
					data[k] = v
				}
			} else {
				return nil, fmt.Errorf("body must be a JSON object when using '*'")
			}
		} else {
			// Nest body under specified field
			setNestedField(data, result.body, bodyData)
		}
	}

	return json.Marshal(data)
}

// setNestedField sets a value in a nested map structure.
// Supports dot notation: "user.name" sets data["user"]["name"].
func setNestedField(data map[string]interface{}, key string, value interface{}) {
	parts := strings.Split(key, ".")
	current := data

	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
		} else {
			if _, ok := current[part]; !ok {
				current[part] = make(map[string]interface{})
			}
			if next, ok := current[part].(map[string]interface{}); ok {
				current = next
			} else {
				// Can't traverse further, overwrite
				next := make(map[string]interface{})
				current[part] = next
				current = next
			}
		}
	}
}

// serviceName returns the configured service name.
func (m *restMapper) serviceName() string {
	return m.service
}
