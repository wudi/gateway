package sdk

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Endpoint represents a single API endpoint for template generation.
type Endpoint struct {
	OperationID string
	Method      string
	Path        string
	Summary     string
	HasBody     bool
	PathParams  []Param
	QueryParams []Param
	ResponseType string
}

// Param represents a parameter for an endpoint.
type Param struct {
	Name     string
	Type     string
	Required bool
}

// SpecData holds all data needed for SDK template generation.
type SpecData struct {
	Title       string
	Version     string
	BasePath    string
	PackageName string
	Endpoints   []Endpoint
}

// WalkSpec extracts endpoint data from an OpenAPI spec.
func WalkSpec(doc *openapi3.T) *SpecData {
	data := &SpecData{
		BasePath: "/",
	}
	if doc.Info != nil {
		data.Title = doc.Info.Title
		data.Version = doc.Info.Version
		data.PackageName = toPackageName(doc.Info.Title)
	}
	if data.PackageName == "" {
		data.PackageName = "client"
	}

	if doc.Paths == nil {
		return data
	}

	// Sort paths for deterministic output
	paths := make([]string, 0, doc.Paths.Len())
	for path := range doc.Paths.Map() {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		item := doc.Paths.Find(path)
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			op := item.GetOperation(method)
			if op == nil {
				continue
			}

			ep := Endpoint{
				OperationID: op.OperationID,
				Method:      method,
				Path:        path,
				Summary:     op.Summary,
				HasBody:     op.RequestBody != nil && method != "GET",
			}
			if ep.OperationID == "" {
				ep.OperationID = toOperationID(method, path)
			}

			for _, p := range op.Parameters {
				if p.Value == nil {
					continue
				}
				param := Param{
					Name:     p.Value.Name,
					Type:     paramType(p.Value.Schema),
					Required: p.Value.Required,
				}
				switch p.Value.In {
				case "path":
					ep.PathParams = append(ep.PathParams, param)
				case "query":
					ep.QueryParams = append(ep.QueryParams, param)
				}
			}

			data.Endpoints = append(data.Endpoints, ep)
		}
	}

	return data
}

func toPackageName(title string) string {
	var sb strings.Builder
	for _, c := range strings.ToLower(title) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

func toOperationID(method, path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var sb strings.Builder
	sb.WriteString(strings.ToLower(method))
	for _, p := range parts {
		if strings.HasPrefix(p, "{") {
			p = strings.TrimSuffix(strings.TrimPrefix(p, "{"), "}")
			sb.WriteString("By")
		}
		sb.WriteString(strings.Title(p))
	}
	return sb.String()
}

func paramType(schemaRef *openapi3.SchemaRef) string {
	if schemaRef == nil || schemaRef.Value == nil {
		return "string"
	}
	types := schemaRef.Value.Type.Slice()
	if len(types) == 0 {
		return "string"
	}
	switch types[0] {
	case "integer":
		return "int"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	default:
		return "string"
	}
}

// GoParamType returns a Go-friendly type for a parameter type string.
func GoParamType(t string) string {
	switch t {
	case "int":
		return "int"
	case "float64":
		return "float64"
	case "bool":
		return "bool"
	default:
		return "string"
	}
}

// PythonParamType returns a Python-friendly type for a parameter type string.
func PythonParamType(t string) string {
	switch t {
	case "int":
		return "int"
	case "float64":
		return "float"
	case "bool":
		return "bool"
	default:
		return "str"
	}
}

// TSParamType returns a TypeScript-friendly type for a parameter type string.
func TSParamType(t string) string {
	switch t {
	case "int", "float64":
		return "number"
	case "bool":
		return "boolean"
	default:
		return "string"
	}
}

// FormatGoPath converts /users/{id} to /users/%v for Go fmt.Sprintf.
func FormatGoPath(path string) string {
	result := path
	for {
		start := strings.Index(result, "{")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], "}")
		if end < 0 {
			break
		}
		result = result[:start] + "%v" + result[start+end+1:]
	}
	return result
}

// FormatPythonPath converts /users/{id} to /users/{id} (f-string compatible).
func FormatPythonPath(path string) string {
	return path // Python f-strings use {var} natively
}

// FormatTSPath converts /users/{id} to `/users/${id}` template literal format.
func FormatTSPath(path string) string {
	return strings.ReplaceAll(strings.ReplaceAll(path, "{", "${"), "}", "}")
}

// SprintfArgs returns the Go Sprintf argument list for path params.
func SprintfArgs(params []Param) string {
	if len(params) == 0 {
		return ""
	}
	var args []string
	for _, p := range params {
		args = append(args, p.Name)
	}
	return ", " + strings.Join(args, ", ")
}

// AllParams returns path + query params combined.
func AllParams(ep Endpoint) []Param {
	var all []Param
	all = append(all, ep.PathParams...)
	all = append(all, ep.QueryParams...)
	return all
}

// FuncName creates a function-safe name from an operationID.
func FuncName(opID string) string {
	if opID == "" {
		return "Unknown"
	}
	// Capitalize first letter
	return strings.ToUpper(opID[:1]) + opID[1:]
}

// SnakeCase converts camelCase to snake_case.
func SnakeCase(s string) string {
	var result strings.Builder
	for i, c := range s {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(c - 'A' + 'a')
		} else {
			result.WriteRune(c)
		}
	}
	return result.String()
}

// HasBodyStr returns "true" or "false" for templates.
func HasBodyStr(ep Endpoint) string {
	if ep.HasBody {
		return "true"
	}
	return "false"
}

// SprintfFmt returns the Go Sprintf format string for path params.
func SprintfFmt(path string) string {
	return fmt.Sprintf("%q", FormatGoPath(path))
}
