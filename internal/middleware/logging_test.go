package middleware

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/variables"
)

func TestLoggingDefault(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	})

	mw := Logging()
	final := mw(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	if rr.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got %q", rr.Body.String())
	}
}

func TestLoggingWithFormatString(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	})

	cfg := LoggingConfig{
		Format: `$request_method $request_uri $status`,
	}

	mw := LoggingWithConfig(cfg)
	final := mw(handler)

	req := httptest.NewRequest("POST", "/items?foo=bar", nil)
	req.Header.Set("User-Agent", "test-agent")
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rr.Code)
	}
}

func TestLoggingWithJSON(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("json-log"))
	})

	cfg := LoggingConfig{
		JSON: true,
	}

	mw := LoggingWithConfig(cfg)
	final := mw(handler)

	req := httptest.NewRequest("GET", "/api/data?key=val", nil)
	req.Header.Set("User-Agent", "json-test-agent")
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestLoggingWithJSONEmptyQuery(t *testing.T) {
	// Tests the JSON path without a query string to cover the branch
	// where r.URL.RawQuery == "".
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := LoggingConfig{
		JSON: true,
	}

	mw := LoggingWithConfig(cfg)
	final := mw(handler)

	req := httptest.NewRequest("GET", "/no-query", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestLoggingSkipPaths(t *testing.T) {
	var handlerCalled bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	cfg := LoggingConfig{
		SkipPaths: []string{"/health", "/ready"},
	}

	mw := LoggingWithConfig(cfg)
	final := mw(handler)

	t.Run("skipped path passes through", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()

		final.ServeHTTP(rr, req)

		if !handlerCalled {
			t.Error("handler should have been called for skipped path")
		}
		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}
	})

	t.Run("non-skipped path is logged", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest("GET", "/api/data", nil)
		rr := httptest.NewRecorder()

		final.ServeHTTP(rr, req)

		if !handlerCalled {
			t.Error("handler should have been called for non-skipped path")
		}
	})
}

func TestLoggingWithEmptyFormat(t *testing.T) {
	// When Format is empty, the middleware should fall back to DefaultLoggingConfig.Format.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := LoggingConfig{
		Format: "",
	}

	mw := LoggingWithConfig(cfg)
	final := mw(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestLoggingResponseWriterWriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	lrw := &loggingResponseWriter{
		ResponseWriter: rr,
		status:         http.StatusOK,
	}

	lrw.WriteHeader(http.StatusNotFound)

	if lrw.Status() != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", lrw.Status())
	}
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected underlying recorder to have status 404, got %d", rr.Code)
	}
}

func TestLoggingResponseWriterWrite(t *testing.T) {
	rr := httptest.NewRecorder()
	lrw := &loggingResponseWriter{
		ResponseWriter: rr,
		status:         http.StatusOK,
	}

	data := []byte("hello world")
	n, err := lrw.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if lrw.BytesWritten() != int64(len(data)) {
		t.Errorf("expected BytesWritten()=%d, got %d", len(data), lrw.BytesWritten())
	}

	// Write more data and verify accumulation.
	more := []byte("!!!")
	n2, err := lrw.Write(more)
	if err != nil {
		t.Fatalf("unexpected error on second write: %v", err)
	}
	expected := int64(n + n2)
	if lrw.BytesWritten() != expected {
		t.Errorf("expected BytesWritten()=%d, got %d", expected, lrw.BytesWritten())
	}
}

func TestLoggingResponseWriterStatus(t *testing.T) {
	rr := httptest.NewRecorder()
	lrw := &loggingResponseWriter{
		ResponseWriter: rr,
		status:         http.StatusTeapot,
	}

	if lrw.Status() != http.StatusTeapot {
		t.Errorf("expected status %d, got %d", http.StatusTeapot, lrw.Status())
	}
}

func TestLoggingResponseWriterBytesWritten(t *testing.T) {
	rr := httptest.NewRecorder()
	lrw := &loggingResponseWriter{
		ResponseWriter: rr,
		bytes:          42,
	}

	if lrw.BytesWritten() != 42 {
		t.Errorf("expected 42, got %d", lrw.BytesWritten())
	}
}

// flusherRecorder is an httptest.ResponseRecorder that also implements http.Flusher.
type flusherRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flusherRecorder) Flush() {
	f.flushed = true
}

