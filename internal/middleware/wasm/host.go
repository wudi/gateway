package wasm

import (
	"context"
	"net/http"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"

)

type ctxKey struct{}

// hostState holds mutable state accessible by host functions during a single invocation.
type hostState struct {
	req           *http.Request
	reqHeaders    http.Header
	respHeaders   http.Header
	reqBody       []byte
	respBody      []byte
	earlyResponse *EarlyResponse
	pluginConfig  map[string]string
	routeID       string
	scheme        string
	logger        *zap.Logger
}

func contextWithHostState(ctx context.Context, hs *hostState) context.Context {
	return context.WithValue(ctx, ctxKey{}, hs)
}

func hostStateFromContext(ctx context.Context) *hostState {
	if v := ctx.Value(ctxKey{}); v != nil {
		return v.(*hostState)
	}
	return nil
}

// readGuestString reads a string from guest memory at the given offset and length.
func readGuestString(mod api.Module, ptr, length uint32) (string, bool) {
	if length == 0 {
		return "", true
	}
	data, ok := mod.Memory().Read(ptr, length)
	if !ok {
		return "", false
	}
	return string(data), true
}

// readGuestBytes reads bytes from guest memory at the given offset and length.
func readGuestBytes(mod api.Module, ptr, length uint32) ([]byte, bool) {
	if length == 0 {
		return nil, true
	}
	return mod.Memory().Read(ptr, length)
}

// writeGuestMemory writes data into guest memory at the given offset, checking capacity.
func writeGuestMemory(mod api.Module, ptr, cap uint32, data []byte) int32 {
	if uint32(len(data)) > cap {
		return -1
	}
	if len(data) == 0 {
		return 0
	}
	if !mod.Memory().Write(ptr, data) {
		return -1
	}
	return int32(len(data))
}

// registerHostFunctions registers all host_* functions in the "env" module.
func registerHostFunctions(rt wazero.Runtime) (wazero.CompiledModule, error) {
	env := rt.NewHostModuleBuilder("env")

	env.NewFunctionBuilder().
		WithFunc(hostLog).
		WithParameterNames("level", "msg_ptr", "msg_len").
		Export("host_log")

	env.NewFunctionBuilder().
		WithFunc(hostGetHeader).
		WithParameterNames("map_type", "key_ptr", "key_len", "val_ptr", "val_cap").
		Export("host_get_header")

	env.NewFunctionBuilder().
		WithFunc(hostSetHeader).
		WithParameterNames("map_type", "key_ptr", "key_len", "val_ptr", "val_len").
		Export("host_set_header")

	env.NewFunctionBuilder().
		WithFunc(hostRemoveHeader).
		WithParameterNames("map_type", "key_ptr", "key_len").
		Export("host_remove_header")

	env.NewFunctionBuilder().
		WithFunc(hostGetBody).
		WithParameterNames("buf_ptr", "buf_cap").
		Export("host_get_body")

	env.NewFunctionBuilder().
		WithFunc(hostSetBody).
		WithParameterNames("buf_ptr", "buf_len").
		Export("host_set_body")

	env.NewFunctionBuilder().
		WithFunc(hostGetProperty).
		WithParameterNames("key_ptr", "key_len", "val_ptr", "val_cap").
		Export("host_get_property")

	env.NewFunctionBuilder().
		WithFunc(hostSendResponse).
		WithParameterNames("status", "body_ptr", "body_len").
		Export("host_send_response")

	return env.Compile(context.Background())
}

// --- Host function implementations ---

func hostLog(ctx context.Context, mod api.Module, level, msgPtr, msgLen uint32) {
	hs := hostStateFromContext(ctx)
	if hs == nil {
		return
	}
	msg, ok := readGuestString(mod, msgPtr, msgLen)
	if !ok {
		return
	}
	logger := hs.logger
	if logger == nil {
		return
	}
	switch level {
	case LogLevelTrace, LogLevelDebug:
		logger.Debug("wasm plugin", zap.String("msg", msg))
	case LogLevelInfo:
		logger.Info("wasm plugin", zap.String("msg", msg))
	case LogLevelWarn:
		logger.Warn("wasm plugin", zap.String("msg", msg))
	case LogLevelError:
		logger.Error("wasm plugin", zap.String("msg", msg))
	default:
		logger.Info("wasm plugin", zap.String("msg", msg))
	}
}

