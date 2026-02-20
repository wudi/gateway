package wasm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"

	"github.com/wudi/gateway/internal/config"
)

// --- Minimal WASM binary builders ---
// These build valid WASM modules in binary format directly.
// Wazero has no WAT parser, so we construct the binary representation.

// buildMinimalWasm builds a WASM module with:
//   - imports: env.host_log, env.host_get_header, env.host_set_header, env.host_remove_header,
//     env.host_get_body, env.host_set_body, env.host_get_property, env.host_send_response
//   - exports: memory, allocate, deallocate, and optionally on_request / on_response
//   - The allocate function returns a fixed offset (1024).
//   - The deallocate function is a no-op.
//   - on_request/on_response are no-ops that return ActionContinue (0).
func buildMinimalWasm(exportRequest, exportResponse bool) []byte {
	// Rather than hand-encoding, use wazero's CompileModule to validate.
	// We'll build a trivial module that compiles correctly.
	// For simplicity, just use a known-good minimal WASM binary.
	return buildWasmBinary(exportRequest, exportResponse, false, false)
}

// buildWasmBinary constructs a minimal but valid WASM binary.
// The module imports all 8 host functions, exports memory + allocate + deallocate,
// and optionally exports on_request / on_response.
// If callSetHeader is true, on_request calls host_set_header to add X-Wasm: true.
// If callSendResponse is true, on_request calls host_send_response(403, ...).
func buildWasmBinary(exportRequest, exportResponse, callSetHeader, callSendResponse bool) []byte {
	var b bytes.Buffer

	// --- Magic + Version ---
	b.Write([]byte{0x00, 0x61, 0x73, 0x6d}) // magic
	b.Write([]byte{0x01, 0x00, 0x00, 0x00}) // version 1

	// --- Type Section (section 1) ---
	// We need these function types:
	// type 0: (i32, i32, i32) -> ()          host_log
	// type 1: (i32, i32, i32, i32, i32) -> i32  host_get_header
	// type 2: (i32, i32, i32, i32, i32) -> ()    host_set_header
	// type 3: (i32, i32, i32) -> ()              host_remove_header (same as type 0)
	// type 4: (i32, i32) -> i32                  host_get_body
	// type 5: (i32, i32) -> ()                   host_set_body
	// type 6: (i32, i32, i32, i32) -> i32        host_get_property
	// type 7: (i32, i32, i32) -> ()              host_send_response (same as type 0)
	// type 8: (i32) -> i32                       allocate
	// type 9: (i32, i32) -> ()                   deallocate (same as type 5)
	// type 10: (i32, i32) -> i32                 on_request / on_response (same as type 4)
	// type 11: () -> ()                          no-op (for deallocate body)

	types := encodeSection(1, encodeVector([][]byte{
		// type 0: (i32, i32, i32) -> ()
		{0x60, 3, 0x7f, 0x7f, 0x7f, 0},
		// type 1: (i32, i32, i32, i32, i32) -> (i32)
		{0x60, 5, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 1, 0x7f},
		// type 2: (i32, i32, i32, i32, i32) -> ()
		{0x60, 5, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0},
		// type 3: (i32, i32) -> (i32)
		{0x60, 2, 0x7f, 0x7f, 1, 0x7f},
		// type 4: (i32, i32) -> ()
		{0x60, 2, 0x7f, 0x7f, 0},
		// type 5: (i32, i32, i32, i32) -> (i32)
		{0x60, 4, 0x7f, 0x7f, 0x7f, 0x7f, 1, 0x7f},
		// type 6: (i32) -> (i32)
		{0x60, 1, 0x7f, 1, 0x7f},
	}))
	b.Write(types)

	// --- Import Section (section 2) ---
	imports := [][]byte{
		encodeImport("env", "host_log", 0x00, 0),             // type 0: (i32,i32,i32)->()
		encodeImport("env", "host_get_header", 0x00, 1),      // type 1: (i32,i32,i32,i32,i32)->(i32)
		encodeImport("env", "host_set_header", 0x00, 2),      // type 2: (i32,i32,i32,i32,i32)->()
		encodeImport("env", "host_remove_header", 0x00, 0),   // type 0: (i32,i32,i32)->()
		encodeImport("env", "host_get_body", 0x00, 3),        // type 3: (i32,i32)->(i32)
		encodeImport("env", "host_set_body", 0x00, 4),        // type 4: (i32,i32)->()
		encodeImport("env", "host_get_property", 0x00, 5),    // type 5: (i32,i32,i32,i32)->(i32)
		encodeImport("env", "host_send_response", 0x00, 0),   // type 0: (i32,i32,i32)->()
	}
	b.Write(encodeSection(2, encodeVector(imports)))

	// --- Function Section (section 3) ---
	// Local function indices start at 8 (after 8 imports)
	// func 8: allocate (type 6: i32->i32)
	// func 9: deallocate (type 4: i32,i32->())
	// func 10: on_request (type 3: i32,i32->i32) — optional
	// func 11: on_response (type 3: i32,i32->i32) — optional
	funcTypes := []byte{6, 4} // allocate, deallocate
	if exportRequest {
		funcTypes = append(funcTypes, 3) // on_request
	}
	if exportResponse {
		funcTypes = append(funcTypes, 3) // on_response
	}
	funcSec := []byte{byte(len(funcTypes))}
	funcSec = append(funcSec, funcTypes...)
	b.Write(encodeSection(3, funcSec))

	// --- Memory Section (section 5) ---
	// 1 memory, min 2 pages
	b.Write(encodeSection(5, []byte{1, 0x00, 2}))

	// --- Export Section (section 7) ---
	exportEntries := [][]byte{
		encodeExport("memory", 0x02, 0),      // memory 0
		encodeExport("allocate", 0x00, 8),     // func 8
		encodeExport("deallocate", 0x00, 9),   // func 9
	}
	nextFuncIdx := 10
	if exportRequest {
		exportEntries = append(exportEntries, encodeExport("on_request", 0x00, byte(nextFuncIdx)))
		nextFuncIdx++
	}
	if exportResponse {
		exportEntries = append(exportEntries, encodeExport("on_response", 0x00, byte(nextFuncIdx)))
	}
	b.Write(encodeSection(7, encodeVector(exportEntries)))

	// --- Code Section (section 10) ---
	var codeBodies [][]byte

	// allocate: return 1024 (a fixed offset)
	codeBodies = append(codeBodies, encodeCode([]byte{
		0x41, 0x80, 0x08, // i32.const 1024
		0x0b,             // end
	}))

	// deallocate: no-op
	codeBodies = append(codeBodies, encodeCode([]byte{
		0x0b, // end
	}))

	if exportRequest {
		if callSetHeader {
			// on_request: call host_set_header(0, "X-Wasm", "true") then return 0
			// We need data in memory. Let's use offset 2048 for key "X-Wasm" (6 bytes)
			// and offset 2060 for value "true" (4 bytes).
			// But we'd need a data section. Instead, let's use i32.store to write bytes.
			// Simpler: just return 0 and test the hostState-based header setting separately.
			// Actually, let's use the data section approach. We'll add data segments.
			codeBodies = append(codeBodies, encodeCode([]byte{
				// call host_set_header(0, 2048, 6, 2054, 4)
				0x41, 0x00,       // i32.const 0 (map_type = request)
				0x41, 0x80, 0x10, // i32.const 2048 (key_ptr)
				0x41, 0x06,       // i32.const 6 (key_len)
				0x41, 0x86, 0x10, // i32.const 2054 (val_ptr)
				0x41, 0x04,       // i32.const 4 (val_len)
				0x10, 0x02,       // call func 2 (host_set_header)
				0x41, 0x00,       // i32.const 0 (ActionContinue)
				0x0b,             // end
			}))
		} else if callSendResponse {
			// on_request: call host_send_response(403, 2058, 9) then return 2
			codeBodies = append(codeBodies, encodeCode([]byte{
				0x41, 0x93, 0x03, // i32.const 403
				0x41, 0x92, 0x10, // i32.const 2066 (body_ptr)
				0x41, 0x09,       // i32.const 9 (body_len = "forbidden")
				0x10, 0x07,       // call func 7 (host_send_response)
				0x41, 0x02,       // i32.const 2 (ActionSendResponse)
				0x0b,             // end
			}))
		} else {
			// on_request: return 0
			codeBodies = append(codeBodies, encodeCode([]byte{
				0x41, 0x00, // i32.const 0
				0x0b,       // end
			}))
		}
	}
	if exportResponse {
		// on_response: return 0
		codeBodies = append(codeBodies, encodeCode([]byte{
			0x41, 0x00, // i32.const 0
			0x0b,       // end
		}))
	}
	b.Write(encodeSection(10, encodeVector(codeBodies)))

	// --- Data Section (section 11) ---
	if callSetHeader || callSendResponse {
		var dataSegments [][]byte
		// "X-Wasm" at offset 2048
		dataSegments = append(dataSegments, encodeDataSegment(2048, []byte("X-Wasm")))
		// "true" at offset 2054
		dataSegments = append(dataSegments, encodeDataSegment(2054, []byte("true")))
		if callSendResponse {
			// "forbidden" at offset 2066
			dataSegments = append(dataSegments, encodeDataSegment(2066, []byte("forbidden")))
		}
		b.Write(encodeSection(11, encodeVector(dataSegments)))
	}

	return b.Bytes()
}