func TestLoggingResponseWriterFlushDelegates(t *testing.T) {
	fr := &flusherRecorder{ResponseRecorder: httptest.NewRecorder()}
	lrw := &loggingResponseWriter{
		ResponseWriter: fr,
		status:         http.StatusOK,
	}

	lrw.Flush()

	if !fr.flushed {
		t.Error("Flush should delegate to underlying Flusher")
	}
}

func TestLoggingResponseWriterFlushNoFlusher(t *testing.T) {
	// Use a plain ResponseWriter that does not implement http.Flusher.
	lrw := &loggingResponseWriter{
		ResponseWriter: &nonFlusherWriter{header: make(http.Header)},
		status:         http.StatusOK,
	}

	// Should not panic even though the underlying writer doesn't support Flush.
	lrw.Flush()
}

// nonFlusherWriter implements http.ResponseWriter but not http.Flusher.
type nonFlusherWriter struct {
	header http.Header
}

func (nf *nonFlusherWriter) Header() http.Header         { return nf.header }
func (nf *nonFlusherWriter) Write(b []byte) (int, error)  { return len(b), nil }
func (nf *nonFlusherWriter) WriteHeader(int)              {}

func TestLoggingResponseWriterHijackNotSupported(t *testing.T) {
	rr := httptest.NewRecorder()
	lrw := &loggingResponseWriter{
		ResponseWriter: rr,
		status:         http.StatusOK,
	}

	conn, rw, err := lrw.Hijack()
	if err != http.ErrNotSupported {
		t.Errorf("expected ErrNotSupported, got %v", err)
	}
	if conn != nil {
		t.Error("expected nil conn")
	}
	if rw != nil {
		t.Error("expected nil rw")
	}
}

// hijackableWriter implements both http.ResponseWriter and http.Hijacker.
type hijackableWriter struct {
	http.ResponseWriter
	hijacked bool
}

func (hw *hijackableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hw.hijacked = true
	// Return a pipe as the connection for testing.
	server, client := net.Pipe()
	_ = server.Close()
	return client, bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client)), nil
}

func TestLoggingResponseWriterHijackDelegates(t *testing.T) {
	hw := &hijackableWriter{ResponseWriter: httptest.NewRecorder()}
	lrw := &loggingResponseWriter{
		ResponseWriter: hw,
		status:         http.StatusOK,
	}

	conn, rw, err := lrw.Hijack()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn == nil {
		t.Error("expected non-nil conn")
	}
	if rw == nil {
		t.Error("expected non-nil rw")
	}
	if !hw.hijacked {
		t.Error("Hijack should delegate to underlying Hijacker")
	}
	// Clean up the connection.
	if c, ok := conn.(io.Closer); ok {
		c.Close()
	}
}

func TestLoggingNilOutput(t *testing.T) {
	// When Output is nil, LoggingWithConfig should default to os.Stdout
	// and not panic.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := LoggingConfig{
		Output: nil,
		Format: "$request_method $status",
	}

	mw := LoggingWithConfig(cfg)
	final := mw(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestLoggingJSONWithVarContext(t *testing.T) {
	// Test the JSON path with populated variable context fields to cover the
	// RouteID, UpstreamAddr, TenantID, Identity, and Custom body branches.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	cfg := LoggingConfig{
		JSON: true,
	}

	mw := LoggingWithConfig(cfg)
	final := mw(handler)

	req := httptest.NewRequest("GET", "/api/data?q=1", nil)
	req.Header.Set("User-Agent", "var-ctx-agent")

	// Pre-populate the variable context so that the JSON branch logs extra fields.
	varCtx := variables.AcquireContext(req)
	varCtx.RouteID = "route-42"
	varCtx.UpstreamAddr = "10.0.0.5:8080"
	varCtx.TenantID = "tenant-abc"
	varCtx.Identity = &variables.Identity{ClientID: "client-xyz"}
	varCtx.Custom = map[string]string{
		"_al_req_body":  `{"input":"test"}`,
		"_al_resp_body": `{"output":"ok"}`,
	}
	ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, varCtx)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestLoggingJSONWithoutOptionalFields(t *testing.T) {
	// Test the JSON path with no optional fields populated (no query, no
	// user-agent, no RouteID, etc.) to verify the branches are skipped cleanly.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := LoggingConfig{
		JSON: true,
	}

	mw := LoggingWithConfig(cfg)
	final := mw(handler)

	req := httptest.NewRequest("GET", "/simple", nil)
	req.Header.Del("User-Agent")
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}
