package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// GraphQLRequest is a standard GraphQL HTTP request body.
type GraphQLRequest struct {
	Query         string          `json:"query"`
	OperationName string          `json:"operationName,omitempty"`
	Variables     json.RawMessage `json:"variables,omitempty"`
}

// GraphQLResponse is a standard GraphQL HTTP response body.
type GraphQLResponse struct {
	Data   json.RawMessage `json:"data,omitempty"`
	Errors []GraphQLError  `json:"errors,omitempty"`
}

// GraphQLError represents a GraphQL error.
type GraphQLError struct {
	Message    string                 `json:"message"`
	Path       []interface{}          `json:"path,omitempty"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

// Executor fans out sub-queries to backend sources and merges responses.
type Executor struct {
	sourceURLs map[string]string // source name → URL
	transport  http.RoundTripper
}

// NewExecutor creates a new query executor.
func NewExecutor(sourceURLs map[string]string, transport http.RoundTripper) *Executor {
	return &Executor{
		sourceURLs: sourceURLs,
		transport:  transport,
	}
}

// Execute fans out sub-queries concurrently and merges the responses.
func (e *Executor) Execute(ctx context.Context, subQueries []SubQuery) (*GraphQLResponse, error) {
	if len(subQueries) == 0 {
		return &GraphQLResponse{Data: json.RawMessage(`{}`)}, nil
	}

	// Single source: simple execution
	if len(subQueries) == 1 {
		return e.executeOne(ctx, subQueries[0])
	}

	// Fan out to multiple sources concurrently
	results := make([]result, len(subQueries))
	var wg sync.WaitGroup

	for i, sq := range subQueries {
		wg.Add(1)
		go func(idx int, sq SubQuery) {
			defer wg.Done()
			resp, err := e.executeOne(ctx, sq)
			results[idx] = result{resp: resp, err: err}
		}(i, sq)
	}

	wg.Wait()

	// Merge responses
	return e.mergeResponses(results)
}

// executeOne executes a single sub-query against its source backend.
func (e *Executor) executeOne(ctx context.Context, sq SubQuery) (*GraphQLResponse, error) {
	url, ok := e.sourceURLs[sq.SourceName]
	if !ok {
		return nil, fmt.Errorf("unknown source: %s", sq.SourceName)
	}

	reqBody := GraphQLRequest{
		Query:     sq.Query,
		Variables: sq.Variables,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request for %s: %w", sq.SourceName, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", sq.SourceName, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Transport: e.transport}
	if e.transport == nil {
		client = http.DefaultClient
	}

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request for %s: %w", sq.SourceName, err)
	}
	defer httpResp.Body.Close()

	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response from %s: %w", sq.SourceName, err)
	}

	var resp GraphQLResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response from %s: %w", sq.SourceName, err)
	}

	return &resp, nil
}

// mergeResponses merges multiple GraphQL responses into one.
type result struct {
	resp *GraphQLResponse
	err  error
}

func (e *Executor) mergeResponses(results []result) (*GraphQLResponse, error) {
	mergedData := make(map[string]json.RawMessage)
	var mergedErrors []GraphQLError

	for _, r := range results {
		if r.err != nil {
			mergedErrors = append(mergedErrors, GraphQLError{
				Message: r.err.Error(),
			})
			continue
		}
		if r.resp == nil {
			continue
		}

		// Merge errors
		mergedErrors = append(mergedErrors, r.resp.Errors...)

		// Merge data fields
		if len(r.resp.Data) > 0 {
			var data map[string]json.RawMessage
			if err := json.Unmarshal(r.resp.Data, &data); err != nil {
				continue
			}
			for k, v := range data {
				mergedData[k] = v
			}
		}
	}

	dataBytes, err := json.Marshal(mergedData)
	if err != nil {
		return nil, fmt.Errorf("marshal merged data: %w", err)
	}

	resp := &GraphQLResponse{
		Data: dataBytes,
	}
	if len(mergedErrors) > 0 {
		resp.Errors = mergedErrors
	}

	return resp, nil
}
