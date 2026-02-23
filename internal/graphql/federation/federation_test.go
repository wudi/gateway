package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMergeSchemas(t *testing.T) {
	sources := []Source{
		{
			Name: "users",
			Schema: &SchemaData{
				QueryType: &TypeName{Name: "Query"},
				Types: []FullType{
					{
						Kind: "OBJECT",
						Name: "Query",
						Fields: []Field{
							{Name: "users", Type: TypeRef{Kind: "LIST"}},
							{Name: "user", Type: TypeRef{Kind: "OBJECT"}},
						},
					},
					{
						Kind: "OBJECT",
						Name: "User",
						Fields: []Field{
							{Name: "id", Type: TypeRef{Kind: "SCALAR"}},
							{Name: "name", Type: TypeRef{Kind: "SCALAR"}},
						},
					},
				},
			},
		},
		{
			Name: "orders",
			Schema: &SchemaData{
				QueryType:    &TypeName{Name: "Query"},
				MutationType: &TypeName{Name: "Mutation"},
				Types: []FullType{
					{
						Kind: "OBJECT",
						Name: "Query",
						Fields: []Field{
							{Name: "orders", Type: TypeRef{Kind: "LIST"}},
							{Name: "order", Type: TypeRef{Kind: "OBJECT"}},
						},
					},
					{
						Kind: "OBJECT",
						Name: "Mutation",
						Fields: []Field{
							{Name: "createOrder", Type: TypeRef{Kind: "OBJECT"}},
						},
					},
					{
						Kind: "OBJECT",
						Name: "Order",
						Fields: []Field{
							{Name: "id", Type: TypeRef{Kind: "SCALAR"}},
							{Name: "total", Type: TypeRef{Kind: "SCALAR"}},
						},
					},
				},
			},
		},
	}

	merged, err := MergeSchemas(sources)
	if err != nil {
		t.Fatalf("MergeSchemas error: %v", err)
	}

	// Verify field ownership
	if merged.FieldOwner["Query.users"] != "users" {
		t.Errorf("Query.users owner = %q, want %q", merged.FieldOwner["Query.users"], "users")
	}
	if merged.FieldOwner["Query.orders"] != "orders" {
		t.Errorf("Query.orders owner = %q, want %q", merged.FieldOwner["Query.orders"], "orders")
	}
	if merged.FieldOwner["Mutation.createOrder"] != "orders" {
		t.Errorf("Mutation.createOrder owner = %q, want %q", merged.FieldOwner["Mutation.createOrder"], "orders")
	}

	// Verify merged schema has all query fields
	queryFields := 0
	for _, typ := range merged.Schema.Types {
		if typ.Name == "Query" {
			queryFields = len(typ.Fields)
		}
	}
	if queryFields != 4 {
		t.Errorf("merged Query fields = %d, want 4", queryFields)
	}

	// Verify mutation type exists
	if merged.Schema.MutationType == nil {
		t.Error("expected MutationType to be set")
	}
}

func TestMergeSchemasConflict(t *testing.T) {
	sources := []Source{
		{
			Name: "service1",
			Schema: &SchemaData{
				QueryType: &TypeName{Name: "Query"},
				Types: []FullType{
					{Kind: "OBJECT", Name: "Query", Fields: []Field{{Name: "users"}}},
				},
			},
		},
		{
			Name: "service2",
			Schema: &SchemaData{
				QueryType: &TypeName{Name: "Query"},
				Types: []FullType{
					{Kind: "OBJECT", Name: "Query", Fields: []Field{{Name: "users"}}},
				},
			},
		},
	}

	_, err := MergeSchemas(sources)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected conflict error, got: %v", err)
	}
}

func TestMergeSchemasInsufficientSources(t *testing.T) {
	sources := []Source{
		{
			Name: "only-one",
			Schema: &SchemaData{
				QueryType: &TypeName{Name: "Query"},
				Types: []FullType{
					{Kind: "OBJECT", Name: "Query", Fields: []Field{{Name: "foo"}}},
				},
			},
		},
	}

	_, err := MergeSchemas(sources)
	if err == nil {
		t.Fatal("expected error for insufficient sources")
	}
}

func TestSplitQuerySingleSource(t *testing.T) {
	fieldOwner := map[string]string{
		"Query.users": "users-service",
		"Query.user":  "users-service",
	}

	query := `{ users { id name } user(id: "1") { name } }`
	subQueries, err := SplitQuery(query, "", nil, fieldOwner)
	if err != nil {
		t.Fatalf("SplitQuery error: %v", err)
	}

	if len(subQueries) != 1 {
		t.Fatalf("expected 1 sub-query, got %d", len(subQueries))
	}
	if subQueries[0].SourceName != "users-service" {
		t.Errorf("source = %q, want %q", subQueries[0].SourceName, "users-service")
	}
	// Single source should forward the original query
	if subQueries[0].Query != query {
		t.Errorf("expected original query to be forwarded")
	}
}

func TestSplitQueryMultiSource(t *testing.T) {
	fieldOwner := map[string]string{
		"Query.users":  "users-service",
		"Query.orders": "orders-service",
	}

	query := `{ users { id } orders { total } }`
	subQueries, err := SplitQuery(query, "", nil, fieldOwner)
	if err != nil {
		t.Fatalf("SplitQuery error: %v", err)
	}

	if len(subQueries) != 2 {
		t.Fatalf("expected 2 sub-queries, got %d", len(subQueries))
	}

	sources := make(map[string]bool)
	for _, sq := range subQueries {
		sources[sq.SourceName] = true
	}
	if !sources["users-service"] || !sources["orders-service"] {
		t.Errorf("expected both services, got %v", sources)
	}
}

