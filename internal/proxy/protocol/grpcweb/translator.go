package grpcweb

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/internal/proxy/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// rawCodec is a gRPC codec that passes bytes through without marshaling/unmarshaling.
type rawCodec struct{}

func (rawCodec) Marshal(v interface{}) ([]byte, error) {
	b, ok := v.(*[]byte)
	if !ok {
		return nil, fmt.Errorf("rawCodec: expected *[]byte, got %T", v)
	}
	return *b, nil
}

func (rawCodec) Unmarshal(data []byte, v interface{}) error {
	b, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("rawCodec: expected *[]byte, got %T", v)
	}
	*b = append((*b)[:0], data...)
	return nil
}

func (rawCodec) Name() string { return "proto" }

// Translator implements gRPC-Web to gRPC protocol translation.
// It accepts gRPC-Web requests from browsers and proxies them to gRPC backends,
// handling frame encoding/decoding and optional base64 text mode.
type Translator struct {
	routeMetrics map[string]*protocol.RouteMetrics
	metricsMu    sync.RWMutex

	connPool sync.Map // backend URL → *grpc.ClientConn
}

// New creates a new gRPC-Web translator.
func New() *Translator {
	return &Translator{
		routeMetrics: make(map[string]*protocol.RouteMetrics),
	}
}

// Name returns the protocol type identifier.
func (t *Translator) Name() string {
	return "grpc_web"
}

// Handler returns an http.Handler that translates gRPC-Web requests to native gRPC.
func (t *Translator) Handler(routeID string, balancer loadbalancer.Balancer, cfg config.ProtocolConfig) (http.Handler, error) {
	t.metricsMu.Lock()
	t.routeMetrics[routeID] = &protocol.RouteMetrics{}
	t.metricsMu.Unlock()

	timeout := cfg.GRPCWeb.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	maxMsgSize := cfg.GRPCWeb.MaxMessageSize
	if maxMsgSize == 0 {
		maxMsgSize = defaultMaxMessageSize
	}

	// Default text_mode to true if not explicitly set (zero value is false,
	// but we treat the default as enabled). The config struct uses a bool,
	// so we respect whatever the user sets. If they set text_mode: false,
	// text mode requests will be rejected.
	textModeEnabled := cfg.GRPCWeb.TextMode

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.serveHTTP(w, r, routeID, balancer, cfg, timeout, maxMsgSize, textModeEnabled)
	}), nil
}

