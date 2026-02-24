package rules

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware/geo"
	"github.com/wudi/gateway/variables"
)

// --- Environment tests ---

func TestNewRequestEnv(t *testing.T) {
	r := httptest.NewRequest("POST", "http://example.com/api/users?page=2&sort=name", nil)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Custom", "hello")

	varCtx := &variables.Context{
		Request:    r,
		RouteID:    "test-route",
		PathParams: map[string]string{"id": "42"},
		Identity: &variables.Identity{
			ClientID: "client-1",
			AuthType: "jwt",
			Claims:   map[string]interface{}{"sub": "user-123"},
		},
	}

	env := NewRequestEnv(r, varCtx)

	if env.HTTP.Request.Method != "POST" {
		t.Errorf("expected method POST, got %s", env.HTTP.Request.Method)
	}
	if env.HTTP.Request.URI.Path != "/api/users" {
		t.Errorf("expected path /api/users, got %s", env.HTTP.Request.URI.Path)
	}
	if env.HTTP.Request.URI.Query != "page=2&sort=name" {
		t.Errorf("expected query page=2&sort=name, got %s", env.HTTP.Request.URI.Query)
	}
	if env.HTTP.Request.URI.Args["page"] != "2" {
		t.Errorf("expected arg page=2, got %s", env.HTTP.Request.URI.Args["page"])
	}
	if env.HTTP.Request.URI.Args["sort"] != "name" {
		t.Errorf("expected arg sort=name, got %s", env.HTTP.Request.URI.Args["sort"])
	}
	if env.HTTP.Request.Headers["Content-Type"] != "application/json" {
		t.Errorf("expected Content-Type header, got %s", env.HTTP.Request.Headers["Content-Type"])
	}
	if env.HTTP.Request.Headers["X-Custom"] != "hello" {
		t.Errorf("expected X-Custom header, got %s", env.HTTP.Request.Headers["X-Custom"])
	}
	if env.HTTP.Request.Host != "example.com" {
		t.Errorf("expected host example.com, got %s", env.HTTP.Request.Host)
	}
	if env.HTTP.Request.Scheme != "http" {
		t.Errorf("expected scheme http, got %s", env.HTTP.Request.Scheme)
	}
	if env.IP.Src == "" {
		t.Error("expected ip.src to be set")
	}
	if env.Route.ID != "test-route" {
		t.Errorf("expected route.id test-route, got %s", env.Route.ID)
	}
	if env.Route.Params["id"] != "42" {
		t.Errorf("expected route.params.id 42, got %s", env.Route.Params["id"])
	}
	if env.Auth.ClientID != "client-1" {
		t.Errorf("expected auth.client_id client-1, got %s", env.Auth.ClientID)
	}
	if env.Auth.Type != "jwt" {
		t.Errorf("expected auth.type jwt, got %s", env.Auth.Type)
	}
	if env.Auth.Claims["sub"] != "user-123" {
		t.Errorf("expected auth.claims.sub user-123, got %v", env.Auth.Claims["sub"])
	}
}

func TestNewRequestEnv_NilVarCtx(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	env := NewRequestEnv(r, nil)

	if env.Route.ID != "" {
		t.Errorf("expected empty route ID, got %s", env.Route.ID)
	}
	if env.Auth.ClientID != "" {
		t.Errorf("expected empty client ID, got %s", env.Auth.ClientID)
	}
	if env.Route.Params == nil {
		t.Error("expected non-nil params map")
	}
	if env.Auth.Claims == nil {
		t.Error("expected non-nil claims map")
	}
}

func TestNewResponseEnv(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/test", nil)
	varCtx := &variables.Context{Request: r, RouteID: "resp-route"}

	respHeaders := http.Header{}
	respHeaders.Set("Content-Type", "text/html")
	respHeaders.Set("Server", "nginx")

	env := NewResponseEnv(r, varCtx, 200, respHeaders)

	if env.HTTP.Response.Code != 200 {
		t.Errorf("expected status 200, got %d", env.HTTP.Response.Code)
	}
	if env.HTTP.Response.Headers["Content-Type"] != "text/html" {
		t.Errorf("expected response Content-Type text/html, got %s", env.HTTP.Response.Headers["Content-Type"])
	}
	if env.HTTP.Response.Headers["Server"] != "nginx" {
		t.Errorf("expected response Server nginx, got %s", env.HTTP.Response.Headers["Server"])
	}
	// Request fields remain available
	if env.HTTP.Request.URI.Path != "/test" {
		t.Errorf("expected path /test, got %s", env.HTTP.Request.URI.Path)
	}
}

