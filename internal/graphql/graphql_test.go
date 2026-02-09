package graphql

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func makeGQLRequest(query string) *http.Request {
	body, _ := json.Marshal(GraphQLRequest{Query: query})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func makeGQLRequestWithVars(query string, vars map[string]interface{}) *http.Request {
	varsJSON, _ := json.Marshal(vars)
	body, _ := json.Marshal(GraphQLRequest{Query: query, Variables: varsJSON})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func makeGQLRequestWithOp(query, opName string) *http.Request {
	body, _ := json.Marshal(GraphQLRequest{Query: query, OperationName: opName})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestParseSimpleQuery(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	r := makeGQLRequest(`{ user { name } }`)
	info, body, err := p.Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected info, got nil")
	}
	if info.OperationType != "query" {
		t.Errorf("expected query, got %s", info.OperationType)
	}
	if info.Depth != 2 {
		t.Errorf("expected depth 2, got %d", info.Depth)
	}
	if info.Complexity != 2 {
		t.Errorf("expected complexity 2, got %d", info.Complexity)
	}
	if info.Introspection {
		t.Error("expected no introspection")
	}
	if len(body) == 0 {
		t.Error("expected non-empty body")
	}
}

func TestParseMutation(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	r := makeGQLRequest(`mutation { createUser(name: "test") { id } }`)
	info, _, err := p.Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if info.OperationType != "mutation" {
		t.Errorf("expected mutation, got %s", info.OperationType)
	}
}

func TestParseSubscription(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	r := makeGQLRequest(`subscription { messageAdded { content } }`)
	info, _, err := p.Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if info.OperationType != "subscription" {
		t.Errorf("expected subscription, got %s", info.OperationType)
	}
}

func TestDepthCalculation(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		query    string
		expected int
	}{
		{`{ user }`, 1},
		{`{ user { name } }`, 2},
		{`{ user { friends { name } } }`, 3},
		{`{ user { friends { posts { title comments { text } } } } }`, 5},
	}

	for _, tt := range tests {
		r := makeGQLRequest(tt.query)
		info, _, err := p.Parse(r)
		if err != nil {
			t.Fatalf("query %q: %v", tt.query, err)
		}
		if info.Depth != tt.expected {
			t.Errorf("query %q: expected depth %d, got %d", tt.query, tt.expected, info.Depth)
		}
	}
}

func TestComplexityCalculation(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		query    string
		expected int
	}{
		{`{ user }`, 1},
		{`{ user { name email } }`, 3},          // user(1) + name(1) + email(1)
		{`{ user { name } post { title } }`, 4}, // user(1) + name(1) + post(1) + title(1)
	}

	for _, tt := range tests {
		r := makeGQLRequest(tt.query)
		info, _, err := p.Parse(r)
		if err != nil {
			t.Fatalf("query %q: %v", tt.query, err)
		}
		if info.Complexity != tt.expected {
			t.Errorf("query %q: expected complexity %d, got %d", tt.query, tt.expected, info.Complexity)
		}
	}
}

func TestIntrospectionDetection(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		query    string
		expected bool
	}{
		{`{ __schema { types { name } } }`, true},
		{`{ __type(name: "User") { name } }`, true},
		{`{ user { name } }`, false},
		{`{ user { __typename } }`, false}, // __typename is nested, not top-level
	}

	for _, tt := range tests {
		r := makeGQLRequest(tt.query)
		info, _, err := p.Parse(r)
		if err != nil {
			t.Fatalf("query %q: %v", tt.query, err)
		}
		if info.Introspection != tt.expected {
			t.Errorf("query %q: expected introspection=%v, got %v", tt.query, tt.expected, info.Introspection)
		}
	}
}

func TestCheckDepthLimit(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled:  true,
		MaxDepth: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Within limit
	r := makeGQLRequest(`{ user { name } }`)
	info, _, _ := p.Parse(r)
	if err := p.Check(info); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Exceeds limit
	r = makeGQLRequest(`{ user { friends { posts { title } } } }`)
	info, _, _ = p.Parse(r)
	err = p.Check(info)
	if err == nil {
		t.Fatal("expected error for deep query")
	}
	gqlErr, ok := err.(*GraphQLError)
	if !ok {
		t.Fatalf("expected GraphQLError, got %T", err)
	}
	if gqlErr.StatusCode != 400 {
		t.Errorf("expected 400, got %d", gqlErr.StatusCode)
	}
	if !strings.Contains(gqlErr.Message, "depth") {
		t.Errorf("expected depth error message, got %q", gqlErr.Message)
	}
}

