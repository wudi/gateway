package graphql

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func sha256Sum(s string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))
}

func batchBody(reqs ...GraphQLRequest) []byte {
	b, _ := json.Marshal(reqs)
	return b
}

func makeBatchRequest(body []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestBatchDetection_ArrayVsObject(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	nextCalled := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"data":{"user":"ok"}}`))
	})
	handler := p.Middleware()(next)

	// Object (single query) — should go through single-query flow
	body, _ := json.Marshal(GraphQLRequest{Query: `{ user }`})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if nextCalled != 1 {
		t.Errorf("expected next called 1 time for object, got %d", nextCalled)
	}
	if GetBatchInfo(r.Context()) != nil {
		t.Error("expected no batch info for single query")
	}

	// Array (batch) — should go through batch flow
	nextCalled = 0
	body = batchBody(GraphQLRequest{Query: `{ user }`})
	r = makeBatchRequest(body)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if nextCalled != 1 {
		t.Errorf("expected next called 1 time for batch (pass_through), got %d", nextCalled)
	}
}

func TestBatchDisabled_ReturnsError(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		// Batching not enabled
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called")
	})
	handler := p.Middleware()(next)

	body := batchBody(GraphQLRequest{Query: `{ user }`})
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "batched queries are not enabled") {
		t.Errorf("unexpected error: %s", w.Body.String())
	}
}

func TestBatchEmpty_ReturnsEmptyArray(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called for empty batch")
	})
	handler := p.Middleware()(next)

	r := makeBatchRequest([]byte("[]"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("expected empty array, got %q", w.Body.String())
	}
}

func TestBatchSingleElement(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		bi := GetBatchInfo(r.Context())
		if bi == nil {
			t.Error("expected batch info in context")
		} else if bi.Size != 1 {
			t.Errorf("expected batch size 1, got %d", bi.Size)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`[{"data":{"user":"ok"}}]`))
	})
	handler := p.Middleware()(next)

	body := batchBody(GraphQLRequest{Query: `{ user }`})
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !nextCalled {
		t.Error("expected next to be called")
	}
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestBatchMaxSizeEnforcement(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled:      true,
			MaxBatchSize: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called")
	})
	handler := p.Middleware()(next)

	// 3 queries exceeds max of 2
	body := batchBody(
		GraphQLRequest{Query: `{ a }`},
		GraphQLRequest{Query: `{ b }`},
		GraphQLRequest{Query: `{ c }`},
	)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "batch size 3 exceeds maximum 2") {
		t.Errorf("unexpected error: %s", w.Body.String())
	}

	// Verify metric
	if p.batchSizeRejected.Load() != 1 {
		t.Errorf("expected batchSizeRejected=1, got %d", p.batchSizeRejected.Load())
	}
}

func TestBatchMaxSizeDefault(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled:      true,
			MaxBatchSize: 0, // should default to 10
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`[{"data":"ok"}]`))
	})
	handler := p.Middleware()(next)

	// 10 queries should pass
	reqs := make([]GraphQLRequest, 10)
	for i := range reqs {
		reqs[i] = GraphQLRequest{Query: `{ user }`}
	}
	body := batchBody(reqs...)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("expected 200 for 10 queries (default max), got %d", w.Code)
	}

	// 11 queries should fail
	reqs = make([]GraphQLRequest, 11)
	for i := range reqs {
		reqs[i] = GraphQLRequest{Query: `{ user }`}
	}
	body = batchBody(reqs...)
	r = makeBatchRequest(body)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Errorf("expected 400 for 11 queries, got %d", w.Code)
	}
}

func TestBatchPerQueryDepthValidation(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled:  true,
		MaxDepth: 2,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called")
	})
	handler := p.Middleware()(next)

	body := batchBody(
		GraphQLRequest{Query: `{ user { name } }`},                        // depth 2, OK
		GraphQLRequest{Query: `{ user { friends { posts { title } } } }`}, // depth 4, exceeds
	)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "query[1]") {
		t.Errorf("expected error to reference query[1], got: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "depth") {
		t.Errorf("expected depth error, got: %s", w.Body.String())
	}
}

func TestBatchPerQueryComplexityValidation(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled:       true,
		MaxComplexity: 2,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called")
	})
	handler := p.Middleware()(next)

	body := batchBody(
		GraphQLRequest{Query: `{ user }`},                   // complexity 1, OK
		GraphQLRequest{Query: `{ user { name email age } }`}, // complexity 4, exceeds
	)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "query[1]") {
		t.Errorf("expected error to reference query[1], got: %s", w.Body.String())
	}
}

func TestBatchPerQueryIntrospectionValidation(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled:       true,
		Introspection: false,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called")
	})
	handler := p.Middleware()(next)

	body := batchBody(
		GraphQLRequest{Query: `{ user }`},
		GraphQLRequest{Query: `{ __schema { types { name } } }`},
	)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 403 {
		t.Errorf("expected 403, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "query[1]") {
		t.Errorf("expected error to reference query[1], got: %s", w.Body.String())
	}
}

func TestBatchPerQueryRateLimiting(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		OperationLimits: map[string]int{
			"mutation": 1,
		},
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called")
	})
	handler := p.Middleware()(next)

	body := batchBody(
		GraphQLRequest{Query: `mutation { createA { id } }`},
		GraphQLRequest{Query: `mutation { createB { id } }`}, // should be rate limited
	)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 429 {
		t.Errorf("expected 429, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "query[1]") {
		t.Errorf("expected error to reference query[1], got: %s", w.Body.String())
	}
}

func TestBatchPassThroughMode(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
			Mode:    "pass_through",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var capturedBody []byte
	var capturedBatchInfo *BatchInfo
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		capturedBatchInfo = GetBatchInfo(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`[{"data":{"a":"1"}},{"data":{"b":"2"}}]`))
	})
	handler := p.Middleware()(next)

	body := batchBody(
		GraphQLRequest{Query: `{ a }`},
		GraphQLRequest{Query: `{ b }`},
	)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Verify body is a JSON array forwarded to backend
	var forwarded []GraphQLRequest
	if err := json.Unmarshal(capturedBody, &forwarded); err != nil {
		t.Fatalf("expected JSON array body, got: %s", capturedBody)
	}
	if len(forwarded) != 2 {
		t.Errorf("expected 2 queries forwarded, got %d", len(forwarded))
	}

	// Verify batch info in context
	if capturedBatchInfo == nil {
		t.Fatal("expected batch info in context")
	}
	if capturedBatchInfo.Size != 2 {
		t.Errorf("expected batch size 2, got %d", capturedBatchInfo.Size)
	}
	if capturedBatchInfo.Mode != "pass_through" {
		t.Errorf("expected mode pass_through, got %s", capturedBatchInfo.Mode)
	}
	if len(capturedBatchInfo.Queries) != 2 {
		t.Errorf("expected 2 query infos, got %d", len(capturedBatchInfo.Queries))
	}

	// Response passed through
	if !strings.Contains(w.Body.String(), `"a":"1"`) {
		t.Errorf("expected response to be passed through, got: %s", w.Body.String())
	}
}

func TestBatchSplitMode(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
			Mode:    "split",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var gqlReq GraphQLRequest
		json.Unmarshal(bodyBytes, &gqlReq)

		info := GetInfo(r.Context())
		if info == nil {
			t.Error("expected GraphQLInfo in context for split request")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		// Echo back the query as the data field for verification
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"query": gqlReq.Query,
			},
		})
	})
	handler := p.Middleware()(next)

	body := batchBody(
		GraphQLRequest{Query: `{ a }`},
		GraphQLRequest{Query: `{ b }`},
		GraphQLRequest{Query: `{ c }`},
	)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Parse merged response
	var responses []json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &responses); err != nil {
		t.Fatalf("expected JSON array response, error: %v, body: %s", err, w.Body.String())
	}
	if len(responses) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}

	// Verify each response contains the correct query
	expectedQueries := []string{`{ a }`, `{ b }`, `{ c }`}
	for i, raw := range responses {
		var resp struct {
			Data struct {
				Query string `json:"query"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("response[%d]: unmarshal error: %v", i, err)
		}
		if resp.Data.Query != expectedQueries[i] {
			t.Errorf("response[%d]: expected query %q, got %q", i, expectedQueries[i], resp.Data.Query)
		}
	}
}

