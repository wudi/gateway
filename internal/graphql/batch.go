package graphql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// handleBatch processes a batched GraphQL request (JSON array of operations).
func (p *Parser) handleBatch(w http.ResponseWriter, r *http.Request, body []byte, next http.Handler) {
	var batch []GraphQLRequest
	if err := json.Unmarshal(body, &batch); err != nil {
		p.parseErrors.Add(1)
		writeGraphQLError(w, "invalid batch JSON: "+err.Error(), 400)
		return
	}

	// Empty batch returns empty array
	if len(batch) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte("[]"))
		return
	}

	// Check max batch size
	maxSize := p.cfg.Batching.MaxBatchSize
	if maxSize == 0 {
		maxSize = 10
	}
	if len(batch) > maxSize {
		p.batchSizeRejected.Add(1)
		writeGraphQLError(w, fmt.Sprintf("batch size %d exceeds maximum %d", len(batch), maxSize), 400)
		return
	}

	p.batchRequestsTotal.Add(1)
	p.batchQueriesTotal.Add(int64(len(batch)))

	// Resolve APQ, parse, analyze, and validate each query
	infos := make([]*GraphQLInfo, len(batch))
	resolvedBatch := make([]GraphQLRequest, len(batch))
	copy(resolvedBatch, batch)

	for i, gqlReq := range resolvedBatch {
		reqBody, _ := json.Marshal(gqlReq)
		info, newBody, err := p.resolveAndParse(gqlReq, reqBody)
		if err != nil {
			if gqlErr, ok := err.(*GraphQLError); ok {
				writeGraphQLError(w, fmt.Sprintf("query[%d]: %s", i, gqlErr.Message), gqlErr.StatusCode)
			} else {
				p.parseErrors.Add(1)
				writeGraphQLError(w, fmt.Sprintf("query[%d]: %s", i, err.Error()), 400)
			}
			return
		}

		// If APQ resolved the query, update the batch entry
		if len(newBody) > 0 {
			json.Unmarshal(newBody, &resolvedBatch[i])
		}

		p.requestsTotal.Add(1)
		p.countOperation(info)

		if err := p.Check(info); err != nil {
			if gqlErr, ok := err.(*GraphQLError); ok {
				writeGraphQLError(w, fmt.Sprintf("query[%d]: %s", i, gqlErr.Message), gqlErr.StatusCode)
			} else {
				writeGraphQLError(w, fmt.Sprintf("query[%d]: %s", i, err.Error()), 400)
			}
			return
		}

		if !p.AllowOperation(info) {
			p.rateLimited.Add(1)
			writeGraphQLError(w, fmt.Sprintf("query[%d]: rate limit exceeded for %s operations", i, info.OperationType), 429)
			return
		}

		infos[i] = info
	}

	mode := p.cfg.Batching.Mode
	if mode == "" {
		mode = "pass_through"
	}

	switch mode {
	case "split":
		p.handleBatchSplit(w, r, resolvedBatch, infos, next)
	default:
		p.handleBatchPassThrough(w, r, resolvedBatch, infos, body, next)
	}
}

// handleBatchPassThrough re-marshals the batch (APQ may have resolved queries)
// and forwards the entire array to the backend.
func (p *Parser) handleBatchPassThrough(w http.ResponseWriter, r *http.Request, batch []GraphQLRequest, infos []*GraphQLInfo, originalBody []byte, next http.Handler) {
	// Re-marshal in case APQ resolved any queries
	newBody, err := json.Marshal(batch)
	if err != nil {
		writeGraphQLError(w, "failed to marshal batch", 500)
		return
	}

	batchInfo := &BatchInfo{
		Size:    len(batch),
		Mode:    "pass_through",
		Queries: infos,
	}

	ctx := WithBatchInfo(r.Context(), batchInfo)
	// Store first query info for backward compat with single-query context readers
	if len(infos) > 0 {
		ctx = WithInfo(ctx, infos[0])
	}
	r = r.WithContext(ctx)

	r.Body = io.NopCloser(bytes.NewReader(newBody))
	r.ContentLength = int64(len(newBody))

	next.ServeHTTP(w, r)
}

// handleBatchSplit fans out each query as an individual request and merges responses.
func (p *Parser) handleBatchSplit(w http.ResponseWriter, r *http.Request, batch []GraphQLRequest, infos []*GraphQLInfo, next http.Handler) {
	type indexedResponse struct {
		index    int
		recorder *httptest.ResponseRecorder
	}

	results := make([]indexedResponse, len(batch))
	var wg sync.WaitGroup

	for i, gqlReq := range batch {
		wg.Add(1)
		go func(idx int, req GraphQLRequest, info *GraphQLInfo) {
			defer wg.Done()

			body, _ := json.Marshal(req)
			subReq := r.Clone(r.Context())
			subReq.Body = io.NopCloser(bytes.NewReader(body))
			subReq.ContentLength = int64(len(body))

			ctx := WithInfo(subReq.Context(), info)
			subReq = subReq.WithContext(ctx)

			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, subReq)

			results[idx] = indexedResponse{index: idx, recorder: rec}
		}(i, gqlReq, infos[i])
	}

	wg.Wait()

	// Merge responses into a JSON array
	merged := make([]json.RawMessage, len(batch))
	for i, res := range results {
		merged[i] = res.recorder.Body.Bytes()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(merged)
}
