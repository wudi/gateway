package rest

import (
	"fmt"
	"strings"

	"github.com/wudi/runway/config"
)

// compiledMapping holds a pre-compiled gRPC-to-REST mapping.
type compiledMapping struct {
	GRPCService string
	GRPCMethod  string
	HTTPMethod  string
	HTTPPath    string // raw template, e.g. "/users/{user_id}"
	Body        string // "*" or ""
	pathParts   []pathPart
}

type pathPart struct {
	literal  string // static text
	variable string // field name from gRPC message (if non-empty, this is a template var)
}

// mappingRegistry stores compiled mappings keyed by gRPC path ("/service/method").
type mappingRegistry struct {
	byGRPCPath map[string]*compiledMapping
}

// newMappingRegistry compiles the config mappings into a lookup table.
func newMappingRegistry(mappings []config.GRPCToRESTMapping) (*mappingRegistry, error) {
	reg := &mappingRegistry{
		byGRPCPath: make(map[string]*compiledMapping, len(mappings)),
	}

	for _, m := range mappings {
		cm := &compiledMapping{
			GRPCService: m.GRPCService,
			GRPCMethod:  m.GRPCMethod,
			HTTPMethod:  m.HTTPMethod,
			HTTPPath:    m.HTTPPath,
			Body:        m.Body,
		}

		parts, err := parsePathTemplate(m.HTTPPath)
		if err != nil {
			return nil, fmt.Errorf("invalid path template %q: %w", m.HTTPPath, err)
		}
		cm.pathParts = parts

		key := "/" + m.GRPCService + "/" + m.GRPCMethod
		reg.byGRPCPath[key] = cm
	}

	return reg, nil
}

// lookup finds a mapping for the given gRPC path (e.g., "/pkg.UserService/GetUser").
func (r *mappingRegistry) lookup(grpcPath string) *compiledMapping {
	return r.byGRPCPath[grpcPath]
}

// buildPath substitutes variables in the path template with values from the message fields.
func (cm *compiledMapping) buildPath(fields map[string]interface{}) string {
	var sb strings.Builder
	for _, p := range cm.pathParts {
		if p.variable != "" {
			if v, ok := fields[p.variable]; ok {
				sb.WriteString(fmt.Sprintf("%v", v))
			}
		} else {
			sb.WriteString(p.literal)
		}
	}
	return sb.String()
}

// variableNames returns the set of field names used as path template variables.
func (cm *compiledMapping) variableNames() []string {
	var names []string
	for _, p := range cm.pathParts {
		if p.variable != "" {
			names = append(names, p.variable)
		}
	}
	return names
}

// parsePathTemplate parses "/users/{user_id}/posts" into parts.
func parsePathTemplate(tmpl string) ([]pathPart, error) {
	var parts []pathPart
	for len(tmpl) > 0 {
		idx := strings.IndexByte(tmpl, '{')
		if idx < 0 {
			parts = append(parts, pathPart{literal: tmpl})
			break
		}
		if idx > 0 {
			parts = append(parts, pathPart{literal: tmpl[:idx]})
		}
		end := strings.IndexByte(tmpl[idx:], '}')
		if end < 0 {
			return nil, fmt.Errorf("unclosed template variable at position %d", idx)
		}
		varName := tmpl[idx+1 : idx+end]
		if varName == "" {
			return nil, fmt.Errorf("empty template variable at position %d", idx)
		}
		parts = append(parts, pathPart{variable: varName})
		tmpl = tmpl[idx+end+1:]
	}
	return parts, nil
}