func TestNewRequestEnv_Cookies(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: "abc123"})
	r.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})

	env := NewRequestEnv(r, nil)

	if env.HTTP.Request.Cookies["session"] != "abc123" {
		t.Errorf("expected cookie session=abc123, got %s", env.HTTP.Request.Cookies["session"])
	}
	if env.HTTP.Request.Cookies["theme"] != "dark" {
		t.Errorf("expected cookie theme=dark, got %s", env.HTTP.Request.Cookies["theme"])
	}
	if len(env.HTTP.Request.Cookies) != 2 {
		t.Errorf("expected 2 cookies, got %d", len(env.HTTP.Request.Cookies))
	}
}

func TestNewRequestEnv_NoCookies(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	env := NewRequestEnv(r, nil)

	if env.HTTP.Request.Cookies == nil {
		t.Error("expected non-nil cookies map")
	}
	if len(env.HTTP.Request.Cookies) != 0 {
		t.Errorf("expected empty cookies map, got %d entries", len(env.HTTP.Request.Cookies))
	}
}

func TestNewResponseEnv_ResponseTime(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	varCtx := &variables.Context{
		Request:   r,
		StartTime: time.Now().Add(-100 * time.Millisecond),
	}

	env := NewResponseEnv(r, varCtx, 200, http.Header{})

	if env.HTTP.Response.ResponseTime <= 0 {
		t.Errorf("expected positive response_time, got %f", env.HTTP.Response.ResponseTime)
	}
}

func TestNewResponseEnv_ResponseTime_NilVarCtx(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	env := NewResponseEnv(r, nil, 200, http.Header{})

	if env.HTTP.Response.ResponseTime != 0 {
		t.Errorf("expected response_time 0 for nil varCtx, got %f", env.HTTP.Response.ResponseTime)
	}
}

func TestCompileRequestRule_CookieExpression(t *testing.T) {
	cfg := config.RuleConfig{
		ID:         "require-session",
		Expression: `http.request.cookies["session"] == "abc123"`,
		Action:     "block",
		StatusCode: 403,
	}

	rule, err := CompileRequestRule(cfg)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	// Request with matching cookie
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: "abc123"})
	env := NewRequestEnv(r, nil)
	matched, err := rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if !matched {
		t.Error("expected rule to match request with session cookie")
	}

	// Request without cookie
	r = httptest.NewRequest("GET", "http://localhost/", nil)
	env = NewRequestEnv(r, nil)
	matched, err = rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if matched {
		t.Error("expected rule NOT to match request without session cookie")
	}
}

func TestCompileResponseRule_ResponseTimeExpression(t *testing.T) {
	cfg := config.RuleConfig{
		ID:         "slow-response",
		Expression: `http.response.response_time > 0`,
		Action:     "set_headers",
		Headers:    config.HeaderTransform{Set: map[string]string{"X-Slow": "true"}},
	}

	rule, err := CompileResponseRule(cfg)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	r := httptest.NewRequest("GET", "http://localhost/", nil)
	varCtx := &variables.Context{
		Request:   r,
		StartTime: time.Now().Add(-50 * time.Millisecond),
	}
	env := NewResponseEnv(r, varCtx, 200, http.Header{})
	matched, err := rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if !matched {
		t.Error("expected rule to match response with positive response_time")
	}
}

// --- Compilation and evaluation tests ---