func TestCheckComplexityLimit(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled:       true,
		MaxComplexity: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Within limit
	r := makeGQLRequest(`{ user { name } }`)
	info, _, _ := p.Parse(r)
	if err := p.Check(info); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Exceeds limit
	r = makeGQLRequest(`{ user { name email phone } }`)
	info, _, _ = p.Parse(r)
	err = p.Check(info)
	if err == nil {
		t.Fatal("expected error for complex query")
	}
	gqlErr, ok := err.(*GraphQLError)
	if !ok {
		t.Fatalf("expected GraphQLError, got %T", err)
	}
	if gqlErr.StatusCode != 400 {
		t.Errorf("expected 400, got %d", gqlErr.StatusCode)
	}
}

func TestCheckIntrospectionBlocked(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled:       true,
		Introspection: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	r := makeGQLRequest(`{ __schema { types { name } } }`)
	info, _, _ := p.Parse(r)
	err = p.Check(info)
	if err == nil {
		t.Fatal("expected error for introspection")
	}
	gqlErr, ok := err.(*GraphQLError)
	if !ok {
		t.Fatalf("expected GraphQLError, got %T", err)
	}
	if gqlErr.StatusCode != 403 {
		t.Errorf("expected 403, got %d", gqlErr.StatusCode)
	}
}

func TestCheckIntrospectionAllowed(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled:       true,
		Introspection: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	r := makeGQLRequest(`{ __schema { types { name } } }`)
	info, _, _ := p.Parse(r)
	if err := p.Check(info); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestOperationRateLimiting(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		OperationLimits: map[string]int{
			"mutation": 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	info := &GraphQLInfo{OperationType: "mutation"}
	// First should be allowed
	if !p.AllowOperation(info) {
		t.Error("expected first mutation to be allowed")
	}
	// Second should be rejected (1 req/s limit, burst=1)
	if p.AllowOperation(info) {
		t.Error("expected second mutation to be rejected")
	}

	// Queries should still be allowed (no limit configured)
	queryInfo := &GraphQLInfo{OperationType: "query"}
	if !p.AllowOperation(queryInfo) {
		t.Error("expected query to be allowed")
	}
}

func TestMiddlewarePassthroughNonGraphQL(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true, MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(200)
	})

	handler := p.Middleware()(next)

	// GET request should pass through
	r := httptest.NewRequest(http.MethodGet, "/graphql", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if !nextCalled {
		t.Error("expected next to be called for GET request")
	}

	// Non-JSON POST should pass through
	nextCalled = false
	r = httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader("not json"))
	r.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if !nextCalled {
		t.Error("expected next to be called for non-JSON POST")
	}
}

func TestMiddlewareRejectsDeepQuery(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true, MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	handler := p.Middleware()(next)

	body, _ := json.Marshal(GraphQLRequest{Query: `{ user { friends { posts { title } } } }`})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if nextCalled {
		t.Error("expected next NOT to be called for deep query")
	}
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	errors, ok := resp["errors"].([]interface{})
	if !ok || len(errors) == 0 {
		t.Error("expected GraphQL error response")
	}
}

func TestMiddlewareRejectsIntrospection(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true, Introspection: false})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called")
	})

	handler := p.Middleware()(next)

	body, _ := json.Marshal(GraphQLRequest{Query: `{ __schema { types { name } } }`})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 403 {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestMiddlewareAllowsValidQuery(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true, MaxDepth: 10, MaxComplexity: 100})
	if err != nil {
		t.Fatal(err)
	}

	var capturedInfo *GraphQLInfo
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedInfo = GetInfo(r.Context())
		// Verify body is still readable
		bodyBytes, _ := io.ReadAll(r.Body)
		if len(bodyBytes) == 0 {
			t.Error("expected non-empty body")
		}
		w.WriteHeader(200)
	})

	handler := p.Middleware()(next)

	body, _ := json.Marshal(GraphQLRequest{Query: `query GetUser { user { name } }`})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedInfo == nil {
		t.Fatal("expected GraphQLInfo in context")
	}
	if capturedInfo.OperationName != "GetUser" {
		t.Errorf("expected operation name GetUser, got %s", capturedInfo.OperationName)
	}
	if capturedInfo.OperationType != "query" {
		t.Errorf("expected query, got %s", capturedInfo.OperationType)
	}
}

