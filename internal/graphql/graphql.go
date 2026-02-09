package graphql

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
	"golang.org/x/time/rate"
)

// GraphQLRequest represents a parsed GraphQL HTTP request body.
type GraphQLRequest struct {
	Query         string          `json:"query"`
	Variables     json.RawMessage `json:"variables"`
	OperationName string          `json:"operationName"`
}

// GraphQLInfo holds the analyzed information about a GraphQL operation.
type GraphQLInfo struct {
	OperationName string
	OperationType string // "query", "mutation", "subscription"
	Depth         int
	Complexity    int
	Introspection bool
	VariablesHash string // hex SHA-256 of variables JSON
}

// Parser parses and validates GraphQL requests for a single route.
type Parser struct {
	cfg              config.GraphQLConfig
	operationLimiter map[string]*rate.Limiter

	// Atomic metrics
	requestsTotal        atomic.Int64
	queriesTotal         atomic.Int64
	mutationsTotal       atomic.Int64
	subscriptionsTotal   atomic.Int64
	depthRejected        atomic.Int64
	complexityRejected   atomic.Int64
	introspectionBlocked atomic.Int64
	rateLimited          atomic.Int64
	parseErrors          atomic.Int64
}

// New creates a new GraphQL parser with the given config.
func New(cfg config.GraphQLConfig) (*Parser, error) {
	p := &Parser{
		cfg:              cfg,
		operationLimiter: make(map[string]*rate.Limiter),
	}

	for opType, rps := range cfg.OperationLimits {
		p.operationLimiter[opType] = rate.NewLimiter(rate.Limit(rps), rps)
	}

	return p, nil
}

// Parse reads the request body, JSON-decodes it, parses the GraphQL query,
// and returns the analyzed info plus the raw body bytes (for re-wrapping).
func (p *Parser) Parse(r *http.Request) (*GraphQLInfo, []byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read body: %w", err)
	}
	r.Body.Close()

	var gqlReq GraphQLRequest
	if err := json.Unmarshal(body, &gqlReq); err != nil {
		return nil, body, fmt.Errorf("invalid JSON: %w", err)
	}

	if gqlReq.Query == "" {
		return nil, body, fmt.Errorf("missing query field")
	}

	doc, parseErr := parser.ParseQuery(&ast.Source{Input: gqlReq.Query})
	if parseErr != nil {
		return nil, body, fmt.Errorf("invalid GraphQL query: %w", parseErr)
	}

	info := &GraphQLInfo{
		OperationName: gqlReq.OperationName,
	}

	// Find the target operation
	var op *ast.OperationDefinition
	for _, o := range doc.Operations {
		if gqlReq.OperationName == "" || o.Name == gqlReq.OperationName {
			op = o
			break
		}
	}
	if op == nil && len(doc.Operations) > 0 {
		op = doc.Operations[0]
	}

	if op != nil {
		info.OperationType = string(op.Operation)
		if info.OperationName == "" {
			info.OperationName = op.Name
		}
	} else {
		info.OperationType = "query"
	}

	// Compute depth
	info.Depth = computeDepth(doc)

	// Compute complexity
	info.Complexity = computeComplexity(doc)

	// Detect introspection
	info.Introspection = detectIntrospection(doc)

	// Hash variables for cache key
	if len(gqlReq.Variables) > 0 {
		h := sha256.Sum256(gqlReq.Variables)
		info.VariablesHash = fmt.Sprintf("%x", h)
	}

	return info, body, nil
}

// Check enforces depth limit, complexity limit, and introspection block.
func (p *Parser) Check(info *GraphQLInfo) error {
	if !p.cfg.Introspection && info.Introspection {
		p.introspectionBlocked.Add(1)
		return &GraphQLError{Message: "introspection is not allowed", StatusCode: 403}
	}

	if p.cfg.MaxDepth > 0 && info.Depth > p.cfg.MaxDepth {
		p.depthRejected.Add(1)
		return &GraphQLError{
			Message:    fmt.Sprintf("query depth %d exceeds maximum %d", info.Depth, p.cfg.MaxDepth),
			StatusCode: 400,
		}
	}

	if p.cfg.MaxComplexity > 0 && info.Complexity > p.cfg.MaxComplexity {
		p.complexityRejected.Add(1)
		return &GraphQLError{
			Message:    fmt.Sprintf("query complexity %d exceeds maximum %d", info.Complexity, p.cfg.MaxComplexity),
			StatusCode: 400,
		}
	}

	return nil
}

// AllowOperation checks the per-operation-type rate limiter.
func (p *Parser) AllowOperation(info *GraphQLInfo) bool {
	limiter, ok := p.operationLimiter[info.OperationType]
	if !ok {
		return true
	}
	return limiter.Allow()
}

