package variables

import (
	crypto_tls "crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseTemplate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		hasVars  bool
		nParts   int
	}{
		{"no vars", "plain text", false, 1},
		{"single var", "$request_id", true, 1},
		{"var at start", "$method /path", true, 2},
		{"var at end", "path: $request_path", true, 2},
		{"multiple vars", "$method $path $status", true, 5},
		{"vars in template", "Method: $request_method Path: $request_path", true, 4},
		{"adjacent vars", "$foo$bar", true, 2},
		{"empty string", "", false, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := ParseTemplate(tt.input)
			if tmpl.HasVars != tt.hasVars {
				t.Errorf("HasVars = %v, want %v", tmpl.HasVars, tt.hasVars)
			}
			if len(tmpl.Parts) != tt.nParts {
				t.Errorf("len(Parts) = %d, want %d", len(tmpl.Parts), tt.nParts)
			}
			if tmpl.Raw != tt.input {
				t.Errorf("Raw = %q, want %q", tmpl.Raw, tt.input)
			}
		})
	}
}

func TestTemplateRender(t *testing.T) {
	t.Run("with vars", func(t *testing.T) {
		tmpl := ParseTemplate("Hello $name, status: $status")
		vals := map[string]string{
			"name":   "world",
			"status": "200",
		}
		got := tmpl.Render(func(name string) string {
			return vals[name]
		})
		want := "Hello world, status: 200"
		if got != want {
			t.Errorf("Render = %q, want %q", got, want)
		}
	})

	t.Run("no vars returns raw", func(t *testing.T) {
		tmpl := ParseTemplate("plain text")
		got := tmpl.Render(func(name string) string {
			t.Error("getValue should not be called for no-var template")
			return ""
		})
		if got != "plain text" {
			t.Errorf("Render = %q, want %q", got, "plain text")
		}
	})
}