func TestBatchMetricsTracking(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled:      true,
			MaxBatchSize: 5,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`[{"data":"ok"}]`))
	})
	handler := p.Middleware()(next)

	// Send a batch of 3
	body := batchBody(
		GraphQLRequest{Query: `{ a }`},
		GraphQLRequest{Query: `{ b }`},
		GraphQLRequest{Query: `mutation { c { id } }`},
	)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if p.batchRequestsTotal.Load() != 1 {
		t.Errorf("expected batchRequestsTotal=1, got %d", p.batchRequestsTotal.Load())
	}
	if p.batchQueriesTotal.Load() != 3 {
		t.Errorf("expected batchQueriesTotal=3, got %d", p.batchQueriesTotal.Load())
	}
	if p.requestsTotal.Load() != 3 {
		t.Errorf("expected requestsTotal=3, got %d", p.requestsTotal.Load())
	}
	if p.queriesTotal.Load() != 2 {
		t.Errorf("expected queriesTotal=2, got %d", p.queriesTotal.Load())
	}
	if p.mutationsTotal.Load() != 1 {
		t.Errorf("expected mutationsTotal=1, got %d", p.mutationsTotal.Load())
	}
}

func TestBatchStatsOutput(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled:      true,
			MaxBatchSize: 5,
			Mode:         "split",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	stats := p.Stats()
	batching, ok := stats["batching"].(map[string]interface{})
	if !ok {
		t.Fatal("expected batching in stats")
	}
	if batching["mode"] != "split" {
		t.Errorf("expected mode split, got %v", batching["mode"])
	}
	if batching["max_batch_size"] != 5 {
		t.Errorf("expected max_batch_size 5, got %v", batching["max_batch_size"])
	}
	if batching["requests_total"].(int64) != 0 {
		t.Errorf("expected requests_total=0, got %v", batching["requests_total"])
	}
}