func TestMiddlewareRateLimit(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled: true,
		OperationLimits: map[string]int{
			"mutation": 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	handler := p.Middleware()(next)

	// First mutation should succeed
	body, _ := json.Marshal(GraphQLRequest{Query: `mutation { deleteUser(id: "1") { id } }`})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("first mutation: expected 200, got %d", w.Code)
	}

	// Second mutation should be rate limited
	body, _ = json.Marshal(GraphQLRequest{Query: `mutation { deleteUser(id: "2") { id } }`})
	r = httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 429 {
		t.Errorf("second mutation: expected 429, got %d", w.Code)
	}
}

func TestVariablesHash(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	r := makeGQLRequestWithVars(`query GetUser($id: ID!) { user(id: $id) { name } }`, map[string]interface{}{"id": "123"})
	info, _, err := p.Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if info.VariablesHash == "" {
		t.Error("expected non-empty variables hash")
	}

	// Different variables should produce different hash
	r2 := makeGQLRequestWithVars(`query GetUser($id: ID!) { user(id: $id) { name } }`, map[string]interface{}{"id": "456"})
	info2, _, _ := p.Parse(r2)
	if info.VariablesHash == info2.VariablesHash {
		t.Error("expected different hashes for different variables")
	}
}

func TestOperationName(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	query := `
		query GetUser { user { name } }
		query GetPost { post { title } }
	`
	r := makeGQLRequestWithOp(query, "GetPost")
	info, _, err := p.Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if info.OperationName != "GetPost" {
		t.Errorf("expected GetPost, got %s", info.OperationName)
	}
}

func TestStats(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true, MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	handler := p.Middleware()(next)

	// Valid query
	body, _ := json.Marshal(GraphQLRequest{Query: `{ user }`})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), r)

	// Deep query (rejected)
	body, _ = json.Marshal(GraphQLRequest{Query: `{ user { friends { name } } }`})
	r = httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), r)

	stats := p.Stats()
	// Both valid and rejected queries are parsed, so both count
	if stats["requests_total"].(int64) != 2 {
		t.Errorf("expected 2 requests total, got %v", stats["requests_total"])
	}
	if stats["depth_rejected"].(int64) != 1 {
		t.Errorf("expected 1 depth rejected, got %v", stats["depth_rejected"])
	}
	if stats["queries_total"].(int64) != 2 {
		t.Errorf("expected 2 queries total, got %v", stats["queries_total"])
	}
}

func TestContextHelpers(t *testing.T) {
	info := &GraphQLInfo{
		OperationName: "TestOp",
		OperationType: "query",
	}

	r := httptest.NewRequest(http.MethodPost, "/graphql", nil)

	// No info in context initially
	if got := GetInfo(r.Context()); got != nil {
		t.Error("expected nil info from empty context")
	}

	// Set info
	ctx := WithInfo(r.Context(), info)
	r = r.WithContext(ctx)

	got := GetInfo(r.Context())
	if got == nil {
		t.Fatal("expected info from context")
	}
	if got.OperationName != "TestOp" {
		t.Errorf("expected TestOp, got %s", got.OperationName)
	}
}

func TestManagerByRoute(t *testing.T) {
	m := NewGraphQLByRoute()

	if ids := m.RouteIDs(); len(ids) != 0 {
		t.Errorf("expected 0 routes, got %d", len(ids))
	}

	err := m.AddRoute("route1", config.GraphQLConfig{Enabled: true, MaxDepth: 5})
	if err != nil {
		t.Fatal(err)
	}

	if p := m.GetParser("route1"); p == nil {
		t.Error("expected parser for route1")
	}
	if p := m.GetParser("nonexistent"); p != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("expected [route1], got %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}

func TestParseInvalidJSON(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader("not json"))
	r.Header.Set("Content-Type", "application/json")
	_, _, err = p.Parse(r)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseInvalidGraphQL(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(GraphQLRequest{Query: "not valid graphql {"})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	_, _, err = p.Parse(r)
	if err == nil {
		t.Error("expected error for invalid GraphQL")
	}
}

func TestParseMissingQuery(t *testing.T) {
	p, err := New(config.GraphQLConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(GraphQLRequest{})
	r := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	_, _, err = p.Parse(r)
	if err == nil {
		t.Error("expected error for missing query")
	}
}
