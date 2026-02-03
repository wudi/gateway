package router

import (
	"strings"
)

// Matcher handles path pattern matching
type Matcher struct {
	pattern    string
	isPrefix   bool
	segments   []segment
	hasParams  bool
}

type segment struct {
	value   string
	isParam bool
}

// NewMatcher creates a new path matcher
func NewMatcher(pattern string, isPrefix bool) *Matcher {
	m := &Matcher{
		pattern:  pattern,
		isPrefix: isPrefix,
	}
	m.compile()
	return m
}

// compile parses the pattern into segments
func (m *Matcher) compile() {
	// Remove leading/trailing slashes for processing
	path := strings.Trim(m.pattern, "/")
	if path == "" {
		m.segments = nil
		return
	}

	parts := strings.Split(path, "/")
	m.segments = make([]segment, len(parts))

	for i, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			// Parameter segment: {name}
			paramName := part[1 : len(part)-1]
			m.segments[i] = segment{value: paramName, isParam: true}
			m.hasParams = true
		} else if strings.HasPrefix(part, ":") {
			// Alternative parameter syntax: :name
			paramName := part[1:]
			m.segments[i] = segment{value: paramName, isParam: true}
			m.hasParams = true
		} else {
			// Literal segment
			m.segments[i] = segment{value: part, isParam: false}
		}
	}
}

// Match checks if a path matches the pattern and extracts parameters
func (m *Matcher) Match(path string) (map[string]string, bool) {
	// Remove leading/trailing slashes for processing
	path = strings.Trim(path, "/")

	// Handle root path
	if m.pattern == "/" {
		if m.isPrefix {
			return make(map[string]string), true
		}
		return make(map[string]string), path == ""
	}

	// Split path into segments
	var pathParts []string
	if path != "" {
		pathParts = strings.Split(path, "/")
	}

	// For prefix matching, path must have at least as many segments
	// For exact matching, path must have exactly the same number of segments
	if m.isPrefix {
		if len(pathParts) < len(m.segments) {
			return nil, false
		}
	} else {
		if len(pathParts) != len(m.segments) {
			return nil, false
		}
	}

	params := make(map[string]string)

	// Match each segment
	for i, seg := range m.segments {
		if seg.isParam {
			// Extract parameter value
			params[seg.value] = pathParts[i]
		} else {
			// Match literal segment
			if pathParts[i] != seg.value {
				return nil, false
			}
		}
	}

	return params, true
}

// Pattern returns the original pattern
func (m *Matcher) Pattern() string {
	return m.pattern
}

// IsPrefix returns whether this is a prefix match
func (m *Matcher) IsPrefix() bool {
	return m.isPrefix
}

// HasParams returns whether the pattern contains parameters
func (m *Matcher) HasParams() bool {
	return m.hasParams
}

// ExtractSuffix returns the path suffix after the matched pattern
func (m *Matcher) ExtractSuffix(path string) string {
	if !m.isPrefix {
		return ""
	}

	path = strings.Trim(path, "/")
	pattern := strings.Trim(m.pattern, "/")

	if pattern == "" {
		return "/" + path
	}

	// Count pattern segments
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	if len(pathParts) <= len(patternParts) {
		return "/"
	}

	// Return remaining path parts
	suffix := strings.Join(pathParts[len(patternParts):], "/")
	if suffix == "" {
		return "/"
	}
	return "/" + suffix
}

// StripPattern removes the pattern prefix from the path
func (m *Matcher) StripPattern(path string) string {
	if !m.isPrefix || m.hasParams {
		// Can't strip if there are parameters
		return path
	}

	pattern := m.pattern
	if !strings.HasSuffix(pattern, "/") {
		pattern += "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Simple prefix strip for patterns without params
	patternBase := strings.Trim(m.pattern, "/")
	pathTrim := strings.Trim(path, "/")

	if strings.HasPrefix(pathTrim, patternBase) {
		remaining := strings.TrimPrefix(pathTrim, patternBase)
		remaining = strings.TrimPrefix(remaining, "/")
		if remaining == "" {
			return "/"
		}
		return "/" + remaining
	}

	return path
}
