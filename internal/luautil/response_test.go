package luautil

import (
	"net/http"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// testResponseBuffer implements ResponseBuffer for testing.
type testResponseBuffer struct {
	header http.Header
	status int
	body   string
}

func newTestResponseBuffer(status int, body string) *testResponseBuffer {
	return &testResponseBuffer{
		header: make(http.Header),
		status: status,
		body:   body,
	}
}

func (b *testResponseBuffer) Header() http.Header     { return b.header }
func (b *testResponseBuffer) StatusCode() int          { return b.status }
func (b *testResponseBuffer) SetStatusCode(code int)   { b.status = code }
func (b *testResponseBuffer) ReadBody() string         { return b.body }
func (b *testResponseBuffer) SetBody(s string)         { b.body = s }

func TestResponseUserData_GetSetHeader(t *testing.T) {
	L := newTestState()
	defer L.Close()

	rb := newTestResponseBuffer(200, "")
	rb.header.Set("Content-Type", "text/plain")

	L.SetGlobal("resp", NewResponseUserData(L, rb))
	if err := L.DoString(`
		ct = resp:get_header("Content-Type")
		resp:set_header("X-Custom", "value")
	`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("ct").String() != "text/plain" {
		t.Errorf("expected 'text/plain', got %s", L.GetGlobal("ct").String())
	}
	if rb.header.Get("X-Custom") != "value" {
		t.Errorf("expected X-Custom 'value', got %s", rb.header.Get("X-Custom"))
	}
}

func TestResponseUserData_DelHeader(t *testing.T) {
	L := newTestState()
	defer L.Close()

	rb := newTestResponseBuffer(200, "")
	rb.header.Set("X-Remove", "value")

	L.SetGlobal("resp", NewResponseUserData(L, rb))
	if err := L.DoString(`resp:del_header("X-Remove")`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if rb.header.Get("X-Remove") != "" {
		t.Errorf("expected header to be deleted, got %s", rb.header.Get("X-Remove"))
	}
}

func TestResponseUserData_Status(t *testing.T) {
	L := newTestState()
	defer L.Close()

	rb := newTestResponseBuffer(201, "")

	L.SetGlobal("resp", NewResponseUserData(L, rb))
	if err := L.DoString(`result = resp:status()`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").(lua.LNumber) != 201 {
		t.Errorf("expected status 201, got %v", L.GetGlobal("result"))
	}
}

func TestResponseUserData_SetStatus(t *testing.T) {
	L := newTestState()
	defer L.Close()

	rb := newTestResponseBuffer(200, "")

	L.SetGlobal("resp", NewResponseUserData(L, rb))
	if err := L.DoString(`resp:set_status(404)`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if rb.status != 404 {
		t.Errorf("expected status 404, got %d", rb.status)
	}
}

func TestResponseUserData_Body(t *testing.T) {
	L := newTestState()
	defer L.Close()

	rb := newTestResponseBuffer(200, "original body")

	L.SetGlobal("resp", NewResponseUserData(L, rb))
	if err := L.DoString(`result = resp:body()`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if L.GetGlobal("result").String() != "original body" {
		t.Errorf("expected 'original body', got %s", L.GetGlobal("result").String())
	}
}

func TestResponseUserData_SetBody(t *testing.T) {
	L := newTestState()
	defer L.Close()

	rb := newTestResponseBuffer(200, "original")

	L.SetGlobal("resp", NewResponseUserData(L, rb))
	if err := L.DoString(`resp:set_body("replaced")`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if rb.body != "replaced" {
		t.Errorf("expected 'replaced', got %s", rb.body)
	}
}

func TestResponseUserData_FullPipeline(t *testing.T) {
	L := newTestState()
	defer L.Close()

	rb := newTestResponseBuffer(200, `{"data":"original"}`)
	rb.header.Set("Content-Type", "application/json")

	L.SetGlobal("resp", NewResponseUserData(L, rb))
	if err := L.DoString(`
		local s = resp:status()
		local b = resp:body()
		resp:set_header("X-Processed", "true")
		resp:set_body(b .. " modified")
		resp:set_status(201)
	`); err != nil {
		t.Fatalf("error: %v", err)
	}
	if rb.status != 201 {
		t.Errorf("expected status 201, got %d", rb.status)
	}
	if rb.body != `{"data":"original"} modified` {
		t.Errorf("expected modified body, got %s", rb.body)
	}
	if rb.header.Get("X-Processed") != "true" {
		t.Errorf("expected X-Processed 'true', got %s", rb.header.Get("X-Processed"))
	}
}
