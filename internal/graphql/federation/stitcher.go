package federation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/logging"
)

// Stitcher holds a merged GraphQL schema and routes queries to the correct backends.
type Stitcher struct {
	routeID         string
	sources         []config.GraphQLFederationSource
	refreshInterval time.Duration
	transport       http.RoundTripper

	mu          sync.RWMutex
	merged      *MergedSchema
	executor    *Executor
	lastRefresh time.Time

	requests       atomic.Int64
	errors         atomic.Int64
	introspections atomic.Int64
}

// NewStitcher creates a new schema stitcher.
func NewStitcher(routeID string, cfg config.GraphQLFederationConfig, transport http.RoundTripper) *Stitcher {
	interval := cfg.RefreshInterval
	if interval == 0 {
		interval = 5 * time.Minute
	}
	return &Stitcher{
		routeID:         routeID,
		sources:         cfg.Sources,
		refreshInterval: interval,
		transport:       transport,
	}
}

// Init performs the initial schema introspection and merge.
func (s *Stitcher) Init(ctx context.Context) error {
	return s.refresh(ctx)
}

// refresh introspects all backends and merges schemas.
func (s *Stitcher) refresh(ctx context.Context) error {
	var sources []Source
	for _, src := range s.sources {
		schema, err := IntrospectSchema(ctx, src.URL, s.transport)
		if err != nil {
			logging.Error("federation: failed to introspect source",
				zap.String("route", s.routeID),
				zap.String("source", src.Name),
				zap.Error(err),
			)
			continue
		}
		sources = append(sources, Source{
			Name:   src.Name,
			URL:    src.URL,
			Schema: schema,
		})
	}

	if len(sources) < 2 {
		return fmt.Errorf("federation requires at least 2 reachable sources, got %d", len(sources))
	}

	merged, err := MergeSchemas(sources)
	if err != nil {
		return fmt.Errorf("merge schemas: %w", err)
	}

	// Build source URL map
	sourceURLs := make(map[string]string)
	for _, src := range s.sources {
		sourceURLs[src.Name] = src.URL
	}

	s.mu.Lock()
	s.merged = merged
	s.executor = NewExecutor(sourceURLs, s.transport)
	s.lastRefresh = time.Now()
	s.mu.Unlock()

	return nil
}

// ensureFresh refreshes the schema if the interval has elapsed.
func (s *Stitcher) ensureFresh(ctx context.Context) {
	s.mu.RLock()
	needsRefresh := time.Since(s.lastRefresh) >= s.refreshInterval
	s.mu.RUnlock()

	if needsRefresh {
		if err := s.refresh(ctx); err != nil {
			logging.Error("federation: schema refresh failed",
				zap.String("route", s.routeID),
				zap.Error(err),
			)
		}
	}
}

// HandleQuery processes a GraphQL request: splits, fans out, and merges.
func (s *Stitcher) HandleQuery(ctx context.Context, req GraphQLRequest) (*GraphQLResponse, error) {
	s.requests.Add(1)
	s.ensureFresh(ctx)

	s.mu.RLock()
	merged := s.merged
	executor := s.executor
	s.mu.RUnlock()

	if merged == nil || executor == nil {
		s.errors.Add(1)
		return nil, fmt.Errorf("federation schema not initialized")
	}

	// Handle introspection
	if isIntrospectionQuery(req.Query) {
		s.introspections.Add(1)
		return s.handleIntrospection(merged)
	}

	// Split the query by field ownership
	subQueries, err := SplitQuery(req.Query, req.OperationName, req.Variables, merged.FieldOwner)
	if err != nil {
		s.errors.Add(1)
		return nil, fmt.Errorf("split query: %w", err)
	}

	// Execute and merge
	resp, err := executor.Execute(ctx, subQueries)
	if err != nil {
		s.errors.Add(1)
		return nil, err
	}

	return resp, nil
}

// handleIntrospection returns the merged schema as an introspection response.
func (s *Stitcher) handleIntrospection(merged *MergedSchema) (*GraphQLResponse, error) {
	schemaJSON, err := json.Marshal(map[string]interface{}{
		"__schema": merged.Schema,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal introspection: %w", err)
	}
	return &GraphQLResponse{Data: schemaJSON}, nil
}

// isIntrospectionQuery checks if a query is an introspection query.
func isIntrospectionQuery(query string) bool {
	// Simple heuristic: if the query contains __schema or __type at the top level
	for i := 0; i < len(query)-8; i++ {
		if query[i] == '_' && query[i+1] == '_' {
			rest := query[i:]
			if len(rest) >= 8 && rest[:8] == "__schema" {
				return true
			}
			if len(rest) >= 6 && rest[:6] == "__type" {
				return true
			}
		}
	}
	return false
}

// Stats returns stitcher statistics.
func (s *Stitcher) Stats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[string]interface{}{
		"sources":         len(s.sources),
		"requests":        s.requests.Load(),
		"errors":          s.errors.Load(),
		"introspections":  s.introspections.Load(),
		"refresh_interval": s.refreshInterval.String(),
	}
	if s.merged != nil {
		stats["fields"] = len(s.merged.FieldOwner)
	}
	return stats
}
