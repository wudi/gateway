package graphql

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestAPQCache_LookupMiss(t *testing.T) {
	c, err := NewAPQCache(100)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := c.Lookup("nonexistent")
	if ok {
		t.Fatal("expected miss")
	}
	stats := c.Stats()
	if stats["misses"].(int64) != 1 {
		t.Fatalf("expected 1 miss, got %v", stats["misses"])
	}
}

func TestAPQCache_RegisterAndLookup(t *testing.T) {
	c, err := NewAPQCache(100)
	if err != nil {
		t.Fatal(err)
	}
	query := "{ hello }"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(query)))

	if !c.Register(hash, query) {
		t.Fatal("register should succeed")
	}

	got, ok := c.Lookup(hash)
	if !ok {
		t.Fatal("expected hit")
	}
	if got != query {
		t.Fatalf("expected %q, got %q", query, got)
	}
	stats := c.Stats()
	if stats["hits"].(int64) != 1 {
		t.Fatalf("expected 1 hit, got %v", stats["hits"])
	}
	if stats["registers"].(int64) != 1 {
		t.Fatalf("expected 1 register, got %v", stats["registers"])
	}
}

func TestAPQCache_RegisterBadHash(t *testing.T) {
	c, err := NewAPQCache(100)
	if err != nil {
		t.Fatal(err)
	}
	if c.Register("badhash", "{ hello }") {
		t.Fatal("register with bad hash should fail")
	}
}

func TestAPQ_Integration_RegisterThenLookup(t *testing.T) {
	query := "{ users { id name } }"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(query)))

	p, err := New(config.GraphQLConfig{
		Enabled:          true,
		PersistedQueries: config.PersistedQueriesConfig{Enabled: true, MaxSize: 100},
	})
	if err != nil {
		t.Fatal(err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"data":{"users":[]}}`))
	})

	handler := p.Middleware()(backend)

	// Step 1: Register the query (hash + query)
	regBody := map[string]interface{}{
		"query": query,
		"extensions": map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    1,
				"sha256Hash": hash,
			},
		},
	}
	regJSON, _ := json.Marshal(regBody)
	req := httptest.NewRequest("POST", "/graphql", bytes.NewReader(regJSON))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("register step: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Step 2: Lookup by hash only (no query)
	lookupBody := map[string]interface{}{
		"extensions": map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    1,
				"sha256Hash": hash,
			},
		},
	}
	lookupJSON, _ := json.Marshal(lookupBody)
	req2 := httptest.NewRequest("POST", "/graphql", bytes.NewReader(lookupJSON))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != 200 {
		t.Fatalf("lookup step: expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// Verify backend received the resolved query
	respBody, _ := io.ReadAll(rec2.Body)
	if !bytes.Contains(respBody, []byte("users")) {
		t.Fatalf("expected backend response, got: %s", string(respBody))
	}
}

func TestAPQ_PersistedQueryNotFound(t *testing.T) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("{ unknown }")))

	p, err := New(config.GraphQLConfig{
		Enabled:          true,
		PersistedQueries: config.PersistedQueriesConfig{Enabled: true, MaxSize: 100},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("backend should not be called")
	}))

	body := map[string]interface{}{
		"extensions": map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    1,
				"sha256Hash": hash,
			},
		},
	}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/graphql", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	errors, ok := resp["errors"].([]interface{})
	if !ok || len(errors) == 0 {
		t.Fatal("expected errors in response")
	}
	errMsg := errors[0].(map[string]interface{})["message"].(string)
	if errMsg != "PersistedQueryNotFound" {
		t.Fatalf("expected PersistedQueryNotFound, got %q", errMsg)
	}
}

func TestAPQ_HashMismatch(t *testing.T) {
	p, err := New(config.GraphQLConfig{
		Enabled:          true,
		PersistedQueries: config.PersistedQueriesConfig{Enabled: true, MaxSize: 100},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("backend should not be called")
	}))

	body := map[string]interface{}{
		"query": "{ users { id } }",
		"extensions": map[string]interface{}{
			"persistedQuery": map[string]interface{}{
				"version":    1,
				"sha256Hash": "badhash000",
			},
		},
	}
	bodyJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/graphql", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestAPQ_DefaultMaxSize(t *testing.T) {
	c, err := NewAPQCache(0) // should default to 1000
	if err != nil {
		t.Fatal(err)
	}
	query := "{ test }"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(query)))
	if !c.Register(hash, query) {
		t.Fatal("register should succeed with default max size")
	}
}
