package runway

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// ParseExtension unmarshals a named extension's raw YAML bytes into a typed struct.
// Returns a zero value and error if the extension is not found or cannot be decoded.
func ParseExtension[T any](extensions map[string]yaml.RawMessage, name string) (T, error) {
	var zero T
	raw, ok := extensions[name]
	if !ok {
		return zero, fmt.Errorf("extension %q not found", name)
	}
	var result T
	if err := yaml.Unmarshal(raw, &result); err != nil {
		return zero, fmt.Errorf("extension %q: %w", name, err)
	}
	return result, nil
}

// HasExtension checks whether a named extension exists in the map.
func HasExtension(extensions map[string]yaml.RawMessage, name string) bool {
	_, ok := extensions[name]
	return ok
}