func TestBuiltinVariables_StaticVars(t *testing.T) {
	b := NewBuiltinVariables()

	req := httptest.NewRequest("POST", "/api/users?page=1", nil)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.1.1:12345"

	ctx := &Context{
		Request:              req,
		RequestID:            "req-123",
		RouteID:              "users-api",
		Status:               200,
		BodyBytesSent:        1024,
		UpstreamAddr:         "10.0.0.1:8080",
		UpstreamStatus:       200,
		UpstreamResponseTime: 50 * time.Millisecond,
		ResponseTime:         55 * time.Millisecond,
		ServerPort:           8080,
		APIVersion:           "v2",
		Identity: &Identity{
			ClientID: "client-abc",
			AuthType: "jwt",
			Claims:   map[string]interface{}{"sub": "user-1"},
		},
		CertInfo: &CertInfo{
			Subject:      "CN=client",
			Issuer:       "CN=ca",
			Fingerprint:  "abc123",
			SerialNumber: "42",
			DNSNames:     []string{"client.example.com", "alt.example.com"},
		},
	}

	tests := []struct {
		name string
		want string
		ok   bool
	}{
		{"request_id", "req-123", true},
		{"request_method", "POST", true},
		{"request_path", "/api/users", true},
		{"query_string", "page=1", true},
		{"remote_addr", "192.168.1.1", true},
		{"remote_port", "12345", true},
		{"scheme", "http", true},
		{"host", "example.com", true},
		{"content_type", "application/json", true},
		{"route_id", "users-api", true},
		{"api_version", "v2", true},
		{"status", "200", true},
		{"body_bytes_sent", "1024", true},
		{"upstream_addr", "10.0.0.1:8080", true},
		{"upstream_status", "200", true},
		{"server_port", "8080", true},
		{"auth_client_id", "client-abc", true},
		{"auth_type", "jwt", true},
		{"client_cert_subject", "CN=client", true},
		{"client_cert_issuer", "CN=ca", true},
		{"client_cert_fingerprint", "abc123", true},
		{"client_cert_serial", "42", true},
		{"client_cert_dns_names", "client.example.com,alt.example.com", true},
		{"unknown_var", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := b.Get(tt.name, ctx)
			if ok != tt.ok {
				t.Errorf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("Get(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestBuiltinVariables_TimeVars(t *testing.T) {
	b := NewBuiltinVariables()
	ctx := &Context{}

	timeVars := []string{"time_iso8601", "time_unix", "time_local"}
	for _, name := range timeVars {
		t.Run(name, func(t *testing.T) {
			val, ok := b.Get(name, ctx)
			if !ok {
				t.Errorf("Get(%q) ok = false", name)
			}
			if val == "" {
				t.Errorf("Get(%q) returned empty string", name)
			}
		})
	}
}

func TestBuiltinVariables_AuthNil(t *testing.T) {
	b := NewBuiltinVariables()
	ctx := &Context{}

	val, ok := b.Get("auth_client_id", ctx)
	if !ok {
		t.Error("auth_client_id should return ok=true even with nil identity")
	}
	if val != "" {
		t.Errorf("auth_client_id with nil identity should be empty, got %q", val)
	}
}

func TestBuiltinVariables_CertNil(t *testing.T) {
	b := NewBuiltinVariables()
	ctx := &Context{}

	certVars := []string{
		"client_cert_subject", "client_cert_issuer",
		"client_cert_fingerprint", "client_cert_serial",
		"client_cert_dns_names",
	}
	for _, name := range certVars {
		t.Run(name, func(t *testing.T) {
			val, ok := b.Get(name, ctx)
			if !ok {
				t.Errorf("Get(%q) ok = false with nil CertInfo", name)
			}
			if val != "" {
				t.Errorf("Get(%q) should be empty with nil CertInfo, got %q", name, val)
			}
		})
	}
}

func TestGetDynamic_HTTP(t *testing.T) {
	b := NewBuiltinVariables()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Custom-Header", "custom-value")
	ctx := &Context{Request: req}

	val, ok := b.Get("http_x_custom_header", ctx)
	if !ok || val != "custom-value" {
		t.Errorf("http_x_custom_header = (%q, %v), want (%q, true)", val, ok, "custom-value")
	}
}

func TestGetDynamic_Arg(t *testing.T) {
	b := NewBuiltinVariables()
	req := httptest.NewRequest("GET", "/?page=5&limit=10", nil)
	ctx := &Context{Request: req}

	val, ok := b.Get("arg_page", ctx)
	if !ok || val != "5" {
		t.Errorf("arg_page = (%q, %v), want (%q, true)", val, ok, "5")
	}
}

func TestGetDynamic_Cookie(t *testing.T) {
	b := NewBuiltinVariables()
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "session_id", Value: "abc123"})
	ctx := &Context{Request: req}

	val, ok := b.Get("cookie_session_id", ctx)
	if !ok || val != "abc123" {
		t.Errorf("cookie_session_id = (%q, %v), want (%q, true)", val, ok, "abc123")
	}

	// Missing cookie
	val, ok = b.Get("cookie_missing", ctx)
	if !ok {
		t.Error("cookie_missing should return ok=true")
	}
	if val != "" {
		t.Errorf("cookie_missing = %q, want empty", val)
	}
}

func TestGetDynamic_RouteParam(t *testing.T) {
	b := NewBuiltinVariables()
	ctx := &Context{
		PathParams: map[string]string{"user_id": "42"},
	}

	val, ok := b.Get("route_param_user_id", ctx)
	if !ok {
		t.Error("route_param_user_id should return ok=true")
	}
	if val != "42" {
		t.Errorf("route_param_user_id = %q, want %q", val, "42")
	}
}

func TestGetDynamic_JWTClaim(t *testing.T) {
	b := NewBuiltinVariables()
	ctx := &Context{
		Identity: &Identity{
			Claims: map[string]interface{}{
				"sub":  "user-1",
				"role": "admin",
			},
		},
	}

	val, ok := b.Get("jwt_claim_sub", ctx)
	if !ok || val != "user-1" {
		t.Errorf("jwt_claim_sub = (%q, %v), want (%q, true)", val, ok, "user-1")
	}

	// Missing claim
	val, ok = b.Get("jwt_claim_missing", ctx)
	if !ok {
		t.Error("jwt_claim_missing should return ok=true")
	}
	if val != "" {
		t.Errorf("jwt_claim_missing = %q, want empty", val)
	}
}

func TestContextPoolLifecycle(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	ctx := AcquireContext(req)

	if ctx.Request != req {
		t.Error("AcquireContext should set Request")
	}
	if ctx.StartTime.IsZero() {
		t.Error("AcquireContext should set StartTime")
	}

	ctx.RouteID = "test-route"
	ctx.RequestID = "req-1"

	ReleaseContext(ctx)

	// After release, fields should be zeroed
	if ctx.Request != nil {
		t.Error("ReleaseContext should nil out Request")
	}
	if ctx.RouteID != "" {
		t.Error("ReleaseContext should clear RouteID")
	}
	if ctx.RequestID != "" {
		t.Error("ReleaseContext should clear RequestID")
	}
}

func TestReleaseContextNil(t *testing.T) {
	// Should not panic
	ReleaseContext(nil)
}

func TestContextClone(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	ctx := AcquireContext(req)
	ctx.RouteID = "original"
	ctx.RequestID = "req-1"
	ctx.PathParams = map[string]string{"id": "42"}
	ctx.SetCustom("key", "value")
	ctx.Overrides = &ValueOverrides{
		RateLimitTier: "premium",
	}

	clone := ctx.Clone()

	// Verify clone has the same values
	if clone.RouteID != "original" {
		t.Errorf("clone.RouteID = %q, want %q", clone.RouteID, "original")
	}
	if clone.RequestID != "req-1" {
		t.Errorf("clone.RequestID = %q, want %q", clone.RequestID, "req-1")
	}
	if clone.PathParams["id"] != "42" {
		t.Errorf("clone.PathParams[id] = %q, want %q", clone.PathParams["id"], "42")
	}
	v, ok := clone.GetCustom("key")
	if !ok || v != "value" {
		t.Errorf("clone custom key = (%q, %v), want (%q, true)", v, ok, "value")
	}
	if clone.Overrides == nil || clone.Overrides.RateLimitTier != "premium" {
		t.Error("clone Overrides not copied correctly")
	}

	// Verify independence
	clone.RouteID = "cloned"
	if ctx.RouteID != "original" {
		t.Error("modifying clone should not affect original")
	}
	clone.PathParams["id"] = "99"
	if ctx.PathParams["id"] != "42" {
		t.Error("modifying clone PathParams should not affect original")
	}
	clone.Overrides.RateLimitTier = "basic"
	if ctx.Overrides.RateLimitTier != "premium" {
		t.Error("modifying clone Overrides should not affect original")
	}

	ReleaseContext(clone)
	ReleaseContext(ctx)
}

func TestContextSetGetCustom(t *testing.T) {
	ctx := &Context{}

	// Get on nil custom
	_, ok := ctx.GetCustom("key")
	if ok {
		t.Error("GetCustom should return false for nil custom map")
	}

	// Set initializes the map
	ctx.SetCustom("key", "value")
	v, ok := ctx.GetCustom("key")
	if !ok || v != "value" {
		t.Errorf("GetCustom = (%q, %v), want (%q, true)", v, ok, "value")
	}

	// Overwrite
	ctx.SetCustom("key", "new-value")
	v, _ = ctx.GetCustom("key")
	if v != "new-value" {
		t.Errorf("GetCustom after overwrite = %q, want %q", v, "new-value")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatBytes(tt.input)
			if got != tt.want {
				t.Errorf("FormatBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractClientIP(t *testing.T) {
	t.Run("XFF single", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		if got := ExtractClientIP(req); got != "1.2.3.4" {
			t.Errorf("got %q, want %q", got, "1.2.3.4")
		}
	})

	t.Run("XFF multiple", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
		if got := ExtractClientIP(req); got != "1.2.3.4" {
			t.Errorf("got %q, want %q", got, "1.2.3.4")
		}
	})

	t.Run("X-Real-IP", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Real-IP", "10.0.0.1")
		if got := ExtractClientIP(req); got != "10.0.0.1" {
			t.Errorf("got %q, want %q", got, "10.0.0.1")
		}
	})

	t.Run("RemoteAddr fallback", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		if got := ExtractClientIP(req); got != "192.168.1.1" {
			t.Errorf("got %q, want %q", got, "192.168.1.1")
		}
	})

	t.Run("RemoteAddr no port", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1"
		if got := ExtractClientIP(req); got != "192.168.1.1" {
			t.Errorf("got %q, want %q", got, "192.168.1.1")
		}
	})
}

