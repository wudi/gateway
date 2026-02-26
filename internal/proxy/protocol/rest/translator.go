// Package rest implements gRPC-to-REST protocol translation.
// It accepts native gRPC requests (application/grpc) and translates them
// to REST/HTTP backend calls based on configured mappings.
package rest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/internal/proxy/protocol"
)

// Translator implements protocol.Translator for gRPC-to-REST.
type Translator struct {
	mu           sync.RWMutex
	routeMetrics map[string]*protocol.RouteMetrics
}

// New creates a new gRPC-to-REST translator.
func New() *Translator {
	return &Translator{
		routeMetrics: make(map[string]*protocol.RouteMetrics),
	}
}

// Name returns the protocol type identifier.
func (t *Translator) Name() string {
	return "grpc_to_rest"
}

// Handler creates an http.Handler for the given route.
func (t *Translator) Handler(routeID string, balancer loadbalancer.Balancer, cfg config.ProtocolConfig) (http.Handler, error) {
	restCfg := cfg.REST

	registry, err := newMappingRegistry(restCfg.Mappings)
	if err != nil {
		return nil, fmt.Errorf("compiling mappings: %w", err)
	}

	// Load proto descriptors if provided
	var descReg *descriptorRegistry
	if len(restCfg.DescriptorFiles) > 0 {
		descReg, err = newDescriptorRegistry(restCfg.DescriptorFiles)
		if err != nil {
			return nil, fmt.Errorf("loading descriptors: %w", err)
		}
	}

	timeout := restCfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	t.mu.Lock()
	t.routeMetrics[routeID] = &protocol.RouteMetrics{}
	t.mu.Unlock()

	h := &handler{
		routeID:   routeID,
		balancer:  balancer,
		mappings:  registry,
		descReg:   descReg,
		timeout:   timeout,
		metrics:   t.routeMetrics[routeID],
		client:    &http.Client{Timeout: timeout},
	}

	return h, nil
}

// Close releases resources for the specified route.
func (t *Translator) Close(routeID string) error {
	t.mu.Lock()
	delete(t.routeMetrics, routeID)
	t.mu.Unlock()
	return nil
}

// Metrics returns metrics for the specified route.
func (t *Translator) Metrics(routeID string) *protocol.TranslatorMetrics {
	t.mu.RLock()
	m, ok := t.routeMetrics[routeID]
	t.mu.RUnlock()
	if !ok {
		return nil
	}
	return m.Snapshot("grpc_to_rest")
}