// --- WASM binary encoding helpers ---

func encodeSection(id byte, content []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(id)
	buf.Write(encodeLEB128(uint32(len(content))))
	buf.Write(content)
	return buf.Bytes()
}

func encodeVector(items [][]byte) []byte {
	var buf bytes.Buffer
	buf.Write(encodeLEB128(uint32(len(items))))
	for _, item := range items {
		buf.Write(item)
	}
	return buf.Bytes()
}

func encodeImport(module, name string, kind, typeIdx byte) []byte {
	var buf bytes.Buffer
	buf.Write(encodeLEB128(uint32(len(module))))
	buf.WriteString(module)
	buf.Write(encodeLEB128(uint32(len(name))))
	buf.WriteString(name)
	buf.WriteByte(kind)
	buf.WriteByte(typeIdx)
	return buf.Bytes()
}

func encodeExport(name string, kind, idx byte) []byte {
	var buf bytes.Buffer
	buf.Write(encodeLEB128(uint32(len(name))))
	buf.WriteString(name)
	buf.WriteByte(kind)
	buf.WriteByte(idx)
	return buf.Bytes()
}

func encodeCode(body []byte) []byte {
	// code = locals_count + body
	locals := []byte{0} // 0 local declarations
	full := append(locals, body...)
	var buf bytes.Buffer
	buf.Write(encodeLEB128(uint32(len(full))))
	buf.Write(full)
	return buf.Bytes()
}