func TestSplitQueryUnknownField(t *testing.T) {
	fieldOwner := map[string]string{
		"Query.users": "users-service",
	}

	query := `{ users { id } unknown_field }`
	_, err := SplitQuery(query, "", nil, fieldOwner)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestExtractFieldBlocks(t *testing.T) {
	query := `{ users { id name } orders(limit: 10) { total } simple }`
	blocks := extractFieldBlocks(query)

	if _, ok := blocks["users"]; !ok {
		t.Error("expected 'users' block")
	}
	if _, ok := blocks["orders"]; !ok {
		t.Error("expected 'orders' block")
	}
	if _, ok := blocks["simple"]; !ok {
		t.Error("expected 'simple' block")
	}

	// Check that users block includes the selection set
	if !strings.Contains(blocks["users"], "id") {
		t.Errorf("users block should contain 'id': %q", blocks["users"])
	}
}

func TestIsIntrospectionQuery(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{`{ __schema { types { name } } }`, true},
		{`{ __type(name: "User") { fields { name } } }`, true},
		{`{ users { id } }`, false},
		{`mutation { createUser(name: "test") { id } }`, false},
	}

	for _, tt := range tests {
		got := isIntrospectionQuery(tt.query)
		if got != tt.want {
			t.Errorf("isIntrospectionQuery(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestExecutorMergeResponses(t *testing.T) {
	executor := NewExecutor(nil, nil)

	results := []result{
		{
			resp: &GraphQLResponse{
				Data: json.RawMessage(`{"users":[{"id":"1"}]}`),
			},
		},
		{
			resp: &GraphQLResponse{
				Data: json.RawMessage(`{"orders":[{"total":100}]}`),
			},
		},
	}

	merged, err := executor.mergeResponses(results)
	if err != nil {
		t.Fatalf("mergeResponses error: %v", err)
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(merged.Data, &data); err != nil {
		t.Fatalf("unmarshal merged data: %v", err)
	}

	if _, ok := data["users"]; !ok {
		t.Error("expected 'users' in merged data")
	}
	if _, ok := data["orders"]; !ok {
		t.Error("expected 'orders' in merged data")
	}
}

func TestHandlerServeHTTP(t *testing.T) {
	// Set up two mock GraphQL backends
	usersBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"users": []map[string]string{{"id": "1", "name": "Alice"}},
			},
		})
	}))
	defer usersBackend.Close()

	ordersBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"orders": []map[string]interface{}{{"id": "o1", "total": 100}},
			},
		})
	}))
	defer ordersBackend.Close()

	// Create stitcher with pre-built merged schema (skip introspection)
	sourceURLs := map[string]string{
		"users":  usersBackend.URL,
		"orders": ordersBackend.URL,
	}

	merged := &MergedSchema{
		FieldOwner: map[string]string{
			"Query.users":  "users",
			"Query.orders": "orders",
		},
	}

	stitcher := &Stitcher{
		routeID:         "test",
		refreshInterval: 24 * time.Hour, // prevent refresh
		lastRefresh:     time.Now(),
	}
	stitcher.merged = merged
	stitcher.executor = NewExecutor(sourceURLs, nil)

	handler := NewHandler(stitcher)

	// Test a query that hits both backends
	reqBody := `{"query":"{ users { id } orders { total } }"}`
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp GraphQLResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(resp.Errors) > 0 {
		t.Errorf("unexpected errors: %v", resp.Errors)
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}

	if _, ok := data["users"]; !ok {
		t.Error("expected 'users' in response data")
	}
	if _, ok := data["orders"]; !ok {
		t.Error("expected 'orders' in response data")
	}
}

func TestHandlerMethodNotAllowed(t *testing.T) {
	handler := NewHandler(&Stitcher{})

	req := httptest.NewRequest("GET", "/graphql", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandlerInvalidJSON(t *testing.T) {
	handler := NewHandler(&Stitcher{})

	req := httptest.NewRequest("POST", "/graphql", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandlerMissingQuery(t *testing.T) {
	handler := NewHandler(&Stitcher{})

	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestIntrospectSchema(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"__schema": map[string]interface{}{
					"queryType": map[string]string{"name": "Query"},
					"types": []map[string]interface{}{
						{
							"kind": "OBJECT",
							"name": "Query",
							"fields": []map[string]interface{}{
								{"name": "hello", "type": map[string]interface{}{"kind": "SCALAR", "name": "String"}},
							},
						},
					},
				},
			},
		})
	}))
	defer backend.Close()

	schema, err := IntrospectSchema(context.Background(), backend.URL, nil)
	if err != nil {
		t.Fatalf("IntrospectSchema error: %v", err)
	}

	if schema.QueryType == nil || schema.QueryType.Name != "Query" {
		t.Error("expected Query type")
	}
	if len(schema.Types) != 1 {
		t.Errorf("expected 1 type, got %d", len(schema.Types))
	}
	if schema.Types[0].Fields[0].Name != "hello" {
		t.Errorf("expected 'hello' field, got %q", schema.Types[0].Fields[0].Name)
	}
}

func TestFederationByRoute(t *testing.T) {
	m := NewFederationByRoute()

	if ids := m.RouteIDs(); len(ids) != 0 {
		t.Errorf("expected 0 routes, got %d", len(ids))
	}

	// GetHandler for non-existent route
	if h := m.GetHandler("unknown"); h != nil {
		t.Error("expected nil for unknown route")
	}
}
