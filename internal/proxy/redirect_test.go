package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedirectTransport_FollowsRedirects(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		switch r.URL.Path {
		case "/start":
			w.Header().Set("Location", "/middle")
			w.WriteHeader(http.StatusFound)
		case "/middle":
			w.Header().Set("Location", "/end")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case "/end":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("done"))
		}
	}))
	defer server.Close()

	rt := NewRedirectTransport(http.DefaultTransport, 10)
	req, _ := http.NewRequest("GET", server.URL+"/start", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "done" {
		t.Errorf("expected 'done', got %q", body)
	}
	if callCount != 3 {
		t.Errorf("expected 3 backend calls, got %d", callCount)
	}
	if rt.followed.Load() != 2 {
		t.Errorf("expected 2 redirects followed, got %d", rt.followed.Load())
	}
}

func TestRedirectTransport_MaxExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/loop")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	rt := NewRedirectTransport(http.DefaultTransport, 3)
	req, _ := http.NewRequest("GET", server.URL+"/loop", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Should return the last redirect response
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302 after max exceeded, got %d", resp.StatusCode)
	}
	if rt.maxExceeded.Load() != 1 {
		t.Errorf("expected max_exceeded=1, got %d", rt.maxExceeded.Load())
	}
}

func TestRedirectTransport_303ChangesToGET(t *testing.T) {
	var lastMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/post" {
			w.Header().Set("Location", "/result")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		lastMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rt := NewRedirectTransport(http.DefaultTransport, 10)
	req, _ := http.NewRequest("POST", server.URL+"/post", strings.NewReader("body"))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if lastMethod != "GET" {
		t.Errorf("expected GET after 303, got %s", lastMethod)
	}
}

func TestRedirectTransport_307PreservesMethod(t *testing.T) {
	var lastMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/post" {
			w.Header().Set("Location", "/result")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		lastMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rt := NewRedirectTransport(http.DefaultTransport, 10)
	req, _ := http.NewRequest("POST", server.URL+"/post", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if lastMethod != "POST" {
		t.Errorf("expected POST after 307, got %s", lastMethod)
	}
}

func TestRedirectTransport_CopiesHeaders(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("Location", "/end")
			w.WriteHeader(http.StatusFound)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rt := NewRedirectTransport(http.DefaultTransport, 10)
	req, _ := http.NewRequest("GET", server.URL+"/start", nil)
	req.Header.Set("Authorization", "Bearer token123")
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if gotAuth != "Bearer token123" {
		t.Errorf("expected Authorization header to be forwarded, got %q", gotAuth)
	}
}

func TestRedirectTransport_NoRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("no redirect"))
	}))
	defer server.Close()

	rt := NewRedirectTransport(http.DefaultTransport, 10)
	req, _ := http.NewRequest("GET", server.URL+"/", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if rt.followed.Load() != 0 {
		t.Errorf("expected 0 redirects, got %d", rt.followed.Load())
	}
}

func TestRedirectTransport_Stats(t *testing.T) {
	rt := NewRedirectTransport(http.DefaultTransport, 5)
	stats := rt.Stats()
	if stats["max_redirects"] != 5 {
		t.Errorf("expected max_redirects=5, got %v", stats["max_redirects"])
	}
}
