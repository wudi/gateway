package altsvc

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareAddsAltSvcOnHTTP1(t *testing.T) {
	handler := Middleware("443")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.ProtoMajor = 1
	req.ProtoMinor = 1
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	altSvc := rec.Header().Get("Alt-Svc")
	expected := `h3=":443"; ma=2592000`
	if altSvc != expected {
		t.Errorf("expected Alt-Svc %q, got %q", expected, altSvc)
	}
}

func TestMiddlewareAddsAltSvcOnHTTP2(t *testing.T) {
	handler := Middleware("8443")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.ProtoMajor = 2
	req.ProtoMinor = 0
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	altSvc := rec.Header().Get("Alt-Svc")
	expected := `h3=":8443"; ma=2592000`
	if altSvc != expected {
		t.Errorf("expected Alt-Svc %q, got %q", expected, altSvc)
	}
}

func TestMiddlewareSkipsAltSvcOnHTTP3(t *testing.T) {
	handler := Middleware("443")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.ProtoMajor = 3
	req.ProtoMinor = 0
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	altSvc := rec.Header().Get("Alt-Svc")
	if altSvc != "" {
		t.Errorf("expected no Alt-Svc header on HTTP/3, got %q", altSvc)
	}
}

func TestMiddlewarePassesThroughResponse(t *testing.T) {
	handler := Middleware("443")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "value")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("body"))
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
	if rec.Header().Get("X-Custom") != "value" {
		t.Error("expected X-Custom header to pass through")
	}
	if rec.Body.String() != "body" {
		t.Errorf("expected body 'body', got %q", rec.Body.String())
	}
}
