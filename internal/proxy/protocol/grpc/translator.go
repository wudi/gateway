package grpc

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

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/loadbalancer"
	"github.com/example/gateway/internal/proxy/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Translator implements HTTP-to-gRPC protocol translation.
type Translator struct {
	routeMetrics map[string]*protocol.RouteMetrics
	routeMappers map[string]*restMapper
	metricsMu    sync.RWMutex

	connPool  sync.Map // backend URL → *grpc.ClientConn
	descCache *descriptorCache
	invoker   *invoker
}

// New creates a new gRPC translator.
func New() *Translator {
	return &Translator{
		routeMetrics: make(map[string]*protocol.RouteMetrics),
		routeMappers: make(map[string]*restMapper),
		descCache:    newDescriptorCache(5 * time.Minute),
		invoker:      newInvoker(),
	}
}

// Name returns the protocol type identifier.
func (t *Translator) Name() string {
	return "http_to_grpc"
}

// Handler returns an http.Handler that translates HTTP/JSON to gRPC.
func (t *Translator) Handler(routeID string, balancer loadbalancer.Balancer, cfg config.ProtocolConfig) (http.Handler, error) {
	// Initialize route metrics
	t.metricsMu.Lock()
	t.routeMetrics[routeID] = &protocol.RouteMetrics{}
	t.metricsMu.Unlock()

	// Create REST mapper if mappings are configured
	if len(cfg.GRPC.Mappings) > 0 {
		mapper, err := newRESTMapper(cfg.GRPC.Service, cfg.GRPC.Mappings)
		if err != nil {
			return nil, fmt.Errorf("failed to create REST mapper: %w", err)
		}
		t.metricsMu.Lock()
		t.routeMappers[routeID] = mapper
		t.metricsMu.Unlock()
	}

	// Update descriptor cache TTL if configured
	if cfg.GRPC.DescriptorCacheTTL > 0 {
		t.descCache = newDescriptorCache(cfg.GRPC.DescriptorCacheTTL)
	}

	timeout := cfg.GRPC.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.serveHTTP(w, r, routeID, balancer, cfg, timeout)
	}), nil
}

func (t *Translator) serveHTTP(w http.ResponseWriter, r *http.Request, routeID string, balancer loadbalancer.Balancer, cfg config.ProtocolConfig, timeout time.Duration) {
	start := time.Now()

	t.metricsMu.RLock()
	metrics := t.routeMetrics[routeID]
	mapper := t.routeMappers[routeID]
	t.metricsMu.RUnlock()

	metrics.Requests.Add(1)

	var serviceName, methodName string
	var requestBody []byte
	var err error

	// Read request body first (needed for both modes)
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		t.writeError(w, codes.InvalidArgument, fmt.Sprintf("failed to read request body: %v", err))
		metrics.Failures.Add(1)
		return
	}

	// Priority order:
	// 1. Fixed method (service + method in config)
	// 2. REST mappings (if configured)
	// 3. Path-based resolution (default/service-scoped modes)
	if cfg.GRPC.Method != "" {
		// Fixed method mode: service and method both from config
		serviceName = cfg.GRPC.Service
		methodName = cfg.GRPC.Method
		requestBody = rawBody
	} else if mapper != nil {
		// REST-to-gRPC mode: match against configured mappings
		match := mapper.match(r.Method, r.URL.Path)
		if match == nil {
			t.writeError(w, codes.NotFound, fmt.Sprintf("no mapping found for %s %s", r.Method, r.URL.Path))
			metrics.Failures.Add(1)
			return
		}

		serviceName = mapper.serviceName()
		methodName = match.grpcMethod

		// Build request body from path params, query params, and body
		requestBody, err = mapper.buildRequestBody(r, match, rawBody)
		if err != nil {
			t.writeError(w, codes.InvalidArgument, err.Error())
			metrics.Failures.Add(1)
			return
		}
	} else {
		// Original mode: POST only, path-based method resolution
		if r.Method != http.MethodPost {
			t.writeError(w, codes.InvalidArgument, "only POST method is allowed for gRPC translation (use mappings for REST-style APIs)")
			metrics.Failures.Add(1)
			return
		}

		serviceName, methodName, err = t.resolveMethod(r.URL.Path, cfg.GRPC.Service)
		if err != nil {
			t.writeError(w, codes.InvalidArgument, err.Error())
			metrics.Failures.Add(1)
			return
		}
		requestBody = rawBody
	}

	// Select backend
	backend := balancer.Next()
	if backend == nil {
		t.writeError(w, codes.Unavailable, "no healthy backend available")
		metrics.Failures.Add(1)
		return
	}

	// Get or create gRPC connection
	conn, err := t.getConnection(backend.URL, cfg.GRPC.TLS)
	if err != nil {
		t.writeError(w, codes.Unavailable, fmt.Sprintf("failed to connect to backend: %v", err))
		metrics.Failures.Add(1)
		return
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Get service descriptor via reflection
	sd, err := t.descCache.getServiceDescriptor(ctx, conn, backend.URL, serviceName)
	if err != nil {
		t.writeError(w, codes.NotFound, fmt.Sprintf("service discovery failed: %v", err))
		metrics.Failures.Add(1)
		return
	}

	// Get method descriptor
	md, err := getMethodDescriptor(sd, methodName)
	if err != nil {
		t.writeError(w, codes.NotFound, err.Error())
		metrics.Failures.Add(1)
		return
	}

	// Check for streaming (not supported in this version)
	if md.IsStreamingClient() || md.IsStreamingServer() {
		t.writeError(w, codes.Unimplemented, "streaming methods are not supported")
		metrics.Failures.Add(1)
		return
	}

	// Invoke the RPC
	respJSON, err := t.invoker.invokeUnary(ctx, conn, md, requestBody)
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

	// Write successful response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respJSON)

	metrics.Successes.Add(1)
	metrics.TotalLatencyNs.Add(time.Since(start).Nanoseconds())
}

