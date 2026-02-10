package thrift

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestCompilePathPattern(t *testing.T) {
	tests := []struct {
		pattern    string
		path       string
		wantMatch  bool
		wantParams map[string]string
	}{
		{"/users", "/users", true, map[string]string{}},
		{"/users/:id", "/users/123", true, map[string]string{"id": "123"}},
		{"/users/{id}", "/users/456", true, map[string]string{"id": "456"}},
		{"/users/:id/posts/:post_id", "/users/1/posts/2", true, map[string]string{"id": "1", "post_id": "2"}},
		{"/users/:id", "/users", false, nil},
		{"/users/:id", "/users/123/extra", false, nil},
		{"/api/v1/users", "/api/v1/users", true, map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"→"+tt.path, func(t *testing.T) {
			re, paramNames, err := compilePathPattern(tt.pattern)
			if err != nil {
				t.Fatalf("compile failed: %v", err)
			}

			matches := re.FindStringSubmatch(tt.path)
			if tt.wantMatch && matches == nil {
				t.Error("expected match, got none")
				return
			}
			if !tt.wantMatch && matches != nil {
				t.Error("expected no match, got one")
				return
			}
			if !tt.wantMatch {
				return
			}

			params := make(map[string]string)
			for i, name := range paramNames {
				if i+1 < len(matches) {
					params[name] = matches[i+1]
				}
			}
			for k, want := range tt.wantParams {
				if got := params[k]; got != want {
					t.Errorf("param %s = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func TestRESTMapperMatch(t *testing.T) {
	mapper, err := newRESTMapper("UserService", []config.ThriftMethodMapping{
		{HTTPMethod: "GET", HTTPPath: "/users/:id", ThriftMethod: "GetUser", Body: ""},
		{HTTPMethod: "POST", HTTPPath: "/users", ThriftMethod: "CreateUser", Body: "*"},
		{HTTPMethod: "PUT", HTTPPath: "/users/:id", ThriftMethod: "UpdateUser", Body: "*"},
		{HTTPMethod: "DELETE", HTTPPath: "/users/:id", ThriftMethod: "DeleteUser", Body: ""},
	})
	if err != nil {
		t.Fatalf("failed to create mapper: %v", err)
	}

	tests := []struct {
		method      string
		path        string
		wantMethod  string
		wantMatch   bool
		wantParams  map[string]string
	}{
		{"GET", "/users/123", "GetUser", true, map[string]string{"id": "123"}},
		{"POST", "/users", "CreateUser", true, map[string]string{}},
		{"PUT", "/users/456", "UpdateUser", true, map[string]string{"id": "456"}},
		{"DELETE", "/users/789", "DeleteUser", true, map[string]string{"id": "789"}},
		{"GET", "/users", "", false, nil},       // no match (GET /users not configured)
		{"PATCH", "/users/1", "", false, nil},    // no match (PATCH not configured)
		{"GET", "/other/path", "", false, nil},   // no match
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			result := mapper.match(tt.method, tt.path)
			if tt.wantMatch && result == nil {
				t.Fatal("expected match, got nil")
			}
			if !tt.wantMatch && result != nil {
				t.Fatal("expected no match, got one")
			}
			if !tt.wantMatch {
				return
			}
			if result.thriftMethod != tt.wantMethod {
				t.Errorf("method = %q, want %q", result.thriftMethod, tt.wantMethod)
			}
			for k, want := range tt.wantParams {
				if got := result.pathParams[k]; got != want {
					t.Errorf("param %s = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func TestRESTMapperBuildRequestBody(t *testing.T) {
	mapper, err := newRESTMapper("UserService", []config.ThriftMethodMapping{
		{HTTPMethod: "GET", HTTPPath: "/users/:id", ThriftMethod: "GetUser", Body: ""},
		{HTTPMethod: "POST", HTTPPath: "/users", ThriftMethod: "CreateUser", Body: "*"},
		{HTTPMethod: "PUT", HTTPPath: "/users/:id", ThriftMethod: "UpdateUser", Body: "user"},
	})
	if err != nil {
		t.Fatalf("failed to create mapper: %v", err)
	}

	t.Run("path_params_only", func(t *testing.T) {
		result := mapper.match("GET", "/users/abc")
		r := &http.Request{URL: &url.URL{RawQuery: ""}}
		body, err := mapper.buildRequestBody(r, result, nil)
		if err != nil {
			t.Fatalf("buildRequestBody failed: %v", err)
		}
		assertJSONContains(t, body, "id", "abc")
	})

	t.Run("body_star", func(t *testing.T) {
		result := mapper.match("POST", "/users")
		r := &http.Request{URL: &url.URL{RawQuery: ""}}
		reqBody := []byte(`{"name":"John","age":30}`)
		body, err := mapper.buildRequestBody(r, result, reqBody)
		if err != nil {
			t.Fatalf("buildRequestBody failed: %v", err)
		}
		assertJSONContains(t, body, "name", "John")
	})

	t.Run("body_nested", func(t *testing.T) {
		result := mapper.match("PUT", "/users/xyz")
		r := &http.Request{URL: &url.URL{RawQuery: ""}}
		reqBody := []byte(`{"name":"Jane"}`)
		body, err := mapper.buildRequestBody(r, result, reqBody)
		if err != nil {
			t.Fatalf("buildRequestBody failed: %v", err)
		}
		assertJSONContains(t, body, "id", "xyz")
		// user field should be a nested object.
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			t.Fatal(err)
		}
		userObj, ok := data["user"].(map[string]interface{})
		if !ok {
			t.Fatal("expected 'user' to be an object")
		}
		if userObj["name"] != "Jane" {
			t.Errorf("user.name = %v, want Jane", userObj["name"])
		}
	})

	t.Run("query_params", func(t *testing.T) {
		result := mapper.match("GET", "/users/abc")
		r := &http.Request{URL: &url.URL{RawQuery: "verbose=true"}}
		body, err := mapper.buildRequestBody(r, result, nil)
		if err != nil {
			t.Fatalf("buildRequestBody failed: %v", err)
		}
		assertJSONContains(t, body, "id", "abc")
		assertJSONContains(t, body, "verbose", "true")
	})
}

func TestNewRESTMapperEmpty(t *testing.T) {
	mapper, err := newRESTMapper("Svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mapper != nil {
		t.Error("expected nil mapper for empty mappings")
	}
}

// assertJSONContains checks that the JSON data contains a key with the expected string value.
func assertJSONContains(t *testing.T, data []byte, key, expected string) {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	if val, ok := m[key]; !ok {
		t.Errorf("missing key %q in %s", key, string(data))
	} else if str, ok := val.(string); ok && str != expected {
		t.Errorf("key %q = %q, want %q", key, str, expected)
	}
}