func TestBatchStatsNotPresentWhenDisabled(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	stats := p.Stats()
	if _, ok := stats["batching"]; ok {
		t.Error("expected no batching in stats when disabled")
	}
}

func TestBatchPerQueryAPQResolution(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		PersistedQueries: config.PersistedQueriesConfig{
			Enabled: true,
			MaxSize: 100,
		},
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Register a query via APQ using a single-query Parse call
	query := `{ user { name } }`
	// Compute SHA-256 hash of the query
	h := sha256Sum(query)

	regBody, _ := json.Marshal(GraphQLRequest{
		Query: query,
		Extensions: map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    1,
				"sha256Hash": h,
			},
		},
	})
	regReq := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(regBody))
	regReq.Header.Set("Content-Type", "application/json")
	_, _, err = p.Parse(regReq)
	if err != nil {
		t.Fatalf("failed to register APQ: %v", err)
	}

	// Now send a batch with one hash-only APQ request
	var capturedBody []byte
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	})
	handler := p.Middleware()(next)

	// Build batch: one normal query + one APQ hash-only
	batchJSON := `[{"query":"{ a }"},{"extensions":{"persistedQuery":{"version":1,"sha256Hash":"` + h + `"}}}]`
	r := makeBatchRequest([]byte(batchJSON))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify the APQ query was resolved in the forwarded body
	if len(capturedBody) > 0 {
		var forwarded []GraphQLRequest
		json.Unmarshal(capturedBody, &forwarded)
		if len(forwarded) == 2 && forwarded[1].Query != query {
			t.Errorf("expected APQ-resolved query %q, got %q", query, forwarded[1].Query)
		}
	}
}

func TestBatchInvalidJSON(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called")
	})
	handler := p.Middleware()(next)

	// Invalid JSON array
	r := makeBatchRequest([]byte(`[{"query": invalid}]`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestBatchMissingQuery(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called")
	})
	handler := p.Middleware()(next)

	body := batchBody(
		GraphQLRequest{Query: `{ user }`},
		GraphQLRequest{}, // missing query
	)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "query[1]") {
		t.Errorf("expected error to reference query[1], got: %s", w.Body.String())
	}
}

func TestBatchContextHelpers(t *testing.T) {
	info := &BatchInfo{
		Size: 3,
		Mode: "split",
		Queries: []*GraphQLInfo{
			{OperationType: "query"},
			{OperationType: "mutation"},
			{OperationType: "query"},
		},
	}

	r := httptest.NewRequest(http.MethodPost, "/graphql", nil)

	// No batch info initially
	if got := GetBatchInfo(r.Context()); got != nil {
		t.Error("expected nil batch info from empty context")
	}

	ctx := WithBatchInfo(r.Context(), info)
	r = r.WithContext(ctx)

	got := GetBatchInfo(r.Context())
	if got == nil {
		t.Fatal("expected batch info from context")
	}
	if got.Size != 3 {
		t.Errorf("expected size 3, got %d", got.Size)
	}
	if got.Mode != "split" {
		t.Errorf("expected mode split, got %s", got.Mode)
	}
}

func TestBatchDefaultMode(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
			// Mode not set, should default to pass_through
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var capturedBatchInfo *BatchInfo
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBatchInfo = GetBatchInfo(r.Context())
		w.WriteHeader(200)
	})
	handler := p.Middleware()(next)

	body := batchBody(GraphQLRequest{Query: `{ user }`})
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if capturedBatchInfo == nil {
		t.Fatal("expected batch info")
	}
	if capturedBatchInfo.Mode != "pass_through" {
		t.Errorf("expected default mode pass_through, got %s", capturedBatchInfo.Mode)
	}
}

func TestBatchWhitespaceBeforeArray(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		Batching: config.GraphQLBatchingConfig{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	handler := p.Middleware()(next)

	// Body with leading whitespace before array
	body := []byte(`  [{"query": "{ user }"}]`)
	r := makeBatchRequest(body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200 (batch detected despite whitespace), got %d", w.Code)
	}
}
