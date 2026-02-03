package variables

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolver(t *testing.T) {
	r := NewResolver()

	ctx := &Context{
		RequestID: "test-123",
		RouteID:   "users-api",
		StartTime: time.Now(),
	}

	tests := []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "request_id",
			template: "$request_id",
			want:     "test-123",
		},
		{
			name:     "route_id",
			template: "$route_id",
			want:     "users-api",
		},
		{
			name:     "multiple variables",
			template: "Request: $request_id, Route: $route_id",
			want:     "Request: test-123, Route: users-api",
		},
		{
			name:     "no variables",
			template: "plain text",
			want:     "plain text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Resolve(tt.template, ctx)
			if got != tt.want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.template, got, tt.want)
			}
		})
	}
}

func TestResolverHTTPVariables(t *testing.T) {
	r := NewResolver()

	req := httptest.NewRequest("GET", "/api/users?page=1&limit=10", nil)
	req.Header.Set("X-Custom-Header", "custom-value")
	req.Header.Set("User-Agent", "test-agent")

	ctx := NewContext(req)
	ctx.RequestID = "req-456"

	tests := []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "request_method",
			template: "$request_method",
			want:     "GET",
		},
		{
			name:     "request_path",
			template: "$request_path",
			want:     "/api/users",
		},
		{
			name:     "query_string",
			template: "$query_string",
			want:     "page=1&limit=10",
		},
		{
			name:     "http header",
			template: "$http_x_custom_header",
			want:     "custom-value",
		},
		{
			name:     "http user agent",
			template: "$http_user_agent",
			want:     "test-agent",
		},
		{
			name:     "arg_page",
			template: "$arg_page",
			want:     "1",
		},
		{
			name:     "arg_limit",
			template: "$arg_limit",
			want:     "10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Resolve(tt.template, ctx)
			if got != tt.want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.template, got, tt.want)
			}
		})
	}
}

func TestResolverCustomVariables(t *testing.T) {
	r := NewResolver()

	r.RegisterCustom("custom_var", func(ctx *Context) string {
		return "custom-value"
	})

	ctx := &Context{}

	got := r.Resolve("Value: $custom_var", ctx)
	if got != "Value: custom-value" {
		t.Errorf("expected 'Value: custom-value', got %q", got)
	}
}

func TestParser(t *testing.T) {
	p := NewParser()

	tests := []struct {
		name     string
		template string
		vars     []string
	}{
		{
			name:     "single var",
			template: "$request_id",
			vars:     []string{"request_id"},
		},
		{
			name:     "multiple vars",
			template: "$foo $bar $baz",
			vars:     []string{"foo", "bar", "baz"},
		},
		{
			name:     "no vars",
			template: "plain text",
			vars:     nil,
		},
		{
			name:     "mixed content",
			template: "Request $request_id from $remote_addr",
			vars:     []string{"request_id", "remote_addr"},
		},
		{
			name:     "duplicate vars",
			template: "$foo $foo",
			vars:     []string{"foo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.Extract(tt.template)
			if len(got) != len(tt.vars) {
				t.Errorf("Extract(%q) = %v, want %v", tt.template, got, tt.vars)
				return
			}
			for i, v := range tt.vars {
				if got[i] != v {
					t.Errorf("Extract(%q)[%d] = %q, want %q", tt.template, i, got[i], v)
				}
			}
		})
	}
}

func TestNormalizeHeaderName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"x_custom_header", "X-Custom-Header"},
		{"content_type", "Content-Type"},
		{"authorization", "Authorization"},
		{"x_forwarded_for", "X-Forwarded-For"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeHeaderName(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeHeaderName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDynamic(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		suffix string
		ok     bool
	}{
		{"http_x_custom", "http", "x_custom", true},
		{"arg_page", "arg", "page", true},
		{"cookie_session_id", "cookie", "session_id", true},
		{"route_param_user_id", "route_param", "user_id", true},
		{"jwt_claim_sub", "jwt_claim", "sub", true},
		{"request_id", "", "", false},
		{"unknown_var", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, suffix, ok := ParseDynamic(tt.name)
			if ok != tt.ok {
				t.Errorf("ParseDynamic(%q) ok = %v, want %v", tt.name, ok, tt.ok)
			}
			if prefix != tt.prefix {
				t.Errorf("ParseDynamic(%q) prefix = %q, want %q", tt.name, prefix, tt.prefix)
			}
			if suffix != tt.suffix {
				t.Errorf("ParseDynamic(%q) suffix = %q, want %q", tt.name, suffix, tt.suffix)
			}
		})
	}
}

func TestIdentityVariables(t *testing.T) {
	r := NewResolver()

	ctx := &Context{
		Identity: &Identity{
			ClientID: "client-123",
			AuthType: "jwt",
			Claims: map[string]interface{}{
				"sub": "user-456",
				"iss": "https://auth.example.com",
			},
		},
	}

	tests := []struct {
		template string
		want     string
	}{
		{"$auth_client_id", "client-123"},
		{"$auth_type", "jwt"},
		{"$jwt_claim_sub", "user-456"},
		{"$jwt_claim_iss", "https://auth.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.template, func(t *testing.T) {
			got := r.Resolve(tt.template, ctx)
			if got != tt.want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.template, got, tt.want)
			}
		})
	}
}

func BenchmarkResolve(b *testing.B) {
	r := NewResolver()
	ctx := &Context{
		RequestID: "test-123",
		RouteID:   "users-api",
	}
	template := "Request: $request_id, Route: $route_id"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Resolve(template, ctx)
	}
}