func (t *Translator) serveHTTP(
	w http.ResponseWriter,
	r *http.Request,
	routeID string,
	balancer loadbalancer.Balancer,
	cfg config.ProtocolConfig,
	timeout time.Duration,
	maxMsgSize int,
	textModeEnabled bool,
) {
	start := time.Now()

	t.metricsMu.RLock()
	metrics := t.routeMetrics[routeID]
	t.metricsMu.RUnlock()

	metrics.Requests.Add(1)

	// Validate content type.
	ct := r.Header.Get("Content-Type")
	if !isGRPCWebContentType(ct) {
		t.writeError(w, 400, "0", "invalid content type: expected application/grpc-web")
		metrics.Failures.Add(1)
		return
	}

	textMode := isTextMode(ct)
	if textMode && !textModeEnabled {
		t.writeError(w, 400, "0", "grpc-web-text mode is disabled")
		metrics.Failures.Add(1)
		return
	}

	// Only POST is allowed for gRPC-Web.
	if r.Method != http.MethodPost {
		t.writeError(w, 405, "0", "only POST method is allowed for gRPC-Web")
		metrics.Failures.Add(1)
		return
	}

	// Parse service/method from URL path.
	serviceName, methodName, err := parseGRPCWebPath(r.URL.Path)
	if err != nil {
		t.writeError(w, 400, "2", err.Error())
		metrics.Failures.Add(1)
		return
	}

	// Read request body.
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		t.writeError(w, 400, "2", fmt.Sprintf("failed to read request body: %v", err))
		metrics.Failures.Add(1)
		return
	}

	// Base64 decode if text mode.
	if textMode {
		rawBody, err = base64Decode(rawBody)
		if err != nil {
			t.writeError(w, 400, "2", fmt.Sprintf("base64 decode failed: %v", err))
			metrics.Failures.Add(1)
			return
		}
	}

	// Decode gRPC-Web frame to get the protobuf payload.
	frame, err := decodeGRPCWebFrame(bytes.NewReader(rawBody), maxMsgSize)
	if err != nil {
		t.writeError(w, 400, "2", fmt.Sprintf("failed to decode grpc-web frame: %v", err))
		metrics.Failures.Add(1)
		return
	}

	// Select backend.
	backend := balancer.Next()
	if backend == nil {
		t.writeError(w, 503, "14", "no healthy backend available")
		metrics.Failures.Add(1)
		return
	}

	// Get or create gRPC connection.
	conn, err := t.getConnection(backend.URL, cfg.GRPCWeb.TLS)
	if err != nil {
		t.writeError(w, 503, "14", fmt.Sprintf("failed to connect to backend: %v", err))
		metrics.Failures.Add(1)
		return
	}

	// Create context with timeout and forward metadata.
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Forward relevant headers as gRPC metadata.
	md := extractMetadata(r)
	if len(md) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	fullMethod := "/" + serviceName + "/" + methodName
	reqPayload := frame.Payload

	// Branch: server streaming or unary.
	if isServerStreaming(r) {
		t.serveServerStream(w, conn, ctx, fullMethod, methodName, reqPayload, textMode, metrics, start)
		return
	}

	// Invoke the unary RPC using raw codec.
	var respPayload []byte
	var headerMD, trailerMD metadata.MD
	err = conn.Invoke(ctx, fullMethod, &reqPayload, &respPayload,
		grpc.Header(&headerMD),
		grpc.Trailer(&trailerMD),
	)

	if err != nil {
		st, ok := status.FromError(err)
		if !ok {
			t.writeGRPCWebError(w, textMode, "13", err.Error())
		} else {
			t.writeGRPCWebError(w, textMode, fmt.Sprintf("%d", st.Code()), st.Message())
		}
		metrics.Failures.Add(1)
		return
	}

	// Write successful gRPC-Web response.
	t.writeGRPCWebResponse(w, textMode, respPayload, headerMD, trailerMD)

	metrics.Successes.Add(1)
	metrics.TotalLatencyNs.Add(time.Since(start).Nanoseconds())
}

// isServerStreaming returns true if the request signals server-streaming mode
// via query parameter (?streaming=server) or header (X-Grpc-Web-Streaming: server).
func isServerStreaming(r *http.Request) bool {
	if r.URL.Query().Get("streaming") == "server" {
		return true
	}
	return r.Header.Get("X-Grpc-Web-Streaming") == "server"
}

