package grpc

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestCompilePathPattern(t *testing.T) {
	tests := []struct {
		name       string
		pattern    string
		testPath   string
		wantMatch  bool
		wantParams map[string]string
	}{
		{
			name:       "literal path",
			pattern:    "/users",
			testPath:   "/users",
			wantMatch:  true,
			wantParams: map[string]string{},
		},
		{
			name:       "colon param style",
			pattern:    "/users/:user_id",
			testPath:   "/users/123",
			wantMatch:  true,
			wantParams: map[string]string{"user_id": "123"},
		},
		{
			name:       "brace param style",
			pattern:    "/users/{user_id}",
			testPath:   "/users/456",
			wantMatch:  true,
			wantParams: map[string]string{"user_id": "456"},
		},
		{
			name:       "multiple params",
			pattern:    "/users/:user_id/posts/:post_id",
			testPath:   "/users/123/posts/456",
			wantMatch:  true,
			wantParams: map[string]string{"user_id": "123", "post_id": "456"},
		},
		{
			name:      "no match - different path",
			pattern:   "/users/:user_id",
			testPath:  "/posts/123",
			wantMatch: false,
		},
		{
			name:      "no match - missing segment",
			pattern:   "/users/:user_id/posts",
			testPath:  "/users/123",
			wantMatch: false,
		},
		{
			name:       "trailing slash in test path",
			pattern:    "/users/:user_id",
			testPath:   "/users/123/",
			wantMatch:  false, // Strict matching without trailing slash
			wantParams: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, paramNames, err := compilePathPattern(tt.pattern)
			if err != nil {
				t.Fatalf("compilePathPattern failed: %v", err)
			}

			matches := re.FindStringSubmatch(tt.testPath)
			gotMatch := matches != nil

			if gotMatch != tt.wantMatch {
				t.Errorf("match = %v, want %v", gotMatch, tt.wantMatch)
				return
			}

			if !tt.wantMatch {
				return
			}

			// Extract params
			gotParams := make(map[string]string)
			for i, name := range paramNames {
				if i+1 < len(matches) {
					gotParams[name] = matches[i+1]
				}
			}

			for k, v := range tt.wantParams {
				if gotParams[k] != v {
					t.Errorf("param %q = %q, want %q", k, gotParams[k], v)
				}
			}
		})
	}
}

func TestRESTMapper(t *testing.T) {
	mappings := []config.GRPCMethodMapping{
		{HTTPMethod: "GET", HTTPPath: "/users/:user_id", GRPCMethod: "GetUser", Body: ""},
		{HTTPMethod: "POST", HTTPPath: "/users", GRPCMethod: "CreateUser", Body: "*"},
		{HTTPMethod: "PUT", HTTPPath: "/users/:user_id", GRPCMethod: "UpdateUser", Body: "*"},
		{HTTPMethod: "DELETE", HTTPPath: "/users/:user_id", GRPCMethod: "DeleteUser", Body: ""},
		{HTTPMethod: "GET", HTTPPath: "/users/:user_id/posts/:post_id", GRPCMethod: "GetUserPost", Body: ""},
	}

	mapper, err := newRESTMapper("myapp.UserService", mappings)
	if err != nil {
		t.Fatalf("newRESTMapper failed: %v", err)
	}

	tests := []struct {
		name           string
		method         string
		path           string
		wantMatch      bool
		wantGRPCMethod string
		wantParams     map[string]string
	}{
		{
			name:           "GET single user",
			method:         "GET",
			path:           "/users/123",
			wantMatch:      true,
			wantGRPCMethod: "GetUser",
			wantParams:     map[string]string{"user_id": "123"},
		},
		{
			name:           "POST create user",
			method:         "POST",
			path:           "/users",
			wantMatch:      true,
			wantGRPCMethod: "CreateUser",
			wantParams:     map[string]string{},
		},
		{
			name:           "PUT update user",
			method:         "PUT",
			path:           "/users/456",
			wantMatch:      true,
			wantGRPCMethod: "UpdateUser",
			wantParams:     map[string]string{"user_id": "456"},
		},
		{
			name:           "DELETE user",
			method:         "DELETE",
			path:           "/users/789",
			wantMatch:      true,
			wantGRPCMethod: "DeleteUser",
			wantParams:     map[string]string{"user_id": "789"},
		},
		{
			name:           "GET nested resource",
			method:         "GET",
			path:           "/users/123/posts/456",
			wantMatch:      true,
			wantGRPCMethod: "GetUserPost",
			wantParams:     map[string]string{"user_id": "123", "post_id": "456"},
		},
		{
			name:      "no match - wrong method",
			method:    "PATCH",
			path:      "/users/123",
			wantMatch: false,
		},
		{
			name:      "no match - unknown path",
			method:    "GET",
			path:      "/orders/123",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapper.match(tt.method, tt.path)

			if tt.wantMatch {
				if result == nil {
					t.Fatal("expected match, got nil")
				}
				if result.grpcMethod != tt.wantGRPCMethod {
					t.Errorf("grpcMethod = %q, want %q", result.grpcMethod, tt.wantGRPCMethod)
				}
				for k, v := range tt.wantParams {
					if result.pathParams[k] != v {
						t.Errorf("param %q = %q, want %q", k, result.pathParams[k], v)
					}
				}
			} else {
				if result != nil {
					t.Errorf("expected no match, got %+v", result)
				}
			}
		})
	}
}