// handler implements http.Handler for a single route.
type handler struct {
	routeID  string
	balancer loadbalancer.Balancer
	mappings *mappingRegistry
	descReg  *descriptorRegistry
	timeout  time.Duration
	metrics  *protocol.RouteMetrics
	client   *http.Client
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	h.metrics.Requests.Add(1)

	// Parse gRPC path: /fully.qualified.ServiceName/MethodName
	grpcPath := r.URL.Path
	mapping := h.mappings.lookup(grpcPath)
	if mapping == nil {
		writeGRPCError(w, 12, fmt.Sprintf("no mapping for gRPC path %s", grpcPath)) // UNIMPLEMENTED
		h.metrics.Failures.Add(1)
		return
	}

	// Read gRPC request body
	body, _, err := decodeGRPCFrame(r.Body)
	if err != nil && err != io.EOF {
		writeGRPCError(w, 2, fmt.Sprintf("failed to decode gRPC frame: %v", err)) // UNKNOWN
		h.metrics.Failures.Add(1)
		return
	}

	// Convert request body: protobuf → JSON
	var jsonBody []byte
	var messageFields map[string]interface{}

	if h.descReg != nil && len(body) > 0 {
		// Use proto descriptors for proper protobuf → JSON conversion
		md, err := h.descReg.findMethod(mapping.GRPCService, mapping.GRPCMethod)
		if err != nil {
			writeGRPCError(w, 2, fmt.Sprintf("descriptor lookup: %v", err))
			h.metrics.Failures.Add(1)
			return
		}

		msg := h.descReg.newInputMessage(md)
		if err := proto.Unmarshal(body, msg); err != nil {
			writeGRPCError(w, 3, fmt.Sprintf("failed to unmarshal protobuf: %v", err)) // INVALID_ARGUMENT
			h.metrics.Failures.Add(1)
			return
		}

		jsonBody, err = protojson.Marshal(msg)
		if err != nil {
			writeGRPCError(w, 13, fmt.Sprintf("failed to marshal to JSON: %v", err)) // INTERNAL
			h.metrics.Failures.Add(1)
			return
		}

		if err := json.Unmarshal(jsonBody, &messageFields); err != nil {
			messageFields = make(map[string]interface{})
		}
	} else if len(body) > 0 {
		// No descriptors — treat body as raw JSON (gRPC-web JSON mode)
		jsonBody = body
		if err := json.Unmarshal(body, &messageFields); err != nil {
			messageFields = make(map[string]interface{})
		}
	} else {
		messageFields = make(map[string]interface{})
	}

	// Build REST request
	restPath := mapping.buildPath(messageFields)

	// Select backend
	backend := h.balancer.Next()
	if backend == nil {
		writeGRPCError(w, 14, "no backends available") // UNAVAILABLE
		h.metrics.Failures.Add(1)
		return
	}

	targetURL := strings.TrimRight(backend.URL, "/") + restPath

	// Build request body based on mapping.Body config
	var reqBody io.Reader
	if mapping.Body == "*" && len(jsonBody) > 0 {
		// Strip path variable fields from the body
		varNames := mapping.variableNames()
		if len(varNames) > 0 && messageFields != nil {
			cleaned := make(map[string]interface{})
			for k, v := range messageFields {
				skip := false
				for _, vn := range varNames {
					if k == vn {
						skip = true
						break
					}
				}
				if !skip {
					cleaned[k] = v
				}
			}
			jsonBody, _ = json.Marshal(cleaned)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	restReq, err := http.NewRequestWithContext(r.Context(), mapping.HTTPMethod, targetURL, reqBody)
	if err != nil {
		writeGRPCError(w, 13, fmt.Sprintf("failed to create REST request: %v", err))
		h.metrics.Failures.Add(1)
		return
	}

	if reqBody != nil {
		restReq.Header.Set("Content-Type", "application/json")
	}

	// Forward relevant headers
	for _, hdr := range []string{"Authorization", "X-Request-Id", "X-Correlation-Id"} {
		if v := r.Header.Get(hdr); v != "" {
			restReq.Header.Set(hdr, v)
		}
	}

	// Make REST call
	resp, err := h.client.Do(restReq)
	if err != nil {
		writeGRPCError(w, 14, fmt.Sprintf("REST backend call failed: %v", err)) // UNAVAILABLE
		h.metrics.Failures.Add(1)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		writeGRPCError(w, 13, fmt.Sprintf("failed to read REST response: %v", err))
		h.metrics.Failures.Add(1)
		return
	}

	// Map HTTP status to gRPC status
	grpcStatus := httpToGRPCStatus(resp.StatusCode)
	if grpcStatus != 0 {
		writeGRPCError(w, grpcStatus, string(respBody))
		h.metrics.Failures.Add(1)
		return
	}

	// Convert response: JSON → protobuf (if descriptors available)
	var responseData []byte
	if h.descReg != nil && len(respBody) > 0 {
		md, err := h.descReg.findMethod(mapping.GRPCService, mapping.GRPCMethod)
		if err != nil {
			writeGRPCError(w, 13, fmt.Sprintf("descriptor lookup for response: %v", err))
			h.metrics.Failures.Add(1)
			return
		}

		outMsg := h.descReg.newOutputMessage(md)
		if err := protojson.Unmarshal(respBody, outMsg); err != nil {
			writeGRPCError(w, 13, fmt.Sprintf("failed to unmarshal JSON response to proto: %v", err))
			h.metrics.Failures.Add(1)
			return
		}

		responseData, err = proto.Marshal(outMsg)
		if err != nil {
			writeGRPCError(w, 13, fmt.Sprintf("failed to marshal proto response: %v", err))
			h.metrics.Failures.Add(1)
			return
		}
	} else {
		// No descriptors — pass JSON as-is in gRPC frame
		responseData = respBody
	}

	// Write gRPC response
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Grpc-Status", "0")
	w.Header().Set("Grpc-Message", "")
	w.WriteHeader(http.StatusOK)

	var buf bytes.Buffer
	if err := encodeGRPCFrame(&buf, responseData, false); err != nil {
		return
	}
	w.Write(buf.Bytes())

	// Write trailers
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	h.metrics.Successes.Add(1)
	h.metrics.TotalLatencyNs.Add(time.Since(start).Nanoseconds())
}

// writeGRPCError writes a gRPC error response with trailers.
func writeGRPCError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Grpc-Status", fmt.Sprintf("%d", code))
	w.Header().Set("Grpc-Message", msg)
	w.WriteHeader(http.StatusOK) // gRPC always returns HTTP 200
}

// httpToGRPCStatus maps HTTP status codes to gRPC status codes.
func httpToGRPCStatus(httpStatus int) int {
	if httpStatus >= 200 && httpStatus < 300 {
		return 0 // OK
	}
	switch httpStatus {
	case 400:
		return 3 // INVALID_ARGUMENT
	case 401:
		return 16 // UNAUTHENTICATED
	case 403:
		return 7 // PERMISSION_DENIED
	case 404:
		return 5 // NOT_FOUND
	case 409:
		return 6 // ALREADY_EXISTS
	case 429:
		return 8 // RESOURCE_EXHAUSTED
	case 499:
		return 1 // CANCELLED
	case 501:
		return 12 // UNIMPLEMENTED
	case 503:
		return 14 // UNAVAILABLE
	case 504:
		return 4 // DEADLINE_EXCEEDED
	default:
		if httpStatus >= 500 {
			return 13 // INTERNAL
		}
		return 2 // UNKNOWN
	}
}