// serveServerStream handles a server-streaming RPC over gRPC-Web.
// It creates a gRPC stream, sends the single request, then loops over responses,
// writing each as a gRPC-Web data frame flushed to the client in real time.
func (t *Translator) serveServerStream(
	w http.ResponseWriter,
	conn *grpc.ClientConn,
	ctx context.Context,
	fullMethod string,
	methodName string,
	reqPayload []byte,
	textMode bool,
	metrics *protocol.RouteMetrics,
	start time.Time,
) {
	streamDesc := &grpc.StreamDesc{
		StreamName:    methodName,
		ServerStreams: true,
	}

	stream, err := conn.NewStream(ctx, streamDesc, fullMethod)
	if err != nil {
		st, ok := status.FromError(err)
		if !ok {
			t.writeGRPCWebError(w, textMode, "13", err.Error())
		} else {
			t.writeGRPCWebError(w, textMode, fmt.Sprintf("%d", st.Code()), st.Message())
		}
		metrics.Failures.Add(1)
		return
	}

	// Send the single request message and close the send direction.
	if err := stream.SendMsg(&reqPayload); err != nil {
		st, ok := status.FromError(err)
		if !ok {
			t.writeGRPCWebError(w, textMode, "13", err.Error())
		} else {
			t.writeGRPCWebError(w, textMode, fmt.Sprintf("%d", st.Code()), st.Message())
		}
		metrics.Failures.Add(1)
		return
	}
	if err := stream.CloseSend(); err != nil {
		t.writeGRPCWebError(w, textMode, "13", fmt.Sprintf("failed to close send: %v", err))
		metrics.Failures.Add(1)
		return
	}

	// Read response headers (initial metadata).
	headerMD, _ := stream.Header()

	// Write HTTP headers before the streaming loop.
	if textMode {
		w.Header().Set("Content-Type", "application/grpc-web-text+proto")
	} else {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
	}
	for k, vals := range headerMD {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)

	// Receive loop: each message becomes one gRPC-Web data frame.
	for {
		var respBytes []byte
		if err := stream.RecvMsg(&respBytes); err != nil {
			if err == io.EOF {
				// Normal end of stream — write success trailer.
				trailers := map[string]string{
					"grpc-status": "0",
				}
				for k, vals := range stream.Trailer() {
					if len(vals) > 0 {
						trailers[k] = vals[0]
					}
				}
				trailerFrame := encodeTrailerFrame(trailers)
				if textMode {
					w.Write(base64Encode(trailerFrame))
				} else {
					w.Write(trailerFrame)
				}
				if canFlush {
					flusher.Flush()
				}
				metrics.Successes.Add(1)
				metrics.TotalLatencyNs.Add(time.Since(start).Nanoseconds())
				return
			}

			// Error — write error trailer.
			trailers := map[string]string{}
			st, ok := status.FromError(err)
			if ok {
				trailers["grpc-status"] = fmt.Sprintf("%d", st.Code())
				trailers["grpc-message"] = st.Message()
			} else {
				trailers["grpc-status"] = "13"
				trailers["grpc-message"] = err.Error()
			}
			trailerFrame := encodeTrailerFrame(trailers)
			if textMode {
				w.Write(base64Encode(trailerFrame))
			} else {
				w.Write(trailerFrame)
			}
			if canFlush {
				flusher.Flush()
			}
			metrics.Failures.Add(1)
			return
		}

		// Write data frame and flush.
		dataFrame := encodeDataFrame(respBytes)
		if textMode {
			w.Write(base64Encode(dataFrame))
		} else {
			w.Write(dataFrame)
		}
		if canFlush {
			flusher.Flush()
		}
	}
}

// writeGRPCWebResponse writes a successful gRPC-Web response with data frame + trailer frame.
func (t *Translator) writeGRPCWebResponse(w http.ResponseWriter, textMode bool, data []byte, headerMD, trailerMD metadata.MD) {
	if textMode {
		w.Header().Set("Content-Type", "application/grpc-web-text+proto")
	} else {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
	}

	// Forward response headers from gRPC metadata.
	for k, vals := range headerMD {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(http.StatusOK)

	// Build data frame.
	dataFrame := encodeDataFrame(data)

	// Build trailer frame with grpc-status and any trailing metadata.
	trailers := map[string]string{
		"grpc-status": "0",
	}
	for k, vals := range trailerMD {
		if len(vals) > 0 {
			trailers[k] = vals[0]
		}
	}
	trailerFrame := encodeTrailerFrame(trailers)

	if textMode {
		// In text mode, each frame is base64-encoded independently.
		w.Write(base64Encode(dataFrame))
		w.Write(base64Encode(trailerFrame))
	} else {
		w.Write(dataFrame)
		w.Write(trailerFrame)
	}
}

// writeGRPCWebError writes a gRPC-Web error response as a trailer-only response.
func (t *Translator) writeGRPCWebError(w http.ResponseWriter, textMode bool, grpcStatus string, grpcMessage string) {
	if textMode {
		w.Header().Set("Content-Type", "application/grpc-web-text+proto")
	} else {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
	}
	w.WriteHeader(http.StatusOK)

	trailers := map[string]string{
		"grpc-status":  grpcStatus,
		"grpc-message": grpcMessage,
	}
	trailerFrame := encodeTrailerFrame(trailers)

	if textMode {
		w.Write(base64Encode(trailerFrame))
	} else {
		w.Write(trailerFrame)
	}
}

// writeError writes a plain HTTP error (for non-gRPC-Web error cases like bad content type).
func (t *Translator) writeError(w http.ResponseWriter, httpStatus int, grpcStatus string, message string) {
	w.Header().Set("Content-Type", "application/grpc-web+proto")
	w.Header().Set("Grpc-Status", grpcStatus)
	w.Header().Set("Grpc-Message", message)
	w.WriteHeader(httpStatus)
}

// parseGRPCWebPath extracts service and method from a gRPC-Web URL path.
// Expected format: /package.Service/Method (same as native gRPC).
func parseGRPCWebPath(path string) (service, method string, err error) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", "", fmt.Errorf("empty path")
	}

	lastSlash := strings.LastIndex(path, "/")
	if lastSlash == -1 {
		return "", "", fmt.Errorf("invalid gRPC-Web path format, expected /package.Service/Method")
	}

	service = path[:lastSlash]
	method = path[lastSlash+1:]

	if service == "" || method == "" {
		return "", "", fmt.Errorf("invalid gRPC-Web path format, expected /package.Service/Method")
	}

	return service, method, nil
}

