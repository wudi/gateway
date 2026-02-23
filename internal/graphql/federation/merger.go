package federation

import (
	"fmt"
	"strings"
)

// MergedSchema holds a merged GraphQL schema from multiple sources.
type MergedSchema struct {
	// Schema holds the merged introspection data.
	Schema *SchemaData

	// FieldOwner maps root type.field → source name.
	// e.g., "Query.users" → "users-service", "Mutation.createOrder" → "orders-service"
	FieldOwner map[string]string
}

// Source represents a backend GraphQL source schema.
type Source struct {
	Name   string
	URL    string
	Schema *SchemaData
}

// MergeSchemas merges root Query/Mutation/Subscription fields from multiple sources.
// Returns error if there are field name conflicts (same root field in multiple sources).
func MergeSchemas(sources []Source) (*MergedSchema, error) {
	if len(sources) < 2 {
		return nil, fmt.Errorf("at least 2 sources required for federation, got %d", len(sources))
	}

	fieldOwner := make(map[string]string)

	// Collect all root fields from all sources
	mergedQueryFields := make([]Field, 0)
	mergedMutationFields := make([]Field, 0)
	mergedSubscriptionFields := make([]Field, 0)

	// Collect all non-root types (merge by name, first-wins for non-root types)
	mergedTypes := make(map[string]FullType)
	mergedDirectives := make(map[string]DirectiveType)

	for _, src := range sources {
		if src.Schema == nil {
			continue
		}

		queryTypeName := "Query"
		mutationTypeName := "Mutation"
		subscriptionTypeName := "Subscription"

		if src.Schema.QueryType != nil {
			queryTypeName = src.Schema.QueryType.Name
		}
		if src.Schema.MutationType != nil {
			mutationTypeName = src.Schema.MutationType.Name
		}
		if src.Schema.SubscriptionType != nil {
			subscriptionTypeName = src.Schema.SubscriptionType.Name
		}

		for _, t := range src.Schema.Types {
			// Skip built-in types
			if strings.HasPrefix(t.Name, "__") {
				continue
			}

			switch t.Name {
			case queryTypeName:
				for _, f := range t.Fields {
					key := "Query." + f.Name
					if existing, ok := fieldOwner[key]; ok {
						return nil, fmt.Errorf("field conflict: Query.%s defined in both %q and %q", f.Name, existing, src.Name)
					}
					fieldOwner[key] = src.Name
					mergedQueryFields = append(mergedQueryFields, f)
				}

			case mutationTypeName:
				for _, f := range t.Fields {
					key := "Mutation." + f.Name
					if existing, ok := fieldOwner[key]; ok {
						return nil, fmt.Errorf("field conflict: Mutation.%s defined in both %q and %q", f.Name, existing, src.Name)
					}
					fieldOwner[key] = src.Name
					mergedMutationFields = append(mergedMutationFields, f)
				}

			case subscriptionTypeName:
				for _, f := range t.Fields {
					key := "Subscription." + f.Name
					if existing, ok := fieldOwner[key]; ok {
						return nil, fmt.Errorf("field conflict: Subscription.%s defined in both %q and %q", f.Name, existing, src.Name)
					}
					fieldOwner[key] = src.Name
					mergedSubscriptionFields = append(mergedSubscriptionFields, f)
				}

			default:
				// Non-root type: first source wins
				if _, exists := mergedTypes[t.Name]; !exists {
					mergedTypes[t.Name] = t
				}
			}
		}

		for _, d := range src.Schema.Directives {
			if _, exists := mergedDirectives[d.Name]; !exists {
				mergedDirectives[d.Name] = d
			}
		}
	}

	// Build merged types list
	var allTypes []FullType

	// Add Query type
	if len(mergedQueryFields) > 0 {
		allTypes = append(allTypes, FullType{
			Kind:   "OBJECT",
			Name:   "Query",
			Fields: mergedQueryFields,
		})
	}

	// Add Mutation type
	if len(mergedMutationFields) > 0 {
		allTypes = append(allTypes, FullType{
			Kind:   "OBJECT",
			Name:   "Mutation",
			Fields: mergedMutationFields,
		})
	}

	// Add Subscription type
	if len(mergedSubscriptionFields) > 0 {
		allTypes = append(allTypes, FullType{
			Kind:   "OBJECT",
			Name:   "Subscription",
			Fields: mergedSubscriptionFields,
		})
	}

	// Add all non-root types
	for _, t := range mergedTypes {
		allTypes = append(allTypes, t)
	}

	// Build directives list
	var directives []DirectiveType
	for _, d := range mergedDirectives {
		directives = append(directives, d)
	}

	schema := &SchemaData{
		QueryType:  &TypeName{Name: "Query"},
		Types:      allTypes,
		Directives: directives,
	}
	if len(mergedMutationFields) > 0 {
		schema.MutationType = &TypeName{Name: "Mutation"}
	}
	if len(mergedSubscriptionFields) > 0 {
		schema.SubscriptionType = &TypeName{Name: "Subscription"}
	}

	return &MergedSchema{
		Schema:     schema,
		FieldOwner: fieldOwner,
	}, nil
}
