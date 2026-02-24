package specmock

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// generator produces values from OpenAPI schema definitions.
type generator struct {
	rng *rand.Rand
}

func newGenerator(seed int64) *generator {
	var rng *rand.Rand
	if seed != 0 {
		rng = rand.New(rand.NewSource(seed))
	} else {
		rng = rand.New(rand.NewSource(rand.Int63()))
	}
	return &generator{rng: rng}
}

// generateValue produces a value from a schema.
func (g *generator) generateValue(schema *openapi3.Schema, depth int) any {
	if depth > 10 {
		return nil
	}

	// Check for explicit example first
	if schema.Example != nil {
		return schema.Example
	}

	// Check for enum
	if len(schema.Enum) > 0 {
		return schema.Enum[g.rng.Intn(len(schema.Enum))]
	}

	// Check for default
	if schema.Default != nil {
		return schema.Default
	}

	switch schema.Type.Slice()[0] {
	case "string":
		return g.generateString(schema)
	case "integer":
		return g.generateInteger(schema)
	case "number":
		return g.generateNumber(schema)
	case "boolean":
		return g.rng.Intn(2) == 1
	case "array":
		return g.generateArray(schema, depth)
	case "object":
		return g.generateObject(schema, depth)
	}

	// Fallback for untyped schemas with properties
	if len(schema.Properties) > 0 {
		return g.generateObject(schema, depth)
	}

	return nil
}

func (g *generator) generateString(schema *openapi3.Schema) any {
	format := schema.Format
	switch format {
	case "date-time":
		return "2024-01-15T09:30:00Z"
	case "date":
		return "2024-01-15"
	case "time":
		return "09:30:00Z"
	case "email":
		return "user@example.com"
	case "uri", "url":
		return "https://example.com/resource"
	case "uuid":
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			g.rng.Uint32(), g.rng.Uint32()&0xffff, g.rng.Uint32()&0xffff,
			g.rng.Uint32()&0xffff, g.rng.Int63()&0xffffffffffff)
	case "ipv4":
		return fmt.Sprintf("%d.%d.%d.%d", g.rng.Intn(256), g.rng.Intn(256), g.rng.Intn(256), g.rng.Intn(256))
	case "ipv6":
		return "2001:db8::1"
	case "hostname":
		return "api.example.com"
	case "byte":
		return "dGVzdA=="
	case "binary":
		return "binary-data"
	case "password":
		return "********"
	}

	// Use pattern hint or generate based on min/max length
	minLen := 5
	maxLen := 20
	if schema.MinLength > 0 {
		minLen = int(schema.MinLength)
	}
	if schema.MaxLength != nil && *schema.MaxLength > 0 {
		maxLen = int(*schema.MaxLength)
	}
	if minLen > maxLen {
		maxLen = minLen
	}

	length := minLen + g.rng.Intn(maxLen-minLen+1)
	return g.randomString(length)
}

func (g *generator) randomString(length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz"
	var sb strings.Builder
	sb.Grow(length)
	for i := 0; i < length; i++ {
		sb.WriteByte(chars[g.rng.Intn(len(chars))])
	}
	return sb.String()
}

func (g *generator) generateInteger(schema *openapi3.Schema) any {
	min := int64(1)
	max := int64(1000)
	if schema.Min != nil {
		min = int64(*schema.Min)
	}
	if schema.Max != nil {
		max = int64(*schema.Max)
	}
	if min > max {
		max = min
	}
	return min + g.rng.Int63n(max-min+1)
}

func (g *generator) generateNumber(schema *openapi3.Schema) any {
	min := 0.0
	max := 1000.0
	if schema.Min != nil {
		min = *schema.Min
	}
	if schema.Max != nil {
		max = *schema.Max
	}
	return min + g.rng.Float64()*(max-min)
}

func (g *generator) generateArray(schema *openapi3.Schema, depth int) any {
	count := 2
	if schema.MinItems > 0 {
		count = int(schema.MinItems)
	}
	if schema.MaxItems != nil && int(*schema.MaxItems) < count {
		count = int(*schema.MaxItems)
	}
	if count == 0 {
		count = 2
	}

	items := make([]any, count)
	if schema.Items != nil && schema.Items.Value != nil {
		for i := range items {
			items[i] = g.generateValue(schema.Items.Value, depth+1)
		}
	}
	return items
}

func (g *generator) generateObject(schema *openapi3.Schema, depth int) any {
	result := make(map[string]any)
	for name, propRef := range schema.Properties {
		if propRef.Value != nil {
			result[name] = g.generateValue(propRef.Value, depth+1)
		}
	}
	return result
}
