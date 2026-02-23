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
	Query         string                 `json:"query"`
	Variables     json.RawMessage        `json:"variables"`
	OperationName string                 `json:"operationName"`
	Extensions    map[string]interface{} `json:"extensions,omitempty"`
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
	apqCache         *APQCache

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

	// Batch metrics
	batchRequestsTotal atomic.Int64
	batchQueriesTotal  atomic.Int64
	batchSizeRejected  atomic.Int64
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

	if cfg.PersistedQueries.Enabled {
		apq, err := NewAPQCache(cfg.PersistedQueries.MaxSize)
		if err != nil {
			return nil, err
		}
		p.apqCache = apq
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

	return p.resolveAndParse(gqlReq, body)
}

// analyzeDocument extracts GraphQLInfo from a parsed AST document and the original request.
func analyzeDocument(doc *ast.QueryDocument, gqlReq GraphQLRequest) *GraphQLInfo {
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

	return info
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

			// Read body once for batch detection
			body, err := io.ReadAll(r.Body)
			if err != nil {
				p.parseErrors.Add(1)
				writeGraphQLError(w, "failed to read body", 400)
				return
			}
			r.Body.Close()

			// Detect batch (JSON array)
			trimmed := bytes.TrimLeft(body, " \t\r\n")
			if len(trimmed) > 0 && trimmed[0] == '[' {
				if p.cfg.Batching.Enabled {
					p.handleBatch(w, r, body, next)
					return
				}
				writeGraphQLError(w, "batched queries are not enabled", 400)
				return
			}

			// Single query flow — parse from already-read body
			var gqlReq GraphQLRequest
			if err := json.Unmarshal(body, &gqlReq); err != nil {
				p.parseErrors.Add(1)
				writeGraphQLError(w, "invalid JSON: "+err.Error(), 400)
				return
			}

			info, body, err := p.resolveAndParse(gqlReq, body)
			if err != nil {
				if gqlErr, ok := err.(*GraphQLError); ok {
					writeGraphQLError(w, gqlErr.Message, gqlErr.StatusCode)
				} else {
					p.parseErrors.Add(1)
					writeGraphQLError(w, err.Error(), 400)
				}
				return
			}

			p.requestsTotal.Add(1)
			p.countOperation(info)

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

// resolveAndParse handles APQ resolution, parsing, and analysis for a single GraphQLRequest.
// It takes the already-read body bytes so it can re-marshal if APQ resolves the query.
func (p *Parser) resolveAndParse(gqlReq GraphQLRequest, body []byte) (*GraphQLInfo, []byte, error) {
	// APQ: handle persisted query extensions
	if p.apqCache != nil {
		if hash, ok := extractAPQHash(gqlReq.Extensions); ok {
			if gqlReq.Query == "" {
				cached, found := p.apqCache.Lookup(hash)
				if !found {
					return nil, body, &GraphQLError{
						Message:    "PersistedQueryNotFound",
						StatusCode: 200,
					}
				}
				gqlReq.Query = cached
				body, _ = json.Marshal(gqlReq)
			} else {
				if !p.apqCache.Register(hash, gqlReq.Query) {
					return nil, body, &GraphQLError{
						Message:    "provided sha does not match query",
						StatusCode: 400,
					}
				}
			}
		}
	}

	if gqlReq.Query == "" {
		return nil, body, fmt.Errorf("missing query field")
	}

	doc, parseErr := parser.ParseQuery(&ast.Source{Input: gqlReq.Query})
	if parseErr != nil {
		return nil, body, fmt.Errorf("invalid GraphQL query: %w", parseErr)
	}

	info := analyzeDocument(doc, gqlReq)
	return info, body, nil
}

// countOperation increments the per-operation-type metric counter.
func (p *Parser) countOperation(info *GraphQLInfo) {
	switch info.OperationType {
	case "query":
		p.queriesTotal.Add(1)
	case "mutation":
		p.mutationsTotal.Add(1)
	case "subscription":
		p.subscriptionsTotal.Add(1)
	}
}

// Stats returns a snapshot of metrics.
func (p *Parser) Stats() map[string]interface{} {
	stats := map[string]interface{}{
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
	if p.apqCache != nil {
		stats["persisted_queries"] = p.apqCache.Stats()
	}
	if p.cfg.Batching.Enabled {
		mode := p.cfg.Batching.Mode
		if mode == "" {
			mode = "pass_through"
		}
		maxSize := p.cfg.Batching.MaxBatchSize
		if maxSize == 0 {
			maxSize = 10
		}
		stats["batching"] = map[string]interface{}{
			"mode":           mode,
			"max_batch_size": maxSize,
			"requests_total": p.batchRequestsTotal.Load(),
			"queries_total":  p.batchQueriesTotal.Load(),
			"size_rejected":  p.batchSizeRejected.Load(),
		}
	}
	return stats
}

// extractAPQHash extracts the sha256Hash from the Apollo APQ extensions format.
func extractAPQHash(extensions map[string]interface{}) (string, bool) {
	if extensions == nil {
		return "", false
	}
	pq, ok := extensions["persistedQuery"]
	if !ok {
		return "", false
	}
	pqMap, ok := pq.(map[string]interface{})
	if !ok {
		return "", false
	}
	hash, ok := pqMap["sha256Hash"].(string)
	if !ok || hash == "" {
		return "", false
	}
	return hash, true
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
