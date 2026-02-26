package soap

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/runway/config"
)

func TestSOAPHandler(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "soap:Envelope") {
			t.Error("expected SOAP envelope")
		}
		if r.Header.Get("Content-Type") != "text/xml; charset=utf-8" {
			t.Errorf("expected text/xml content type, got %s", r.Header.Get("Content-Type"))
		}

		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetUserResponse><Name>Alice</Name><Email>alice@example.com</Email></GetUserResponse></soap:Body></soap:Envelope>`))
	}))
	defer backend.Close()

	cfg := config.SOAPProtocolConfig{
		URL:      backend.URL,
		Template: `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetUser><ID>{{.Path}}</ID></GetUser></soap:Body></soap:Envelope>`,
	}

	h, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/users/123", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected JSON content type, got %s", ct)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON response: %v, body: %s", err, w.Body.String())
	}
}

func TestSOAPHandlerValidation(t *testing.T) {
	_, err := New(config.SOAPProtocolConfig{}, http.DefaultTransport)
	if err == nil {
		t.Error("expected error for empty config")
	}

	_, err = New(config.SOAPProtocolConfig{URL: "http://example.com"}, http.DefaultTransport)
	if err == nil {
		t.Error("expected error for missing template")
	}
}

func TestSOAPByRoute(t *testing.T) {
	m := NewSOAPByRoute()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<response><status>ok</status></response>`))
	}))
	defer backend.Close()

	cfg := config.SOAPProtocolConfig{
		URL:      backend.URL,
		Template: `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><Ping/></soap:Body></soap:Envelope>`,
	}
	if err := m.AddRoute("route1", cfg, http.DefaultTransport); err != nil {
		t.Fatal(err)
	}

	h := m.GetHandler("route1")
	if h == nil {
		t.Fatal("expected handler")
	}

	stats := m.Stats()
	if stats["route1"] == nil {
		t.Error("expected route1 stats")
	}
}