func TestCompileRequestRule_BasicExpression(t *testing.T) {
	cfg := config.RuleConfig{
		ID:         "test-block",
		Expression: `http.request.method == "POST"`,
		Action:     "block",
		StatusCode: 403,
	}

	rule, err := CompileRequestRule(cfg)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	if rule.ID != "test-block" {
		t.Errorf("expected ID test-block, got %s", rule.ID)
	}
	if !rule.Enabled {
		t.Error("expected rule to be enabled by default")
	}

	// Evaluate against POST
	r := httptest.NewRequest("POST", "http://localhost/", nil)
	env := NewRequestEnv(r, nil)
	matched, err := rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if !matched {
		t.Error("expected rule to match POST request")
	}

	// Evaluate against GET
	r = httptest.NewRequest("GET", "http://localhost/", nil)
	env = NewRequestEnv(r, nil)
	matched, err = rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if matched {
		t.Error("expected rule NOT to match GET request")
	}
}

func TestCompileRequestRule_IPInList(t *testing.T) {
	cfg := config.RuleConfig{
		ID:         "block-ip",
		Expression: `ip.src in ["1.2.3.4", "5.6.7.8"]`,
		Action:     "block",
		StatusCode: 403,
	}

	rule, err := CompileRequestRule(cfg)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	r := httptest.NewRequest("GET", "http://localhost/", nil)
	r.RemoteAddr = "1.2.3.4:12345"
	env := NewRequestEnv(r, nil)
	matched, err := rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if !matched {
		t.Error("expected rule to match blocked IP")
	}

	r.RemoteAddr = "10.0.0.1:12345"
	env = NewRequestEnv(r, nil)
	matched, err = rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if matched {
		t.Error("expected rule NOT to match safe IP")
	}
}

func TestCompileRequestRule_HeaderContains(t *testing.T) {
	cfg := config.RuleConfig{
		ID:         "require-json",
		Expression: `http.request.method == "POST" and not (http.request.headers["Content-Type"] contains "application/json")`,
		Action:     "block",
		StatusCode: 415,
	}

	rule, err := CompileRequestRule(cfg)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	// POST without JSON content type -> match
	r := httptest.NewRequest("POST", "http://localhost/", nil)
	r.Header.Set("Content-Type", "text/plain")
	env := NewRequestEnv(r, nil)
	matched, err := rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if !matched {
		t.Error("expected rule to match POST with non-JSON content type")
	}

	// POST with JSON content type -> no match
	r.Header.Set("Content-Type", "application/json")
	env = NewRequestEnv(r, nil)
	matched, err = rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if matched {
		t.Error("expected rule NOT to match POST with JSON content type")
	}

	// GET without JSON content type -> no match (method check)
	r = httptest.NewRequest("GET", "http://localhost/", nil)
	env = NewRequestEnv(r, nil)
	matched, err = rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if matched {
		t.Error("expected rule NOT to match GET request")
	}
}

func TestCompileRequestRule_DisabledRule(t *testing.T) {
	enabled := false
	cfg := config.RuleConfig{
		ID:         "disabled",
		Expression: `true`,
		Action:     "block",
		Enabled:    &enabled,
	}

	rule, err := CompileRequestRule(cfg)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	if rule.Enabled {
		t.Error("expected rule to be disabled")
	}
}

func TestCompileRequestRule_InvalidExpression(t *testing.T) {
	cfg := config.RuleConfig{
		ID:         "bad",
		Expression: `invalid syntax !!!`,
		Action:     "block",
	}

	_, err := CompileRequestRule(cfg)
	if err == nil {
		t.Error("expected compile error for invalid expression")
	}
}

func TestCompileResponseRule(t *testing.T) {
	cfg := config.RuleConfig{
		ID:         "strip-header",
		Expression: `http.response.code == 200`,
		Action:     "set_headers",
		Headers: config.HeaderTransform{
			Remove: []string{"Server"},
		},
	}

	rule, err := CompileResponseRule(cfg)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	r := httptest.NewRequest("GET", "http://localhost/", nil)
	env := NewResponseEnv(r, nil, 200, http.Header{})
	matched, err := rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if !matched {
		t.Error("expected rule to match status 200")
	}

	env = NewResponseEnv(r, nil, 500, http.Header{})
	matched, err = rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if matched {
		t.Error("expected rule NOT to match status 500")
	}
}

