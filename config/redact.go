package config

import (
	"fmt"
	"reflect"

	"github.com/goccy/go-yaml"
)

// RedactedValue is the placeholder string used for redacted secrets.
const RedactedValue = "[REDACTED]"

// RedactConfig returns a deep copy of cfg with all string fields tagged
// `redact:"true"` replaced by RedactedValue. The original cfg is not mutated.
func RedactConfig(cfg *Config) (*Config, error) {
	// Deep copy via YAML round-trip. Fields tagged yaml:"-" (e.g.,
	// TLSCertPair.CertData/KeyData) are intentionally dropped — they
	// contain raw cert bytes that should not appear in admin output.
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("redact: marshal failed: %w", err)
	}
	var cp Config
	if err := yaml.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("redact: unmarshal failed: %w", err)
	}
	redactFields(reflect.ValueOf(&cp).Elem())
	return &cp, nil
}

// redactFields walks a struct value and sets every non-empty string field
// tagged `redact:"true"` to RedactedValue.
func redactFields(v reflect.Value) {
	walkStructStrings(v, "", func(field reflect.Value, _ string, tag reflect.StructTag) {
		if tag.Get("redact") == "true" && field.String() != "" {
			field.SetString(RedactedValue)
		}
	})
}