// extractMetadata extracts gRPC metadata from HTTP request headers.
func extractMetadata(r *http.Request) metadata.MD {
	md := metadata.MD{}
	for k, vals := range r.Header {
		key := strings.ToLower(k)
		// Skip standard HTTP headers and gRPC-Web specific headers.
		switch key {
		case "content-type", "content-length", "accept",
			"user-agent", "host", "connection",
			"transfer-encoding", "te",
			"x-grpc-web-streaming":
			continue
		}
		md[key] = vals
	}
	return md
}

// getConnection returns a gRPC connection for the backend, creating one if needed.
func (t *Translator) getConnection(backendURL string, tlsCfg config.ProtocolTLSConfig) (*grpc.ClientConn, error) {
	if existing, ok := t.connPool.Load(backendURL); ok {
		return existing.(*grpc.ClientConn), nil
	}

	target := backendURL
	target = strings.TrimPrefix(target, "grpc://")
	target = strings.TrimPrefix(target, "http://")
	target = strings.TrimPrefix(target, "https://")

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))

	if tlsCfg.Enabled {
		creds, err := t.buildTLSCredentials(tlsCfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.Dial(target, opts...)
	if err != nil {
		return nil, err
	}

	// Store in pool (race-safe: if another goroutine stored first, close ours).
	actual, loaded := t.connPool.LoadOrStore(backendURL, conn)
	if loaded {
		conn.Close()
		return actual.(*grpc.ClientConn), nil
	}

	return conn, nil
}

func (t *Translator) buildTLSCredentials(cfg config.ProtocolTLSConfig) (credentials.TransportCredentials, error) {
	tlsConfig := &tls.Config{}

	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = certPool
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return credentials.NewTLS(tlsConfig), nil
}

// Close releases resources for the specified route.
func (t *Translator) Close(routeID string) error {
	t.metricsMu.Lock()
	delete(t.routeMetrics, routeID)
	t.metricsMu.Unlock()
	return nil
}

// CloseAll closes all gRPC connections.
func (t *Translator) CloseAll() {
	t.connPool.Range(func(key, value interface{}) bool {
		if conn, ok := value.(*grpc.ClientConn); ok {
			conn.Close()
		}
		t.connPool.Delete(key)
		return true
	})
}

// Metrics returns metrics for the specified route.
func (t *Translator) Metrics(routeID string) *protocol.TranslatorMetrics {
	t.metricsMu.RLock()
	defer t.metricsMu.RUnlock()

	if m, ok := t.routeMetrics[routeID]; ok {
		return m.Snapshot(t.Name())
	}
	return nil
}