// --- IsTerminating tests ---

func TestIsTerminating(t *testing.T) {
	tests := []struct {
		actionType string
		expected   bool
	}{
		{"block", true},
		{"custom_response", true},
		{"redirect", true},
		{"set_headers", false},
	}

	for _, tt := range tests {
		got := IsTerminating(Action{Type: tt.actionType})
		if got != tt.expected {
			t.Errorf("IsTerminating(%s) = %v, want %v", tt.actionType, got, tt.expected)
		}
	}
}

// --- Actions tests ---

func TestExecuteTerminatingAction_Block(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	ExecuteTerminatingAction(w, r, Action{
		Type:       "block",
		StatusCode: 403,
		Body:       "Forbidden by rule",
	})

	if w.Code != 403 {
		t.Errorf("expected status 403, got %d", w.Code)
	}
	if w.Body.String() != "Forbidden by rule" {
		t.Errorf("expected body 'Forbidden by rule', got %s", w.Body.String())
	}
}

func TestExecuteTerminatingAction_BlockDefaultStatus(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	ExecuteTerminatingAction(w, r, Action{Type: "block"})

	if w.Code != 403 {
		t.Errorf("expected default status 403, got %d", w.Code)
	}
}

func TestExecuteTerminatingAction_Redirect(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/old", nil)

	ExecuteTerminatingAction(w, r, Action{
		Type:        "redirect",
		StatusCode:  301,
		RedirectURL: "https://example.com/new",
	})

	if w.Code != 301 {
		t.Errorf("expected status 301, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://example.com/new" {
		t.Errorf("expected Location header https://example.com/new, got %s", loc)
	}
}

func TestExecuteTerminatingAction_CustomResponse(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	ExecuteTerminatingAction(w, r, Action{
		Type:       "custom_response",
		StatusCode: 200,
		Body:       `{"status":"ok"}`,
	})

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Body.String() != `{"status":"ok"}` {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
}

func TestExecuteRequestHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Existing", "old-value")

	ExecuteRequestHeaders(r, config.HeaderTransform{
		Add: map[string]string{"X-Added": "added"},
		Set: map[string]string{"X-Existing": "new-value"},
	})

	if r.Header.Get("X-Added") != "added" {
		t.Errorf("expected X-Added header, got %s", r.Header.Get("X-Added"))
	}
	if r.Header.Get("X-Existing") != "new-value" {
		t.Errorf("expected X-Existing to be overwritten, got %s", r.Header.Get("X-Existing"))
	}
}

func TestExecuteResponseHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Server", "nginx")
	w.Header().Set("X-Powered-By", "PHP")

	ExecuteResponseHeaders(w, config.HeaderTransform{
		Set:    map[string]string{"X-Frame-Options": "DENY"},
		Remove: []string{"Server", "X-Powered-By"},
	})

	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("expected X-Frame-Options DENY, got %s", w.Header().Get("X-Frame-Options"))
	}
	if w.Header().Get("Server") != "" {
		t.Errorf("expected Server header to be removed, got %s", w.Header().Get("Server"))
	}
	if w.Header().Get("X-Powered-By") != "" {
		t.Errorf("expected X-Powered-By header to be removed, got %s", w.Header().Get("X-Powered-By"))
	}
}

// --- Engine tests ---