func TestBuildRequestBody(t *testing.T) {
	mappings := []config.GRPCMethodMapping{
		{HTTPMethod: "GET", HTTPPath: "/users/:user_id", GRPCMethod: "GetUser", Body: ""},
		{HTTPMethod: "POST", HTTPPath: "/users", GRPCMethod: "CreateUser", Body: "*"},
		{HTTPMethod: "PUT", HTTPPath: "/users/:user_id", GRPCMethod: "UpdateUser", Body: "user"},
	}

	mapper, err := newRESTMapper("myapp.UserService", mappings)
	if err != nil {
		t.Fatalf("newRESTMapper failed: %v", err)
	}

	tests := []struct {
		name        string
		method      string
		path        string
		query       string
		body        string
		wantContain []string
	}{
		{
			name:        "GET with path param only",
			method:      "GET",
			path:        "/users/123",
			query:       "",
			body:        "",
			wantContain: []string{`"user_id":"123"`},
		},
		{
			name:        "GET with query params",
			method:      "GET",
			path:        "/users/123",
			query:       "include_posts=true&limit=10",
			body:        "",
			wantContain: []string{`"user_id":"123"`, `"include_posts":"true"`, `"limit":"10"`},
		},
		{
			name:        "POST with body merged",
			method:      "POST",
			path:        "/users",
			query:       "",
			body:        `{"name":"John","email":"john@example.com"}`,
			wantContain: []string{`"name":"John"`, `"email":"john@example.com"`},
		},
		{
			name:        "PUT with body nested under field",
			method:      "PUT",
			path:        "/users/123",
			query:       "",
			body:        `{"name":"Jane"}`,
			wantContain: []string{`"user_id":"123"`, `"user":`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapper.match(tt.method, tt.path)
			if result == nil {
				t.Fatal("expected match")
			}

			u, _ := url.Parse("http://example.com" + tt.path)
			if tt.query != "" {
				u.RawQuery = tt.query
			}

			req := &http.Request{
				Method: tt.method,
				URL:    u,
			}

			body, err := mapper.buildRequestBody(req, result, []byte(tt.body))
			if err != nil {
				t.Fatalf("buildRequestBody failed: %v", err)
			}

			bodyStr := string(body)
			for _, want := range tt.wantContain {
				if !strings.Contains(bodyStr, want) {
					t.Errorf("body %q does not contain %q", bodyStr, want)
				}
			}
		})
	}
}

func TestSetNestedField(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value interface{}
		want  string
	}{
		{
			name:  "simple field",
			key:   "name",
			value: "John",
			want:  `{"name":"John"}`,
		},
		{
			name:  "nested field",
			key:   "user.name",
			value: "Jane",
			want:  `{"user":{"name":"Jane"}}`,
		},
		{
			name:  "deeply nested",
			key:   "a.b.c",
			value: "deep",
			want:  `{"a":{"b":{"c":"deep"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := make(map[string]interface{})
			setNestedField(data, tt.key, tt.value)

			// Simple check - just verify the value is accessible
			parts := strings.Split(tt.key, ".")
			current := interface{}(data)
			for _, part := range parts {
				m, ok := current.(map[string]interface{})
				if !ok {
					t.Fatalf("expected map at %q", part)
				}
				current = m[part]
			}

			if current != tt.value {
				t.Errorf("got %v, want %v", current, tt.value)
			}
		})
	}
}
