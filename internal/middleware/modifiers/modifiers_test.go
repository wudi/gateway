package modifiers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestHeaderCopy(t *testing.T) {
	chain, err := Compile([]config.ModifierConfig{
		{Type: "header_copy", From: "Authorization", To: "X-Auth-Backup", Scope: "request"},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := chain.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Auth-Backup"); got != "Bearer token123" {
			t.Errorf("expected X-Auth-Backup=Bearer token123, got %q", got)
		}
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer token123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
}

func TestCookieModifier(t *testing.T) {
	chain, err := Compile([]config.ModifierConfig{
		{Type: "cookie", Name: "session", Value: "abc123", Scope: "response", Secure: true, HttpOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := chain.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected Set-Cookie header")
	}
	if cookies[0].Name != "session" || cookies[0].Value != "abc123" {
		t.Errorf("unexpected cookie: %+v", cookies[0])
	}
}

func TestQueryModifier(t *testing.T) {
	chain, err := Compile([]config.ModifierConfig{
		{Type: "query", Params: map[string]string{"api_key": "secret123"}, Scope: "request"},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := chain.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api_key"); got != "secret123" {
			t.Errorf("expected api_key=secret123, got %q", got)
		}
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/test?existing=yes", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
}

func TestStashModifier(t *testing.T) {
	chain, err := Compile([]config.ModifierConfig{
		{Type: "stash", Scope: "request"},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := chain.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Original-URL"); got != "/original?q=1" {
			t.Errorf("expected X-Original-URL=/original?q=1, got %q", got)
		}
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/original?q=1", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
}

func TestPortModifier(t *testing.T) {
	chain, err := Compile([]config.ModifierConfig{
		{Type: "port", Port: 9090, Scope: "request"},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := chain.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Host; got != "example.com:9090" {
			t.Errorf("expected host example.com:9090, got %q", got)
		}
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "http://example.com/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
}

func TestConditionalModifier(t *testing.T) {
	chain, err := Compile([]config.ModifierConfig{
		{
			Type:  "header_set",
			Name:  "X-Result",
			Value: "matched",
			Scope: "request",
			Condition: &config.ConditionConfig{
				Type:  "header",
				Name:  "X-Check",
				Value: "yes.*",
			},
			Else: &config.ModifierConfig{
				Type:  "header_set",
				Name:  "X-Result",
				Value: "no-match",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("condition met", func(t *testing.T) {
		handler := chain.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Result"); got != "matched" {
				t.Errorf("expected matched, got %q", got)
			}
		}))
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Check", "yes-please")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	})

	t.Run("condition not met - else", func(t *testing.T) {
		handler := chain.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Result"); got != "no-match" {
				t.Errorf("expected no-match, got %q", got)
			}
		}))
		req := httptest.NewRequest("GET", "/test", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	})
}

func TestPriorityOrdering(t *testing.T) {
	chain, err := Compile([]config.ModifierConfig{
		{Type: "header_set", Name: "X-Order", Value: "low", Scope: "request", Priority: 1},
		{Type: "header_set", Name: "X-Order", Value: "high", Scope: "request", Priority: 10},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := chain.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Higher priority runs first, then low priority overwrites
		if got := r.Header.Get("X-Order"); got != "low" {
			t.Errorf("expected low (last writer wins), got %q", got)
		}
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
}

func TestPathRegexCondition(t *testing.T) {
	chain, err := Compile([]config.ModifierConfig{
		{
			Type:  "header_set",
			Name:  "X-API",
			Value: "true",
			Scope: "request",
			Condition: &config.ConditionConfig{
				Type:  "path_regex",
				Value: `^/api/`,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("matches", func(t *testing.T) {
		handler := chain.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-API"); got != "true" {
				t.Errorf("expected true, got %q", got)
			}
		}))
		req := httptest.NewRequest("GET", "/api/users", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	})

	t.Run("no match", func(t *testing.T) {
		handler := chain.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-API"); got != "" {
				t.Errorf("expected empty, got %q", got)
			}
		}))
		req := httptest.NewRequest("GET", "/web/page", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	})
}