func TestParserHasVariables(t *testing.T) {
	p := NewParser()

	if p.HasVariables("no vars here") {
		t.Error("HasVariables should return false for plain text")
	}
	if !p.HasVariables("has $var") {
		t.Error("HasVariables should return true when template has variables")
	}
}

func TestParserReplace(t *testing.T) {
	p := NewParser()

	got := p.Replace("Hello $name, you are $status", func(name string) string {
		switch name {
		case "name":
			return "Alice"
		case "status":
			return "active"
		default:
			return ""
		}
	})

	want := "Hello Alice, you are active"
	if got != want {
		t.Errorf("Replace = %q, want %q", got, want)
	}
}

func TestResolverResolveMap(t *testing.T) {
	r := NewResolver()
	ctx := &Context{RequestID: "req-1", RouteID: "route-1"}

	m := map[string]string{
		"id":    "$request_id",
		"route": "$route_id",
		"plain": "no-vars",
	}

	result := r.ResolveMap(m, ctx)
	if result["id"] != "req-1" {
		t.Errorf("id = %q, want %q", result["id"], "req-1")
	}
	if result["route"] != "route-1" {
		t.Errorf("route = %q, want %q", result["route"], "route-1")
	}
	if result["plain"] != "no-vars" {
		t.Errorf("plain = %q, want %q", result["plain"], "no-vars")
	}
}

