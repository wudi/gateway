package grpcjson

import (
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Translator implements gRPC JSON codec proxy translation.
// It sends raw JSON bytes over the gRPC wire using a custom codec named "json"
// (content-type: application/grpc+json). No proto descriptors are required —
// both the gateway and the backend use the JSON codec natively.
type Translator struct {
	routeMetrics map[string]*protocol.RouteMetrics
	metricsMu    sync.RWMutex
	connPool     sync.Map // backend URL → *grpc.ClientConn
}

// New creates a new gRPC JSON codec translator.
func New() *Translator {
	return &Translator{
		routeMetrics: make(map[string]*protocol.RouteMetrics),
	}
}

// Name returns the protocol type identifier.
func (t *Translator) Name() string {
	return "grpc_json"
}

// Handler returns an http.Handler that proxies HTTP/JSON requests to a gRPC backend
// using the JSON codec.
func (t *Translator) Handler(routeID string, balancer loadbalancer.Balancer, cfg config.ProtocolConfig) (http.Handler, error) {
	t.metricsMu.Lock()
	t.routeMetrics[routeID] = &protocol.RouteMetrics{}
	t.metricsMu.Unlock()

	timeout := cfg.GRPCJson.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.serveHTTP(w, r, routeID, balancer, cfg, timeout)
	}), nil
}

func (t *Translator) serveHTTP(
	w http.ResponseWriter,
	r *http.Request,
	routeID string,
	balancer loadbalancer.Balancer,
	cfg config.ProtocolConfig,
	timeout time.Duration,
) {
	start := time.Now()

	t.metricsMu.RLock()
	metrics := t.routeMetrics[routeID]
	t.metricsMu.RUnlock()

	metrics.Requests.Add(1)

	// gRPC is always POST.
	if r.Method != http.MethodPost {
		t.writeError(w, codes.InvalidArgument, "only POST method is allowed for gRPC JSON proxy")
		metrics.Failures.Add(1)
		return
	}

	// Resolve the gRPC method path.
	fullMethod, err := resolveMethod(r.URL.Path, cfg.GRPCJson)
	if err != nil {
		t.writeError(w, codes.InvalidArgument, err.Error())
		metrics.Failures.Add(1)
		return
	}

	// Read JSON body.
	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		t.writeError(w, codes.InvalidArgument, fmt.Sprintf("failed to read request body: %v", err))
		metrics.Failures.Add(1)
		return
	}

	// Select backend.
	backend := balancer.Next()
	if backend == nil {
		t.writeError(w, codes.Unavailable, "no healthy backend available")
		metrics.Failures.Add(1)
		return
	}

	// Get or create pooled gRPC connection.
	conn, err := t.getConnection(backend.URL, cfg.GRPCJson.TLS)
	if err != nil {
		t.writeError(w, codes.Unavailable, fmt.Sprintf("failed to connect to backend: %v", err))
		metrics.Failures.Add(1)
		return
	}

	// Create context with timeout.
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Forward HTTP headers as gRPC metadata.
	md := extractMetadata(r)
	if len(md) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	// Invoke the unary RPC using the JSON codec.
	var respBody []byte
	var headerMD, trailerMD metadata.MD
	err = conn.Invoke(ctx, fullMethod, &reqBody, &respBody,
		grpc.Header(&headerMD),
		grpc.Trailer(&trailerMD),
	)

	if err != nil {
		st, ok := status.FromError(err)
		if !ok {
			t.writeError(w, codes.Internal, err.Error())
		} else {
			t.writeError(w, st.Code(), st.Message())
		}
		metrics.Failures.Add(1)
		return
	}

	// Forward response metadata as HTTP headers, filtering gRPC transport headers.
	for k, vals := range headerMD {
		key := strings.ToLower(k)
		switch key {
		case "content-type", "grpc-status", "grpc-message", "grpc-status-details-bin":
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}

	// Write successful JSON response.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBody)

	metrics.Successes.Add(1)
	metrics.TotalLatencyNs.Add(time.Since(start).Nanoseconds())
}

// resolveMethod determines the full gRPC method path from the URL and config.
// Three modes:
//   - Fixed: both Service and Method set → "/<service>/<method>"
//   - Service-scoped: only Service set → "/<service>/<lastURLSegment>"
//   - Path-based (default): URL path IS the gRPC method path
func resolveMethod(urlPath string, cfg config.GRPCJSONTranslateConfig) (string, error) {
	if cfg.Service != "" && cfg.Method != "" {
		// Fixed mode.
		return "/" + cfg.Service + "/" + cfg.Method, nil
	}

	if cfg.Service != "" {
		// Service-scoped mode: extract method name from last URL path segment.
		path := strings.TrimPrefix(urlPath, "/")
		if path == "" {
			return "", fmt.Errorf("empty path")
		}
		parts := strings.Split(path, "/")
		method := parts[len(parts)-1]
		if method == "" {
			return "", fmt.Errorf("method name is required in URL path")
		}
		return "/" + cfg.Service + "/" + method, nil
	}

	// Path-based mode: parse /package.Service/Method from URL.
	return parseMethodPath(urlPath)
}

// parseMethodPath validates and returns a gRPC method path in the form /Service/Method.
func parseMethodPath(path string) (string, error) {
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return "", fmt.Errorf("empty path")
	}

	lastSlash := strings.LastIndex(trimmed, "/")
	if lastSlash == -1 {
		return "", fmt.Errorf("invalid gRPC path format, expected /package.Service/Method")
	}

	service := trimmed[:lastSlash]
	method := trimmed[lastSlash+1:]

	if service == "" || method == "" {
		return "", fmt.Errorf("invalid gRPC path format, expected /package.Service/Method")
	}

	return "/" + service + "/" + method, nil
}

// extractMetadata extracts gRPC metadata from HTTP request headers,
// filtering out standard HTTP headers that shouldn't be forwarded.
func extractMetadata(r *http.Request) metadata.MD {
	md := metadata.MD{}
	for k, vals := range r.Header {
		key := strings.ToLower(k)
		switch key {
		case "content-type", "content-length", "accept", "accept-encoding",
			"user-agent", "host", "connection", "transfer-encoding", "te":
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
	opts = append(opts, grpc.WithDefaultCallOptions(grpc.ForceCodec(jsonCodec{})))

	if tlsCfg.Enabled {
		creds, err := t.buildTLSCredentials(tlsCfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// TODO: migrate to grpc.NewClient when bulk-updating all translators
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

// writeError writes a JSON error response with the appropriate HTTP status code.
func (t *Translator) writeError(w http.ResponseWriter, code codes.Code, message string) {
	httpStatus := protocol.GRPCStatusToHTTP(code)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	fmt.Fprintf(w, `{"code":%d,"message":%q,"details":[]}`, code, message)
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