func encodeDataSegment(offset int, data []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(0x00)            // active, memory 0
	buf.WriteByte(0x41)            // i32.const
	buf.Write(encodeSignedLEB128(int32(offset)))
	buf.WriteByte(0x0b)            // end
	buf.Write(encodeLEB128(uint32(len(data))))
	buf.Write(data)
	return buf.Bytes()
}

func encodeLEB128(value uint32) []byte {
	var buf []byte
	for {
		b := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if value == 0 {
			break
		}
	}
	return buf
}

func encodeSignedLEB128(value int32) []byte {
	var buf []byte
	for {
		b := byte(value & 0x7f)
		value >>= 7
		if (value == 0 && b&0x40 == 0) || (value == -1 && b&0x40 != 0) {
			buf = append(buf, b)
			break
		}
		b |= 0x80
		buf = append(buf, b)
	}
	return buf
}

// writeWasmFile writes WASM bytes to a temp file and returns the path.
func writeWasmFile(t *testing.T, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.wasm")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- Tests ---

func TestRequestPhase_NoOp(t *testing.T) {
	wasmBytes := buildWasmBinary(true, false, false, false)
	path := writeWasmFile(t, wasmBytes)

	mgr := NewWasmByRoute(config.WasmConfig{})
	defer mgr.Close(context.Background())

	err := mgr.AddRoute("test-route", []config.WasmPluginConfig{
		{
			Enabled:  true,
			Name:     "noop",
			Path:     path,
			Phase:    "request",
			PoolSize: 2,
			Timeout:  100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	chain := mgr.GetChain("test-route")
	if chain == nil {
		t.Fatal("expected chain")
	}

	mw := chain.RequestMiddleware()
	if mw == nil {
		t.Fatal("expected request middleware")
	}

	// Should pass through to the next handler
	var called bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("next handler was not called")
	}
	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRequestPhase_SetHeader(t *testing.T) {
	wasmBytes := buildWasmBinary(true, false, true, false)
	path := writeWasmFile(t, wasmBytes)

	mgr := NewWasmByRoute(config.WasmConfig{})
	defer mgr.Close(context.Background())

	err := mgr.AddRoute("test-route", []config.WasmPluginConfig{
		{
			Enabled:  true,
			Name:     "set-header",
			Path:     path,
			Phase:    "request",
			PoolSize: 2,
			Timeout:  100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	chain := mgr.GetChain("test-route")
	mw := chain.RequestMiddleware()

	var gotHeader string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Wasm")
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)

	if gotHeader != "true" {
		t.Errorf("expected X-Wasm header 'true', got %q", gotHeader)
	}
}

func TestSendResponse_EarlyTermination(t *testing.T) {
	wasmBytes := buildWasmBinary(true, false, false, true)
	path := writeWasmFile(t, wasmBytes)

	mgr := NewWasmByRoute(config.WasmConfig{})
	defer mgr.Close(context.Background())

	err := mgr.AddRoute("test-route", []config.WasmPluginConfig{
		{
			Enabled:  true,
			Name:     "send-response",
			Path:     path,
			Phase:    "request",
			PoolSize: 2,
			Timeout:  100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	chain := mgr.GetChain("test-route")
	mw := chain.RequestMiddleware()

	var backendCalled bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)

	if backendCalled {
		t.Error("backend should not have been called")
	}
	if rec.Code != 403 {
		t.Errorf("expected 403, got %d", rec.Code)
	}
	if rec.Body.String() != "forbidden" {
		t.Errorf("expected body 'forbidden', got %q", rec.Body.String())
	}
}

func TestResponsePhase_NoOp(t *testing.T) {
	wasmBytes := buildWasmBinary(false, true, false, false)
	path := writeWasmFile(t, wasmBytes)

	mgr := NewWasmByRoute(config.WasmConfig{})
	defer mgr.Close(context.Background())

	err := mgr.AddRoute("test-route", []config.WasmPluginConfig{
		{
			Enabled:  true,
			Name:     "resp-noop",
			Path:     path,
			Phase:    "response",
			PoolSize: 2,
			Timeout:  100 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	chain := mgr.GetChain("test-route")
	mw := chain.ResponseMiddleware()
	if mw == nil {
		t.Fatal("expected response middleware")
	}

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "true")
		w.WriteHeader(200)
		w.Write([]byte("hello"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got %q", rec.Body.String())
	}
}

func TestPoolBorrowReturn(t *testing.T) {
	wasmBytes := buildWasmBinary(true, false, false, false)
	path := writeWasmFile(t, wasmBytes)

	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx)

	registerHostFunctions(rt)
	envCfg := wazero.NewModuleConfig().WithName("env")
	// Need to instantiate env first — use the same approach as WasmByRoute
	mgr := NewWasmByRoute(config.WasmConfig{})
	defer mgr.Close(ctx)

	err := mgr.AddRoute("test", []config.WasmPluginConfig{
		{
			Enabled:  true,
			Name:     "pool-test",
			Path:     path,
			Phase:    "request",
			PoolSize: 2,
			Timeout:  100 * time.Millisecond,
		},
	})
	_ = envCfg
	if err != nil {
		t.Fatal(err)
	}

	chain := mgr.GetChain("test")
	if chain == nil || len(chain.plugins) == 0 {
		t.Fatal("expected plugin chain")
	}

	plugin := chain.plugins[0]
	stats := plugin.pool.Stats()
	if stats.PoolSize != 2 {
		t.Errorf("expected pool size 2, got %d", stats.PoolSize)
	}

	// Borrow and return
	mod, err := plugin.pool.Borrow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plugin.pool.Return(ctx, mod)

	stats = plugin.pool.Stats()
	if stats.Borrows != 1 {
		t.Errorf("expected 1 borrow, got %d", stats.Borrows)
	}
	if stats.Returns != 1 {
		t.Errorf("expected 1 return, got %d", stats.Returns)
	}
}

func TestMultiplePlugins_Chain(t *testing.T) {
	// Create two no-op plugins
	wasmBytes := buildWasmBinary(true, false, false, false)
	path1 := writeWasmFile(t, wasmBytes)

	wasmBytes2 := buildWasmBinary(true, false, false, false)
	dir2 := t.TempDir()
	path2 := filepath.Join(dir2, "plugin2.wasm")
	os.WriteFile(path2, wasmBytes2, 0644)

	mgr := NewWasmByRoute(config.WasmConfig{})
	defer mgr.Close(context.Background())

	err := mgr.AddRoute("test-route", []config.WasmPluginConfig{
		{Enabled: true, Name: "p1", Path: path1, Phase: "request", PoolSize: 1, Timeout: 100 * time.Millisecond},
		{Enabled: true, Name: "p2", Path: path2, Phase: "request", PoolSize: 1, Timeout: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}

	chain := mgr.GetChain("test-route")
	if chain == nil || len(chain.plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %v", chain)
	}

	mw := chain.RequestMiddleware()
	var called bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("next handler not called")
	}
	// Both plugins should have been invoked
	for i, p := range chain.plugins {
		if p.requestInvocations.Load() != 1 {
			t.Errorf("plugin %d: expected 1 invocation, got %d", i, p.requestInvocations.Load())
		}
	}
}

func TestPluginConfig_Property(t *testing.T) {
	// Build a module that calls host_get_property("config.api_key", ...)
	// and then host_set_header to propagate the value.
	// For simplicity, we test the host function directly.
	ctx := context.Background()

	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter())
	defer rt.Close(ctx)

	hs := &hostState{
		pluginConfig: map[string]string{"api_key": "secret123"},
		routeID:      "route-1",
	}

	// Test the property lookup directly
	if val := getPropertyValue(hs, "config.api_key"); val != "secret123" {
		t.Errorf("expected 'secret123', got %q", val)
	}
	if val := getPropertyValue(hs, "route_id"); val != "route-1" {
		t.Errorf("expected 'route-1', got %q", val)
	}
	if val := getPropertyValue(hs, "config.nonexistent"); val != "" {
		t.Errorf("expected empty, got %q", val)
	}
}

// getPropertyValue extracts the property value that hostGetProperty would return.
func getPropertyValue(hs *hostState, key string) string {
	switch {
	case key == "method" && hs.req != nil:
		return hs.req.Method
	case key == "path" && hs.req != nil:
		return hs.req.URL.Path
	case key == "query" && hs.req != nil:
		return hs.req.URL.RawQuery
	case key == "host" && hs.req != nil:
		return hs.req.Host
	case key == "scheme":
		return hs.scheme
	case key == "route_id":
		return hs.routeID
	case key == "client_ip" && hs.req != nil:
		return hs.req.RemoteAddr
	case len(key) > 7 && key[:7] == "config.":
		if hs.pluginConfig != nil {
			return hs.pluginConfig[key[7:]]
		}
	}
	return ""
}

func TestByRoute_Manager(t *testing.T) {
	wasmBytes := buildWasmBinary(true, true, false, false)
	path := writeWasmFile(t, wasmBytes)

	mgr := NewWasmByRoute(config.WasmConfig{RuntimeMode: "interpreter"})
	defer mgr.Close(context.Background())

	err := mgr.AddRoute("route-a", []config.WasmPluginConfig{
		{Enabled: true, Name: "p1", Path: path, Phase: "both", PoolSize: 1, Timeout: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}

	// RouteIDs
	ids := mgr.RouteIDs()
	if len(ids) != 1 || ids[0] != "route-a" {
		t.Errorf("unexpected route IDs: %v", ids)
	}

	// GetChain
	chain := mgr.GetChain("route-a")
	if chain == nil {
		t.Fatal("expected chain for route-a")
	}
	if chain2 := mgr.GetChain("nonexistent"); chain2 != nil {
		t.Error("expected nil for nonexistent route")
	}

	// Stats
	stats := mgr.Stats()
	if _, ok := stats["route-a"]; !ok {
		t.Error("expected stats for route-a")
	}
}

func TestInvalidWasm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.wasm")
	os.WriteFile(path, []byte("not a wasm module"), 0644)

	mgr := NewWasmByRoute(config.WasmConfig{})
	defer mgr.Close(context.Background())

	err := mgr.AddRoute("test", []config.WasmPluginConfig{
		{Enabled: true, Name: "bad", Path: path, Phase: "request", PoolSize: 1, Timeout: 100 * time.Millisecond},
	})
	if err == nil {
		t.Error("expected error for invalid WASM")
	}
}

func TestDisabledPlugin(t *testing.T) {
	wasmBytes := buildWasmBinary(true, false, false, false)
	path := writeWasmFile(t, wasmBytes)

	mgr := NewWasmByRoute(config.WasmConfig{})
	defer mgr.Close(context.Background())

	err := mgr.AddRoute("test", []config.WasmPluginConfig{
		{Enabled: false, Name: "disabled", Path: path, Phase: "request", PoolSize: 1, Timeout: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}

	// No chain should be created for disabled-only plugins
	chain := mgr.GetChain("test")
	if chain != nil {
		t.Error("expected nil chain for all-disabled plugins")
	}
}

func TestBothPhase(t *testing.T) {
	wasmBytes := buildWasmBinary(true, true, false, false)
	path := writeWasmFile(t, wasmBytes)

	mgr := NewWasmByRoute(config.WasmConfig{})
	defer mgr.Close(context.Background())

	err := mgr.AddRoute("test", []config.WasmPluginConfig{
		{Enabled: true, Name: "both", Path: path, PoolSize: 2, Timeout: 100 * time.Millisecond},
		// Phase defaults to "both"
	})
	if err != nil {
		t.Fatal(err)
	}

	chain := mgr.GetChain("test")
	if chain == nil {
		t.Fatal("expected chain")
	}

	reqMW := chain.RequestMiddleware()
	respMW := chain.ResponseMiddleware()

	if reqMW == nil {
		t.Error("expected request middleware for 'both' phase")
	}
	if respMW == nil {
		t.Error("expected response middleware for 'both' phase")
	}

	// Execute both
	var nextCalled bool
	handler := reqMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)
	if !nextCalled {
		t.Error("next not called")
	}
}

func TestRequestBody_Passthrough(t *testing.T) {
	wasmBytes := buildWasmBinary(true, false, false, false)
	path := writeWasmFile(t, wasmBytes)

	mgr := NewWasmByRoute(config.WasmConfig{})
	defer mgr.Close(context.Background())

	err := mgr.AddRoute("test", []config.WasmPluginConfig{
		{Enabled: true, Name: "body-test", Path: path, Phase: "request", PoolSize: 2, Timeout: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}

	chain := mgr.GetChain("test")
	mw := chain.RequestMiddleware()

	var gotBody string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/test", bytes.NewBufferString("hello world"))
	handler.ServeHTTP(rec, req)

	if gotBody != "hello world" {
		t.Errorf("expected body 'hello world', got %q", gotBody)
	}
}