// resolveMethod extracts service and method names from the HTTP path.
// If serviceOverride is set, path is just /MethodName.
// Otherwise, path is /package.Service/MethodName.
func (t *Translator) resolveMethod(path string, serviceOverride string) (serviceName, methodName string, err error) {
	// Remove leading slash
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", "", fmt.Errorf("empty path")
	}

	if serviceOverride != "" {
		// Service-scoped mode: path is just the method name
		// Remove any leading path segments if the route has path prefix
		parts := strings.Split(path, "/")
		methodName = parts[len(parts)-1]
		if methodName == "" {
			return "", "", fmt.Errorf("method name is required")
		}
		return serviceOverride, methodName, nil
	}

	// Default mode: path is /package.Service/MethodName
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash == -1 {
		return "", "", fmt.Errorf("invalid gRPC path format, expected /package.Service/Method")
	}

	serviceName = path[:lastSlash]
	methodName = path[lastSlash+1:]

	if serviceName == "" || methodName == "" {
		return "", "", fmt.Errorf("invalid gRPC path format, expected /package.Service/Method")
	}

	return serviceName, methodName, nil
}

// getConnection returns a gRPC connection for the backend, creating one if needed.
func (t *Translator) getConnection(backendURL string, tlsCfg config.ProtocolTLSConfig) (*grpc.ClientConn, error) {
	if existing, ok := t.connPool.Load(backendURL); ok {
		return existing.(*grpc.ClientConn), nil
	}

	// Parse backend URL to get host:port
	// Expected format: grpc://host:port or just host:port
	target := backendURL
	target = strings.TrimPrefix(target, "grpc://")
	target = strings.TrimPrefix(target, "http://")
	target = strings.TrimPrefix(target, "https://")

	// Build dial options
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithDefaultCallOptions(grpc.ForceCodec(dynamicCodec{})))

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

	// Store in pool (race-safe: if another goroutine stored first, close ours)
	actual, loaded := t.connPool.LoadOrStore(backendURL, conn)
	if loaded {
		conn.Close()
		return actual.(*grpc.ClientConn), nil
	}

	return conn, nil
}

func (t *Translator) buildTLSCredentials(cfg config.ProtocolTLSConfig) (credentials.TransportCredentials, error) {
	tlsConfig := &tls.Config{}

	// Load CA certificate
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

	// Load client certificate if provided (mTLS)
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
	httpStatus := GRPCStatusToHTTP(code)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Grpc-Status", fmt.Sprintf("%d", code))
	w.Header().Set("Grpc-Message", message)
	w.WriteHeader(httpStatus)
	fmt.Fprintf(w, `{"error":{"code":%d,"message":%q}}`, code, message)
}

// Close releases resources for the specified route.
func (t *Translator) Close(routeID string) error {
	t.metricsMu.Lock()
	delete(t.routeMetrics, routeID)
	delete(t.routeMappers, routeID)
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
