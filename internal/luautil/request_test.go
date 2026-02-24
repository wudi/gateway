package luautil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestUserData_GetSetHeader(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Input", "hello")

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`
		val = req:get_header("X-Input")
		req:set_header("X-Output", val .. "-processed")
	`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if r.Header.Get("X-Output") != "hello-processed" {
		t.Errorf("expected 'hello-processed', got %s", r.Header.Get("X-Output"))
	}
}

func TestRequestUserData_DelHeader(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Remove", "value")

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`req:del_header("X-Remove")`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if r.Header.Get("X-Remove") != "" {
		t.Errorf("expected header to be deleted, got %s", r.Header.Get("X-Remove"))
	}
}

func TestRequestUserData_PathMethod(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("POST", "/api/test", nil)

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`
		path = req:path()
		method = req:method()
	`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("path").String() != "/api/test" {
		t.Errorf("expected path '/api/test', got %s", L.GetGlobal("path").String())
	}
	if L.GetGlobal("method").String() != "POST" {
		t.Errorf("expected method 'POST', got %s", L.GetGlobal("method").String())
	}
}

func TestRequestUserData_QueryParam(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/path?foo=bar&baz=qux", nil)

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`result = req:query_param("foo")`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "bar" {
		t.Errorf("expected 'bar', got %s", L.GetGlobal("result").String())
	}
}

func TestRequestUserData_Host(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "http://example.com/", nil)

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`result = req:host()`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "example.com" {
		t.Errorf("expected 'example.com', got %s", L.GetGlobal("result").String())
	}
}

func TestRequestUserData_Scheme(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "http://example.com/", nil)

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`result = req:scheme()`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "http" {
		t.Errorf("expected 'http', got %s", L.GetGlobal("result").String())
	}
}

func TestRequestUserData_RemoteAddr(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:5678"

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`result = req:remote_addr()`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "1.2.3.4:5678" {
		t.Errorf("expected '1.2.3.4:5678', got %s", L.GetGlobal("result").String())
	}
}

func TestRequestUserData_Body(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("POST", "/", strings.NewReader("test body"))

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`result = req:body()`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "test body" {
		t.Errorf("expected 'test body', got %s", L.GetGlobal("result").String())
	}
	// Body should still be readable downstream
	data, _ := io.ReadAll(r.Body)
	if string(data) != "test body" {
		t.Errorf("expected body to still be readable, got %s", string(data))
	}
}

func TestRequestUserData_SetBody(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("POST", "/", strings.NewReader("original"))

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`req:set_body("replaced")`); err != nil {
		t.Fatalf("error: %v", err)
	}
	data, _ := io.ReadAll(r.Body)
	if string(data) != "replaced" {
		t.Errorf("expected 'replaced', got %s", string(data))
	}
	if r.ContentLength != 8 {
		t.Errorf("expected ContentLength 8, got %d", r.ContentLength)
	}
}

func TestRequestUserData_Cookie(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: "abc123"})

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`
		result = req:cookie("session")
		missing = req:cookie("nonexistent")
	`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "abc123" {
		t.Errorf("expected 'abc123', got %s", L.GetGlobal("result").String())
	}
	if L.GetGlobal("missing").String() != "" {
		t.Errorf("expected empty string for missing cookie, got %s", L.GetGlobal("missing").String())
	}
}

func TestRequestUserData_SetPath(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/old/path", nil)

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`req:set_path("/new/path")`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if r.URL.Path != "/new/path" {
		t.Errorf("expected '/new/path', got %s", r.URL.Path)
	}
}

func TestRequestUserData_SetQuery(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/path?old=value", nil)

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`req:set_query("new=value")`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if r.URL.RawQuery != "new=value" {
		t.Errorf("expected 'new=value', got %s", r.URL.RawQuery)
	}
}

func TestRequestUserData_NilBody(t *testing.T) {
	L := newTestState()
	defer L.Close()

	r := httptest.NewRequest("GET", "/", nil)
	r.Body = nil

	L.SetGlobal("req", NewRequestUserData(L, r))
	if err := L.DoString(`result = req:body()`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "" {
		t.Errorf("expected empty body, got %s", L.GetGlobal("result").String())
	}
}
