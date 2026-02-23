package federation

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wundergraph/graphql-go-tools/v2/pkg/ast"
	"github.com/wundergraph/graphql-go-tools/v2/pkg/astparser"
)

// SubQuery represents a query fragment destined for a specific backend source.
type SubQuery struct {
	SourceName string
	Query      string
	Variables  json.RawMessage
}

// SplitQuery splits a GraphQL query into per-backend sub-queries based on field ownership.
// If all root fields belong to a single source, the original query is forwarded as-is.
// Otherwise, sub-queries are built per source containing only that source's fields.
func SplitQuery(query string, operationName string, variables json.RawMessage, fieldOwner map[string]string) ([]SubQuery, error) {
	doc, report := astparser.ParseGraphqlDocumentString(query)
	if report.HasErrors() {
		return nil, fmt.Errorf("parse query: %s", report.Error())
	}

	opIdx := findOperation(&doc, operationName)
	if opIdx == -1 {
		return nil, fmt.Errorf("operation not found: %q", operationName)
	}

	opDef := doc.OperationDefinitions[opIdx]
	opType := operationTypeName(opDef.OperationType)

	if !opDef.HasSelections {
		return nil, nil
	}

	// Group top-level fields by source
	type fieldInfo struct {
		name string
	}
	sourceFields := make(map[string][]fieldInfo)
	var fieldOrder []string // preserve order

	selSet := doc.SelectionSets[opDef.SelectionSet]
	for _, selRef := range selSet.SelectionRefs {
		sel := doc.Selections[selRef]
		if sel.Kind != ast.SelectionKindField {
			continue
		}
		fieldName := doc.FieldNameString(sel.Ref)
		key := opType + "." + fieldName
		source, ok := fieldOwner[key]
		if !ok {
			return nil, fmt.Errorf("field %s.%s not found in any source", opType, fieldName)
		}
		if _, seen := sourceFields[source]; !seen {
			fieldOrder = append(fieldOrder, source)
		}
		sourceFields[source] = append(sourceFields[source], fieldInfo{name: fieldName})
	}

	// If all fields belong to one source, forward the original query as-is
	if len(sourceFields) == 1 {
		for source := range sourceFields {
			return []SubQuery{{
				SourceName: source,
				Query:      query,
				Variables:  variables,
			}}, nil
		}
	}

	// Build per-source sub-queries by extracting field blocks from the original text
	fieldBlocks := extractFieldBlocks(query)

	var subQueries []SubQuery
	for _, source := range fieldOrder {
		fields := sourceFields[source]
		var fieldTexts []string
		for _, f := range fields {
			if block, ok := fieldBlocks[f.name]; ok {
				fieldTexts = append(fieldTexts, block)
			} else {
				fieldTexts = append(fieldTexts, f.name)
			}
		}

		subQuery := buildSubQueryText(opType, operationName, fieldTexts)
		subQueries = append(subQueries, SubQuery{
			SourceName: source,
			Query:      subQuery,
			Variables:  variables,
		})
	}

	return subQueries, nil
}

// findOperation finds the operation definition index by name.
func findOperation(doc *ast.Document, operationName string) int {
	for i := range doc.OperationDefinitions {
		if operationName == "" || string(doc.OperationDefinitionNameString(i)) == operationName {
			return i
		}
	}
	return -1
}

// operationTypeName returns the string name for an operation type.
func operationTypeName(opType ast.OperationType) string {
	switch opType {
	case ast.OperationTypeMutation:
		return "Mutation"
	case ast.OperationTypeSubscription:
		return "Subscription"
	default:
		return "Query"
	}
}

// extractFieldBlocks does a simple text extraction of top-level field blocks
// from the first { ... } body of the query.
func extractFieldBlocks(query string) map[string]string {
	blocks := make(map[string]string)

	// Find the first opening brace
	start := strings.IndexByte(query, '{')
	if start == -1 {
		return blocks
	}

	body := query[start+1:]
	// Find matching closing brace
	depth := 1
	end := 0
	for i, c := range body {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
				goto done
			}
		}
	}
done:
	if end > 0 {
		body = body[:end]
	}

	// Parse individual field blocks
	body = strings.TrimSpace(body)
	i := 0
	for i < len(body) {
		// Skip whitespace
		for i < len(body) && (body[i] == ' ' || body[i] == '\n' || body[i] == '\r' || body[i] == '\t' || body[i] == ',') {
			i++
		}
		if i >= len(body) {
			break
		}

		// Read field name
		nameStart := i
		for i < len(body) && body[i] != '{' && body[i] != ' ' && body[i] != '\n' && body[i] != '(' && body[i] != '}' {
			i++
		}
		fieldName := strings.TrimSpace(body[nameStart:i])
		if fieldName == "" {
			break
		}

		// Check if there's arguments or a selection set
		blockStart := nameStart
		// Skip args (parentheses)
		for i < len(body) && (body[i] == ' ' || body[i] == '\n' || body[i] == '\r' || body[i] == '\t') {
			i++
		}
		if i < len(body) && body[i] == '(' {
			parenDepth := 1
			i++
			for i < len(body) && parenDepth > 0 {
				if body[i] == '(' {
					parenDepth++
				} else if body[i] == ')' {
					parenDepth--
				}
				i++
			}
		}

		// Skip whitespace
		for i < len(body) && (body[i] == ' ' || body[i] == '\n' || body[i] == '\r' || body[i] == '\t') {
			i++
		}

		// Check for selection set
		if i < len(body) && body[i] == '{' {
			braceDepth := 1
			i++
			for i < len(body) && braceDepth > 0 {
				if body[i] == '{' {
					braceDepth++
				} else if body[i] == '}' {
					braceDepth--
				}
				i++
			}
		}

		block := strings.TrimSpace(body[blockStart:i])
		if block != "" {
			blocks[fieldName] = block
		}
	}

	return blocks
}

// buildSubQueryText constructs a sub-query string from an operation type and field texts.
func buildSubQueryText(opType, operationName string, fieldTexts []string) string {
	body := strings.Join(fieldTexts, "\n  ")
	if opType == "Query" && operationName == "" {
		return "{ " + body + " }"
	}
	opKwd := strings.ToLower(opType)
	if operationName != "" {
		return fmt.Sprintf("%s %s { %s }", opKwd, operationName, body)
	}
	return fmt.Sprintf("%s { %s }", opKwd, body)
}