func TestResolverResolveSlice(t *testing.T) {
	r := NewResolver()
	ctx := &Context{RequestID: "req-1"}

	s := []string{"$request_id", "plain", "id=$request_id"}
	result := r.ResolveSlice(s, ctx)

	if result[0] != "req-1" {
		t.Errorf("[0] = %q, want %q", result[0], "req-1")
	}
	if result[1] != "plain" {
		t.Errorf("[1] = %q, want %q", result[1], "plain")
	}
	if result[2] != "id=req-1" {
		t.Errorf("[2] = %q, want %q", result[2], "id=req-1")
	}
}

func TestCompiledTemplate(t *testing.T) {
	r := NewResolver()
	ct := r.PrecompileTemplate("Request $request_id on route $route_id")

	if !ct.HasVariables() {
		t.Error("CompiledTemplate should have variables")
	}

	ctx := &Context{RequestID: "r-1", RouteID: "rt-1"}
	got := ct.Resolve(ctx)
	want := "Request r-1 on route rt-1"
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestCompiledTemplateNoVars(t *testing.T) {
	r := NewResolver()
	ct := r.PrecompileTemplate("plain text")
	if ct.HasVariables() {
		t.Error("CompiledTemplate should not have variables")
	}
	got := ct.Resolve(&Context{})
	if got != "plain text" {
		t.Errorf("Resolve = %q, want %q", got, "plain text")
	}
}

func TestResolverExtractVariables(t *testing.T) {
	r := NewResolver()
	vars := r.ExtractVariables("$request_id and $route_id")
	if len(vars) != 2 {
		t.Fatalf("len(vars) = %d, want 2", len(vars))
	}
	if vars[0] != "request_id" || vars[1] != "route_id" {
		t.Errorf("vars = %v, want [request_id route_id]", vars)
	}
}

func TestResolverHasVariables(t *testing.T) {
	r := NewResolver()
	if r.HasVariables("plain") {
		t.Error("HasVariables should return false")
	}
	if !r.HasVariables("$var") {
		t.Error("HasVariables should return true")
	}
}

func TestResolverUnregisterCustom(t *testing.T) {
	r := NewResolver()
	r.RegisterCustom("test_var", func(ctx *Context) string {
		return "test-value"
	})

	ctx := &Context{}
	val, ok := r.Get("test_var", ctx)
	if !ok || val != "test-value" {
		t.Errorf("Get before unregister = (%q, %v), want (%q, true)", val, ok, "test-value")
	}

	r.UnregisterCustom("test_var")
	_, ok = r.Get("test_var", ctx)
	if ok {
		t.Error("Get after unregister should return ok=false")
	}
}

func TestResolverContextCustomOverride(t *testing.T) {
	r := NewResolver()
	ctx := &Context{}
	ctx.SetCustom("my_var", "from-context")

	val, ok := r.Get("my_var", ctx)
	if !ok || val != "from-context" {
		t.Errorf("Get = (%q, %v), want (%q, true)", val, ok, "from-context")
	}
}

func TestDefaultResolver(t *testing.T) {
	ctx := &Context{RequestID: "default-req"}
	got := Resolve("$request_id", ctx)
	if got != "default-req" {
		t.Errorf("Resolve = %q, want %q", got, "default-req")
	}

	val, ok := Get("request_id", ctx)
	if !ok || val != "default-req" {
		t.Errorf("Get = (%q, %v), want (%q, true)", val, ok, "default-req")
	}
}

func TestSchemeHTTP(t *testing.T) {
	b := NewBuiltinVariables()
	req := httptest.NewRequest("GET", "/", nil)
	ctx := &Context{Request: req}

	val, ok := b.Get("scheme", ctx)
	if !ok || val != "http" {
		t.Errorf("scheme = (%q, %v), want (http, true)", val, ok)
	}
}

func TestSchemeHTTPS(t *testing.T) {
	b := NewBuiltinVariables()
	req := httptest.NewRequest("GET", "/", nil)
	req.TLS = &crypto_tls.ConnectionState{}
	ctx := &Context{Request: req}

	val, ok := b.Get("scheme", ctx)
	if !ok || val != "https" {
		t.Errorf("scheme = (%q, %v), want (https, true)", val, ok)
	}
}

func TestContentLength(t *testing.T) {
	b := NewBuiltinVariables()
	req := httptest.NewRequest("POST", "/", nil)
	req.ContentLength = 512
	ctx := &Context{Request: req}

	val, ok := b.Get("content_length", ctx)
	if !ok || val != "512" {
		t.Errorf("content_length = (%q, %v), want (%q, true)", val, ok, "512")
	}
}

func TestServerAddr(t *testing.T) {
	b := NewBuiltinVariables()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com:8080"
	ctx := &Context{Request: req}

	val, ok := b.Get("server_addr", ctx)
	if !ok || val != "example.com" {
		t.Errorf("server_addr = (%q, %v), want (%q, true)", val, ok, "example.com")
	}
}

func TestServerAddrNoPort(t *testing.T) {
	b := NewBuiltinVariables()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com"
	ctx := &Context{Request: req}

	val, ok := b.Get("server_addr", ctx)
	if !ok || val != "example.com" {
		t.Errorf("server_addr = (%q, %v), want (%q, true)", val, ok, "example.com")
	}
}

func TestRequestURI(t *testing.T) {
	b := NewBuiltinVariables()
	req := httptest.NewRequest("GET", "/api/users?page=1", nil)
	ctx := &Context{Request: req}

	val, ok := b.Get("request_uri", ctx)
	if !ok || val != "/api/users?page=1" {
		t.Errorf("request_uri = (%q, %v), want (%q, true)", val, ok, "/api/users?page=1")
	}
}