func hostGetHeader(ctx context.Context, mod api.Module, mapType, keyPtr, keyLen, valPtr, valCap uint32) int32 {
	hs := hostStateFromContext(ctx)
	if hs == nil {
		return -1
	}
	key, ok := readGuestString(mod, keyPtr, keyLen)
	if !ok {
		return -1
	}
	var headers http.Header
	switch mapType {
	case MapTypeRequestHeaders:
		headers = hs.reqHeaders
	case MapTypeResponseHeaders:
		headers = hs.respHeaders
	default:
		return -1
	}
	if headers == nil {
		return -1
	}
	val := headers.Get(key)
	if val == "" {
		return 0
	}
	return writeGuestMemory(mod, valPtr, valCap, []byte(val))
}

func hostSetHeader(ctx context.Context, mod api.Module, mapType, keyPtr, keyLen, valPtr, valLen uint32) {
	hs := hostStateFromContext(ctx)
	if hs == nil {
		return
	}
	key, ok := readGuestString(mod, keyPtr, keyLen)
	if !ok {
		return
	}
	val, ok := readGuestString(mod, valPtr, valLen)
	if !ok {
		return
	}
	switch mapType {
	case MapTypeRequestHeaders:
		if hs.reqHeaders != nil {
			hs.reqHeaders.Set(key, val)
		}
	case MapTypeResponseHeaders:
		if hs.respHeaders != nil {
			hs.respHeaders.Set(key, val)
		}
	}
}

func hostRemoveHeader(ctx context.Context, mod api.Module, mapType, keyPtr, keyLen uint32) {
	hs := hostStateFromContext(ctx)
	if hs == nil {
		return
	}
	key, ok := readGuestString(mod, keyPtr, keyLen)
	if !ok {
		return
	}
	switch mapType {
	case MapTypeRequestHeaders:
		if hs.reqHeaders != nil {
			hs.reqHeaders.Del(key)
		}
	case MapTypeResponseHeaders:
		if hs.respHeaders != nil {
			hs.respHeaders.Del(key)
		}
	}
}

func hostGetBody(ctx context.Context, mod api.Module, bufPtr, bufCap uint32) int32 {
	hs := hostStateFromContext(ctx)
	if hs == nil {
		return -1
	}
	var body []byte
	if hs.respBody != nil {
		body = hs.respBody
	} else {
		body = hs.reqBody
	}
	if len(body) == 0 {
		return 0
	}
	return writeGuestMemory(mod, bufPtr, bufCap, body)
}

func hostSetBody(ctx context.Context, mod api.Module, bufPtr, bufLen uint32) {
	hs := hostStateFromContext(ctx)
	if hs == nil {
		return
	}
	data, ok := readGuestBytes(mod, bufPtr, bufLen)
	if !ok {
		return
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	if hs.respBody != nil {
		hs.respBody = cp
	} else {
		hs.reqBody = cp
	}
}

func hostGetProperty(ctx context.Context, mod api.Module, keyPtr, keyLen, valPtr, valCap uint32) int32 {
	hs := hostStateFromContext(ctx)
	if hs == nil {
		return -1
	}
	key, ok := readGuestString(mod, keyPtr, keyLen)
	if !ok {
		return -1
	}

	var val string
	switch {
	case key == "method" && hs.req != nil:
		val = hs.req.Method
	case key == "path" && hs.req != nil:
		val = hs.req.URL.Path
	case key == "query" && hs.req != nil:
		val = hs.req.URL.RawQuery
	case key == "host" && hs.req != nil:
		val = hs.req.Host
	case key == "scheme":
		val = hs.scheme
	case key == "route_id":
		val = hs.routeID
	case key == "client_ip" && hs.req != nil:
		val = hs.req.RemoteAddr
	case len(key) > 7 && key[:7] == "config.":
		if hs.pluginConfig != nil {
			val = hs.pluginConfig[key[7:]]
		}
	}

	if val == "" {
		return 0
	}
	return writeGuestMemory(mod, valPtr, valCap, []byte(val))
}

func hostSendResponse(ctx context.Context, mod api.Module, status, bodyPtr, bodyLen uint32) {
	hs := hostStateFromContext(ctx)
	if hs == nil {
		return
	}
	var body []byte
	if bodyLen > 0 {
		data, ok := readGuestBytes(mod, bodyPtr, bodyLen)
		if !ok {
			return
		}
		body = make([]byte, len(data))
		copy(body, data)
	}
	hs.earlyResponse = &EarlyResponse{
		StatusCode: int(status),
		Body:       body,
	}
}
