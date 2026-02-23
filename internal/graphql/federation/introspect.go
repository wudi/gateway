package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// introspectionQuery is the standard GraphQL introspection query.
const introspectionQuery = `{
  __schema {
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types {
      kind name description
      fields(includeDeprecated: true) {
        name description isDeprecated deprecationReason
        args { name description type { ...TypeRef } defaultValue }
        type { ...TypeRef }
      }
      inputFields { name description type { ...TypeRef } defaultValue }
      interfaces { ...TypeRef }
      enumValues(includeDeprecated: true) { name description isDeprecated deprecationReason }
      possibleTypes { ...TypeRef }
    }
    directives {
      name description locations
      args { name description type { ...TypeRef } defaultValue }
    }
  }
}

fragment TypeRef on __Type {
  kind name
  ofType {
    kind name
    ofType {
      kind name
      ofType {
        kind name
        ofType {
          kind name
          ofType {
            kind name
            ofType { kind name }
          }
        }
      }
    }
  }
}`

// IntrospectionResult holds the raw introspection response.
type IntrospectionResult struct {
	Data struct {
		Schema SchemaData `json:"__schema"`
	} `json:"data"`
}

// SchemaData represents the GraphQL introspection schema.
type SchemaData struct {
	QueryType        *TypeName       `json:"queryType"`
	MutationType     *TypeName       `json:"mutationType"`
	SubscriptionType *TypeName       `json:"subscriptionType"`
	Types            []FullType      `json:"types"`
	Directives       []DirectiveType `json:"directives"`
}

// TypeName holds a type name reference.
type TypeName struct {
	Name string `json:"name"`
}

// FullType represents a complete GraphQL type.
type FullType struct {
	Kind          string       `json:"kind"`
	Name          string       `json:"name"`
	Description   string       `json:"description,omitempty"`
	Fields        []Field      `json:"fields,omitempty"`
	InputFields   []InputValue `json:"inputFields,omitempty"`
	Interfaces    []TypeRef    `json:"interfaces,omitempty"`
	EnumValues    []EnumValue  `json:"enumValues,omitempty"`
	PossibleTypes []TypeRef    `json:"possibleTypes,omitempty"`
}

// Field represents a GraphQL field.
type Field struct {
	Name              string       `json:"name"`
	Description       string       `json:"description,omitempty"`
	Args              []InputValue `json:"args,omitempty"`
	Type              TypeRef      `json:"type"`
	IsDeprecated      bool         `json:"isDeprecated"`
	DeprecationReason string       `json:"deprecationReason,omitempty"`
}

// InputValue represents a GraphQL input value (argument or input field).
type InputValue struct {
	Name         string  `json:"name"`
	Description  string  `json:"description,omitempty"`
	Type         TypeRef `json:"type"`
	DefaultValue *string `json:"defaultValue,omitempty"`
}

// TypeRef represents a reference to a GraphQL type (with nesting for NON_NULL, LIST).
type TypeRef struct {
	Kind   string   `json:"kind"`
	Name   *string  `json:"name,omitempty"`
	OfType *TypeRef `json:"ofType,omitempty"`
}

// EnumValue represents a GraphQL enum value.
type EnumValue struct {
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
	IsDeprecated      bool   `json:"isDeprecated"`
	DeprecationReason string `json:"deprecationReason,omitempty"`
}

// DirectiveType represents a GraphQL directive.
type DirectiveType struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Locations   []string     `json:"locations"`
	Args        []InputValue `json:"args,omitempty"`
}

// IntrospectSchema sends the introspection query to a backend and returns the schema data.
func IntrospectSchema(ctx context.Context, url string, transport http.RoundTripper) (*SchemaData, error) {
	reqBody, err := json.Marshal(map[string]string{"query": introspectionQuery})
	if err != nil {
		return nil, fmt.Errorf("marshal introspection query: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Transport: transport}
	if transport == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("introspect %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read introspection response from %s: %w", url, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("introspect %s: status %d", url, resp.StatusCode)
	}

	var result IntrospectionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal introspection response from %s: %w", url, err)
	}

	return &result.Data.Schema, nil
}
