package openapi

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/wudi/runway/config"
)

// pathParamRegex matches OpenAPI path parameters like {user_id}.
var pathParamRegex = regexp.MustCompile(`\{([^}]+)\}`)

// GenerateRoutes auto-generates route configs from an OpenAPI spec.
func GenerateRoutes(specCfg config.OpenAPISpecConfig) ([]config.RouteConfig, error) {
	doc, err := LoadSpec(specCfg.File)
	if err != nil {
		return nil, err
	}

	if doc.Paths == nil {
		return nil, nil
	}

	validateReq := true
	if specCfg.Validation.Request != nil {
		validateReq = *specCfg.Validation.Request
	}

	var routes []config.RouteConfig

	for path, pathItem := range doc.Paths.Map() {
		for method, op := range pathItem.Operations() {
			routeID := generateRouteID(method, path, op.OperationID)

			// Build the full runway path
			gwPath := specCfg.RoutePrefix + convertOpenAPIPath(path)

			valReqPtr := &validateReq
			routeCfg := config.RouteConfig{
				ID:          routeID,
				Path:        gwPath,
				PathPrefix:  hasPathParams(path),
				Methods:     []string{strings.ToUpper(method)},
				Backends:    specCfg.DefaultBackends,
				StripPrefix: specCfg.StripPrefix,
				OpenAPI: config.OpenAPIRouteConfig{
					SpecFile:         specCfg.File,
					SpecID:           specCfg.ID,
					OperationID:      op.OperationID,
					ValidateRequest:  valReqPtr,
					ValidateResponse: specCfg.Validation.Response,
					LogOnly:          specCfg.Validation.LogOnly,
				},
			}

			routes = append(routes, routeCfg)
		}
	}

	return routes, nil
}

// generateRouteID creates a route ID from the operation.
func generateRouteID(method, path, operationID string) string {
	if operationID != "" {
		return "openapi-" + operationID
	}
	// Sanitize path for use as an ID
	sanitized := strings.NewReplacer(
		"/", "-",
		"{", "",
		"}", "",
	).Replace(path)
	sanitized = strings.Trim(sanitized, "-")
	return fmt.Sprintf("openapi-%s-%s", strings.ToLower(method), sanitized)
}

// convertOpenAPIPath converts OpenAPI path params {id} to runway path params :id.
func convertOpenAPIPath(path string) string {
	return pathParamRegex.ReplaceAllString(path, ":$1")
}

// hasPathParams returns true if the path has path parameters (needs path_prefix).
func hasPathParams(path string) bool {
	return strings.Contains(path, "{")
}
