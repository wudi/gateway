package variables

import (
	"regexp"
	"strings"
)

// varPattern matches $variable_name
var varPattern = regexp.MustCompile(`\$([a-zA-Z_][a-zA-Z0-9_]*)`)

// Parser handles variable extraction from strings
type Parser struct{}

// NewParser creates a new variable parser
func NewParser() *Parser {
	return &Parser{}
}

// Extract returns all variable names from a template string
func (p *Parser) Extract(template string) []string {
	matches := varPattern.FindAllStringSubmatch(template, -1)
	if matches == nil {
		return nil
	}

	seen := make(map[string]bool)
	var vars []string

	for _, match := range matches {
		if len(match) > 1 && !seen[match[1]] {
			seen[match[1]] = true
			vars = append(vars, match[1])
		}
	}

	return vars
}

// HasVariables returns true if the template contains variables
func (p *Parser) HasVariables(template string) bool {
	return varPattern.MatchString(template)
}

// Replace replaces all variables in the template with their values
func (p *Parser) Replace(template string, getValue func(name string) string) string {
	return varPattern.ReplaceAllStringFunc(template, func(match string) string {
		// Extract variable name (without $)
		name := match[1:]
		return getValue(name)
	})
}

// ParseDynamic extracts dynamic variable parts
// e.g., "http_x_custom_header" returns ("http", "x_custom_header")
// e.g., "arg_page" returns ("arg", "page")
func ParseDynamic(name string) (prefix, suffix string, ok bool) {
	dynamicPrefixes := []string{
		"http_",
		"arg_",
		"cookie_",
		"route_param_",
		"jwt_claim_",
	}

	for _, p := range dynamicPrefixes {
		if strings.HasPrefix(name, p) {
			return p[:len(p)-1], name[len(p):], true
		}
	}

	return "", "", false
}

// NormalizeHeaderName converts http_x_custom_header to X-Custom-Header
func NormalizeHeaderName(name string) string {
	// Replace underscores with hyphens
	name = strings.ReplaceAll(name, "_", "-")

	// Capitalize each word
	parts := strings.Split(name, "-")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(string(part[0])) + strings.ToLower(part[1:])
		}
	}

	return strings.Join(parts, "-")
}

// Template represents a parsed template with variables
type Template struct {
	Raw      string
	Parts    []TemplatePart
	HasVars  bool
}

// TemplatePart represents either literal text or a variable
type TemplatePart struct {
	IsVariable bool
	Value      string // literal text or variable name
}

// ParseTemplate parses a template string into parts
func ParseTemplate(template string) *Template {
	t := &Template{
		Raw:   template,
		Parts: make([]TemplatePart, 0),
	}

	indices := varPattern.FindAllStringSubmatchIndex(template, -1)
	if len(indices) == 0 {
		t.Parts = append(t.Parts, TemplatePart{IsVariable: false, Value: template})
		return t
	}

	t.HasVars = true
	lastEnd := 0

	for _, loc := range indices {
		// Add literal text before this variable
		if loc[0] > lastEnd {
			t.Parts = append(t.Parts, TemplatePart{
				IsVariable: false,
				Value:      template[lastEnd:loc[0]],
			})
		}

		// Add variable (group 1 contains the name without $)
		t.Parts = append(t.Parts, TemplatePart{
			IsVariable: true,
			Value:      template[loc[2]:loc[3]],
		})

		lastEnd = loc[1]
	}

	// Add remaining literal text
	if lastEnd < len(template) {
		t.Parts = append(t.Parts, TemplatePart{
			IsVariable: false,
			Value:      template[lastEnd:],
		})
	}

	return t
}

// Render renders the template with the given value function
func (t *Template) Render(getValue func(name string) string) string {
	if !t.HasVars {
		return t.Raw
	}

	var builder strings.Builder
	for _, part := range t.Parts {
		if part.IsVariable {
			builder.WriteString(getValue(part.Value))
		} else {
			builder.WriteString(part.Value)
		}
	}
	return builder.String()
}