func TestEngine_EvaluateRequest_TerminatingStops(t *testing.T) {
	engine, err := NewEngine(
		[]config.RuleConfig{
			{
				ID:         "block-all",
				Expression: `true`,
				Action:     "block",
				StatusCode: 403,
			},
			{
				ID:         "never-reached",
				Expression: `true`,
				Action:     "set_headers",
				Headers:    config.HeaderTransform{Set: map[string]string{"X-Test": "val"}},
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}

	r := httptest.NewRequest("GET", "http://localhost/", nil)
	env := NewRequestEnv(r, nil)
	results := engine.EvaluateRequest(env)

	if len(results) != 1 {
		t.Fatalf("expected 1 result (terminating stops), got %d", len(results))
	}
	if results[0].RuleID != "block-all" {
		t.Errorf("expected rule block-all, got %s", results[0].RuleID)
	}
	if !results[0].Terminated {
		t.Error("expected result to be terminated")
	}
}

func TestEngine_EvaluateRequest_NonTerminatingContinues(t *testing.T) {
	engine, err := NewEngine(
		[]config.RuleConfig{
			{
				ID:         "add-header-1",
				Expression: `true`,
				Action:     "set_headers",
				Headers:    config.HeaderTransform{Set: map[string]string{"X-1": "val1"}},
			},
			{
				ID:         "add-header-2",
				Expression: `true`,
				Action:     "set_headers",
				Headers:    config.HeaderTransform{Set: map[string]string{"X-2": "val2"}},
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}

	r := httptest.NewRequest("GET", "http://localhost/", nil)
	env := NewRequestEnv(r, nil)
	results := engine.EvaluateRequest(env)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestEngine_DisabledRuleSkipped(t *testing.T) {
	enabled := false
	engine, err := NewEngine(
		[]config.RuleConfig{
			{
				ID:         "disabled-block",
				Expression: `true`,
				Action:     "block",
				Enabled:    &enabled,
			},
			{
				ID:         "active-header",
				Expression: `true`,
				Action:     "set_headers",
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}

	r := httptest.NewRequest("GET", "http://localhost/", nil)
	env := NewRequestEnv(r, nil)
	results := engine.EvaluateRequest(env)

	if len(results) != 1 {
		t.Fatalf("expected 1 result (disabled skipped), got %d", len(results))
	}
	if results[0].RuleID != "active-header" {
		t.Errorf("expected active-header, got %s", results[0].RuleID)
	}
}

func TestEngine_HasRules(t *testing.T) {
	engine, err := NewEngine(
		[]config.RuleConfig{{ID: "r", Expression: `true`, Action: "block"}},
		[]config.RuleConfig{{ID: "s", Expression: `true`, Action: "set_headers"}},
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}

	if !engine.HasRequestRules() {
		t.Error("expected HasRequestRules to be true")
	}
	if !engine.HasResponseRules() {
		t.Error("expected HasResponseRules to be true")
	}

	empty, err := NewEngine(nil, nil)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}
	if empty.HasRequestRules() {
		t.Error("expected HasRequestRules to be false for empty engine")
	}
	if empty.HasResponseRules() {
		t.Error("expected HasResponseRules to be false for empty engine")
	}
}

// --- Metrics tests ---

func TestMetrics_Tracking(t *testing.T) {
	engine, err := NewEngine(
		[]config.RuleConfig{
			{
				ID:         "match-post",
				Expression: `http.request.method == "POST"`,
				Action:     "block",
				StatusCode: 403,
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}

	// Evaluate twice with POST (matches, blocks)
	r := httptest.NewRequest("POST", "http://localhost/", nil)
	env := NewRequestEnv(r, nil)
	engine.EvaluateRequest(env)
	engine.EvaluateRequest(env)

	// Evaluate once with GET (no match)
	r = httptest.NewRequest("GET", "http://localhost/", nil)
	env = NewRequestEnv(r, nil)
	engine.EvaluateRequest(env)

	snap := engine.GetMetrics()
	if snap.Evaluated != 3 {
		t.Errorf("expected 3 evaluated, got %d", snap.Evaluated)
	}
	if snap.Matched != 2 {
		t.Errorf("expected 2 matched, got %d", snap.Matched)
	}
	if snap.Blocked != 2 {
		t.Errorf("expected 2 blocked, got %d", snap.Blocked)
	}
}

// --- RulesByRoute tests ---

func TestRulesByRoute(t *testing.T) {
	rbr := NewRulesByRoute()

	err := rbr.AddRoute("route-1", config.RulesConfig{
		Request: []config.RuleConfig{
			{ID: "r1", Expression: `true`, Action: "set_headers"},
		},
	})
	if err != nil {
		t.Fatalf("AddRoute error: %v", err)
	}

	engine := rbr.GetEngine("route-1")
	if engine == nil {
		t.Fatal("expected engine for route-1")
	}
	if !engine.HasRequestRules() {
		t.Error("expected request rules for route-1")
	}

	if rbr.GetEngine("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	stats := rbr.Stats()
	if _, ok := stats["route-1"]; !ok {
		t.Error("expected stats for route-1")
	}
}

func TestRulesByRoute_CompileError(t *testing.T) {
	rbr := NewRulesByRoute()

	err := rbr.AddRoute("bad-route", config.RulesConfig{
		Request: []config.RuleConfig{
			{ID: "bad", Expression: `invalid !!!`, Action: "block"},
		},
	})
	if err == nil {
		t.Error("expected error for invalid expression")
	}
}

// --- Writer tests ---

func TestRulesResponseWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewRulesResponseWriter(rec)

	// WriteHeader should be captured but not flushed
	rw.WriteHeader(201)
	if rw.StatusCode() != 201 {
		t.Errorf("expected captured status 201, got %d", rw.StatusCode())
	}
	if rw.Flushed() {
		t.Error("expected not flushed yet")
	}
	if rec.Code != 200 { // httptest.Recorder default
		t.Errorf("expected underlying to still be default 200, got %d", rec.Code)
	}

	// Write body before flush — should be buffered, not sent
	n, err := rw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected no body on underlying before flush, got %s", rec.Body.String())
	}
	if rw.Flushed() {
		t.Error("expected not flushed after buffered Write")
	}

	// Modify headers before flush
	rw.Header().Set("X-Custom", "value")

	// Flush sends everything through
	rw.Flush()
	if !rw.Flushed() {
		t.Error("expected flushed after Flush()")
	}
	if rec.Code != 201 {
		t.Errorf("expected underlying status 201 after flush, got %d", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("expected body 'hello' after flush, got %s", rec.Body.String())
	}

	// Header should be present
	if rec.Header().Get("X-Custom") != "value" {
		t.Errorf("expected X-Custom header, got %s", rec.Header().Get("X-Custom"))
	}

	// Write after flush passes through directly
	rw.Write([]byte(" world"))
	if rec.Body.String() != "hello world" {
		t.Errorf("expected body 'hello world' after post-flush write, got %s", rec.Body.String())
	}
}

func TestRulesResponseWriter_FlushSendsBufferedBody(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewRulesResponseWriter(rec)

	rw.WriteHeader(404)
	rw.Write([]byte("not found"))

	// Nothing on underlying yet
	if rec.Body.Len() != 0 {
		t.Error("expected no body before flush")
	}

	// Flush sends status + body
	rw.Flush()
	if rec.Code != 404 {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
	if rec.Body.String() != "not found" {
		t.Errorf("expected body 'not found', got %s", rec.Body.String())
	}
}

// --- Geo fields tests ---

func TestGeoFieldsFromContext(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	ctx := geo.WithGeoResult(r.Context(), &geo.GeoResult{
		CountryCode: "US",
		CountryName: "United States",
		City:        "New York",
	})
	r = r.WithContext(ctx)

	env := NewRequestEnv(r, nil)

	if env.Geo.Country != "US" {
		t.Errorf("expected geo.country US, got %q", env.Geo.Country)
	}
	if env.Geo.CountryName != "United States" {
		t.Errorf("expected geo.country_name United States, got %q", env.Geo.CountryName)
	}
	if env.Geo.City != "New York" {
		t.Errorf("expected geo.city New York, got %q", env.Geo.City)
	}
}

func TestGeoFieldsNilContext(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	env := NewRequestEnv(r, nil)

	if env.Geo.Country != "" {
		t.Errorf("expected empty geo.country, got %q", env.Geo.Country)
	}
	if env.Geo.CountryName != "" {
		t.Errorf("expected empty geo.country_name, got %q", env.Geo.CountryName)
	}
	if env.Geo.City != "" {
		t.Errorf("expected empty geo.city, got %q", env.Geo.City)
	}
}

func TestGeoExpressionCompileAndEvaluate(t *testing.T) {
	cfg := config.RuleConfig{
		ID:         "block-country",
		Expression: `geo.country == "US"`,
		Action:     "block",
		StatusCode: 451,
	}

	rule, err := CompileRequestRule(cfg)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	// Request with US geo result → match
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	ctx := geo.WithGeoResult(r.Context(), &geo.GeoResult{
		CountryCode: "US",
		CountryName: "United States",
		City:        "New York",
	})
	r = r.WithContext(ctx)
	env := NewRequestEnv(r, nil)
	matched, err := rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if !matched {
		t.Error("expected rule to match US geo result")
	}

	// Request with DE geo result → no match
	r = httptest.NewRequest("GET", "http://localhost/", nil)
	ctx = geo.WithGeoResult(r.Context(), &geo.GeoResult{
		CountryCode: "DE",
		CountryName: "Germany",
		City:        "Berlin",
	})
	r = r.WithContext(ctx)
	env = NewRequestEnv(r, nil)
	matched, err = rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if matched {
		t.Error("expected rule NOT to match DE geo result")
	}

	// Request without geo result → no match (empty string != "US")
	r = httptest.NewRequest("GET", "http://localhost/", nil)
	env = NewRequestEnv(r, nil)
	matched, err = rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if matched {
		t.Error("expected rule NOT to match request without geo result")
	}
}

func TestGeoExpressionInList(t *testing.T) {
	cfg := config.RuleConfig{
		ID:         "block-countries",
		Expression: `geo.country in ["CN", "RU", "IR"]`,
		Action:     "block",
		StatusCode: 451,
	}

	rule, err := CompileRequestRule(cfg)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	// CN → match
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	ctx := geo.WithGeoResult(r.Context(), &geo.GeoResult{CountryCode: "CN"})
	r = r.WithContext(ctx)
	env := NewRequestEnv(r, nil)
	matched, err := rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if !matched {
		t.Error("expected rule to match CN")
	}

	// US → no match
	r = httptest.NewRequest("GET", "http://localhost/", nil)
	ctx = geo.WithGeoResult(r.Context(), &geo.GeoResult{CountryCode: "US"})
	r = r.WithContext(ctx)
	env = NewRequestEnv(r, nil)
	matched, err = rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if matched {
		t.Error("expected rule NOT to match US")
	}
}

func TestGeoExpressionCity(t *testing.T) {
	cfg := config.RuleConfig{
		ID:         "block-city",
		Expression: `geo.city == "Beijing"`,
		Action:     "block",
		StatusCode: 451,
	}

	rule, err := CompileRequestRule(cfg)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	r := httptest.NewRequest("GET", "http://localhost/", nil)
	ctx := geo.WithGeoResult(r.Context(), &geo.GeoResult{City: "Beijing"})
	r = r.WithContext(ctx)
	env := NewRequestEnv(r, nil)
	matched, err := rule.Evaluate(env)
	if err != nil {
		t.Fatalf("evaluate error: %v", err)
	}
	if !matched {
		t.Error("expected rule to match Beijing")
	}
}

// --- New action tests ---

func TestExecuteDelay(t *testing.T) {
	start := time.Now()
	ExecuteDelay(10 * time.Millisecond)
	elapsed := time.Since(start)
	if elapsed < 10*time.Millisecond {
		t.Errorf("expected at least 10ms delay, got %v", elapsed)
	}
}

func TestExecuteSetVar(t *testing.T) {
	varCtx := &variables.Context{}
	ExecuteSetVar(varCtx, map[string]string{"key1": "val1", "key2": "val2"})

	if varCtx.Custom["key1"] != "val1" {
		t.Errorf("expected key1=val1, got %s", varCtx.Custom["key1"])
	}
	if varCtx.Custom["key2"] != "val2" {
		t.Errorf("expected key2=val2, got %s", varCtx.Custom["key2"])
	}
}

func TestExecuteSetVar_NilContext(t *testing.T) {
	// Should not panic
	ExecuteSetVar(nil, map[string]string{"key": "val"})
}

func TestExecuteSetVar_ExistingCustom(t *testing.T) {
	varCtx := &variables.Context{
		Custom: map[string]string{"existing": "value"},
	}
	ExecuteSetVar(varCtx, map[string]string{"new": "added"})

	if varCtx.Custom["existing"] != "value" {
		t.Errorf("expected existing=value, got %s", varCtx.Custom["existing"])
	}
	if varCtx.Custom["new"] != "added" {
		t.Errorf("expected new=added, got %s", varCtx.Custom["new"])
	}
}

func TestExecuteSetStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewRulesResponseWriter(rec)
	rw.WriteHeader(200)

	ExecuteSetStatus(rw, 404)

	if rw.StatusCode() != 404 {
		t.Errorf("expected status 404, got %d", rw.StatusCode())
	}
}

func TestExecuteSetBody(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewRulesResponseWriter(rec)
	rw.Write([]byte("original"))

	ExecuteSetBody(rw, "replaced")

	if rw.ReadBody() != "replaced" {
		t.Errorf("expected body 'replaced', got %s", rw.ReadBody())
	}
}

func TestCacheBypass(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)

	if IsCacheBypass(r) {
		t.Error("expected no cache bypass initially")
	}

	r = SetCacheBypass(r)
	if !IsCacheBypass(r) {
		t.Error("expected cache bypass after SetCacheBypass")
	}
}

func TestRulesResponseWriter_SetStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewRulesResponseWriter(rec)
	rw.WriteHeader(200)

	rw.SetStatusCode(404)
	if rw.StatusCode() != 404 {
		t.Errorf("expected status 404, got %d", rw.StatusCode())
	}

	// After flush, SetStatusCode should be ignored
	rw.Flush()
	rw.SetStatusCode(500)
	if rec.Code != 404 {
		t.Errorf("expected flushed status 404, got %d", rec.Code)
	}
}

func TestRulesResponseWriter_SetBody(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewRulesResponseWriter(rec)
	rw.Write([]byte("original"))

	rw.SetBody("new body")
	if rw.ReadBody() != "new body" {
		t.Errorf("expected 'new body', got %s", rw.ReadBody())
	}

	// Flush and verify
	rw.Flush()
	if rec.Body.String() != "new body" {
		t.Errorf("expected flushed body 'new body', got %s", rec.Body.String())
	}
}

func TestRulesResponseWriter_FlushUpdatesContentLength(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewRulesResponseWriter(rec)
	rw.Header().Set("Content-Length", "8")
	rw.Write([]byte("original"))

	// Modify body to a different size
	rw.SetBody("short")
	rw.Flush()

	if rec.Header().Get("Content-Length") != "5" {
		t.Errorf("expected Content-Length 5, got %s", rec.Header().Get("Content-Length"))
	}
	if rec.Body.String() != "short" {
		t.Errorf("expected body 'short', got %s", rec.Body.String())
	}
}

func TestIsTerminating_NewActions(t *testing.T) {
	// All new actions should be non-terminating
	for _, action := range []string{"delay", "set_var", "set_status", "set_body", "cache_bypass", "lua"} {
		if IsTerminating(Action{Type: action}) {
			t.Errorf("expected %s to be non-terminating", action)
		}
	}
}

func TestEngine_NewActions_Compile(t *testing.T) {
	// Test that new action types compile successfully
	engine, err := NewEngine(
		[]config.RuleConfig{
			{
				ID:         "delay-rule",
				Expression: `true`,
				Action:     "delay",
			},
			{
				ID:         "set-var-rule",
				Expression: `true`,
				Action:     "set_var",
			},
			{
				ID:         "cache-bypass-rule",
				Expression: `true`,
				Action:     "cache_bypass",
			},
		},
		[]config.RuleConfig{
			{
				ID:         "set-status-rule",
				Expression: `true`,
				Action:     "set_status",
				StatusCode: 201,
			},
			{
				ID:         "set-body-rule",
				Expression: `true`,
				Action:     "set_body",
				Body:       "new body",
			},
		},
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}
	if !engine.HasRequestRules() {
		t.Error("expected request rules")
	}
	if !engine.HasResponseRules() {
		t.Error("expected response rules")
	}
}