// Middleware returns the middleware function for this GraphQL parser.
func (p *Parser) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only intercept POST requests with JSON content type
			ct := r.Header.Get("Content-Type")
			if r.Method != http.MethodPost || (ct != "application/json" && ct != "application/json; charset=utf-8") {
				next.ServeHTTP(w, r)
				return
			}

			info, body, err := p.Parse(r)
			if err != nil {
				p.parseErrors.Add(1)
				writeGraphQLError(w, err.Error(), 400)
				return
			}

			p.requestsTotal.Add(1)
			switch info.OperationType {
			case "query":
				p.queriesTotal.Add(1)
			case "mutation":
				p.mutationsTotal.Add(1)
			case "subscription":
				p.subscriptionsTotal.Add(1)
			}

			if err := p.Check(info); err != nil {
				if gqlErr, ok := err.(*GraphQLError); ok {
					writeGraphQLError(w, gqlErr.Message, gqlErr.StatusCode)
				} else {
					writeGraphQLError(w, err.Error(), 400)
				}
				return
			}

			if !p.AllowOperation(info) {
				p.rateLimited.Add(1)
				writeGraphQLError(w, fmt.Sprintf("rate limit exceeded for %s operations", info.OperationType), 429)
				return
			}

			// Store info in context for downstream (e.g., cache key)
			ctx := WithInfo(r.Context(), info)
			r = r.WithContext(ctx)

			// Restore body for downstream handlers
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))

			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns a snapshot of metrics.
func (p *Parser) Stats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":               p.cfg.Enabled,
		"max_depth":             p.cfg.MaxDepth,
		"max_complexity":        p.cfg.MaxComplexity,
		"introspection_allowed": p.cfg.Introspection,
		"requests_total":        p.requestsTotal.Load(),
		"queries_total":         p.queriesTotal.Load(),
		"mutations_total":       p.mutationsTotal.Load(),
		"subscriptions_total":   p.subscriptionsTotal.Load(),
		"depth_rejected":        p.depthRejected.Load(),
		"complexity_rejected":   p.complexityRejected.Load(),
		"introspection_blocked": p.introspectionBlocked.Load(),
		"rate_limited":          p.rateLimited.Load(),
		"parse_errors":          p.parseErrors.Load(),
	}
}

// GraphQLError is an error with an associated HTTP status code.
type GraphQLError struct {
	Message    string
	StatusCode int
}

func (e *GraphQLError) Error() string {
	return e.Message
}

// writeGraphQLError writes a GraphQL-format JSON error response.
func writeGraphQLError(w http.ResponseWriter, msg string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"errors": []map[string]interface{}{
			{"message": msg},
		},
	})
}

// computeDepth walks the AST and returns the maximum nesting depth.
func computeDepth(doc *ast.QueryDocument) int {
	maxDepth := 0
	for _, op := range doc.Operations {
		d := selectionSetDepth(op.SelectionSet, 0)
		if d > maxDepth {
			maxDepth = d
		}
	}
	for _, frag := range doc.Fragments {
		d := selectionSetDepth(frag.SelectionSet, 0)
		if d > maxDepth {
			maxDepth = d
		}
	}
	return maxDepth
}

func selectionSetDepth(ss ast.SelectionSet, current int) int {
	if len(ss) == 0 {
		return current
	}
	maxDepth := current
	for _, sel := range ss {
		var childSS ast.SelectionSet
		switch s := sel.(type) {
		case *ast.Field:
			childSS = s.SelectionSet
		case *ast.InlineFragment:
			childSS = s.SelectionSet
		case *ast.FragmentSpread:
			// Fragment spreads reference named fragments; we don't resolve them here
			// to avoid infinite recursion. The fragment's depth is counted separately.
			continue
		}
		d := selectionSetDepth(childSS, current+1)
		if d > maxDepth {
			maxDepth = d
		}
	}
	return maxDepth
}

// computeComplexity scores each field as 1 + sum of child complexities.
func computeComplexity(doc *ast.QueryDocument) int {
	total := 0
	for _, op := range doc.Operations {
		total += selectionSetComplexity(op.SelectionSet)
	}
	return total
}

func selectionSetComplexity(ss ast.SelectionSet) int {
	complexity := 0
	for _, sel := range ss {
		switch s := sel.(type) {
		case *ast.Field:
			complexity += 1 + selectionSetComplexity(s.SelectionSet)
		case *ast.InlineFragment:
			complexity += selectionSetComplexity(s.SelectionSet)
		case *ast.FragmentSpread:
			complexity += 1
		}
	}
	return complexity
}

// detectIntrospection checks if any top-level field starts with "__".
func detectIntrospection(doc *ast.QueryDocument) bool {
	for _, op := range doc.Operations {
		for _, sel := range op.SelectionSet {
			if field, ok := sel.(*ast.Field); ok {
				if len(field.Name) >= 2 && field.Name[:2] == "__" {
					return true
				}
			}
		}
	}
	return false
}
