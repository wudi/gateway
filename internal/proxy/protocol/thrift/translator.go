package thrift

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

	athrift "github.com/apache/thrift/lib/go/thrift"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/internal/proxy/protocol"
)

// connEntry holds a pooled Thrift connection.
type connEntry struct {
	transport athrift.TTransport
	iprot     athrift.TProtocol
	oprot     athrift.TProtocol
	mu        sync.Mutex
}

// Translator implements HTTP-to-Thrift protocol translation.
type Translator struct {
	routeMetrics map[string]*protocol.RouteMetrics
	routeMappers map[string]*restMapper
	routeSchemas map[string]*serviceSchema
	metricsMu    sync.RWMutex

	connPool    sync.Map // backend URL → *connEntry
	schemaCache *schemaCache
	invoker     *invoker
}

// New creates a new Thrift translator.
func New() *Translator {
	return &Translator{
		routeMetrics: make(map[string]*protocol.RouteMetrics),
		routeMappers: make(map[string]*restMapper),
		routeSchemas: make(map[string]*serviceSchema),
		schemaCache:  newSchemaCache(),
		invoker:      newInvoker(),
	}
}

// Name returns the protocol type identifier.
func (t *Translator) Name() string {
	return "http_to_thrift"
}

// Handler returns an http.Handler that translates HTTP/JSON to Thrift.
func (t *Translator) Handler(routeID string, balancer loadbalancer.Balancer, cfg config.ProtocolConfig) (http.Handler, error) {
	// Load schema from IDL file or inline config.
	var schema *serviceSchema
	var err error
	if cfg.Thrift.IDLFile != "" {
		schema, err = t.schemaCache.getServiceSchema(cfg.Thrift.IDLFile, cfg.Thrift.Service)
	} else {
		schema, err = buildServiceSchemaFromConfig(cfg.Thrift)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load thrift schema: %w", err)
	}

	// Initialize route metrics.
	t.metricsMu.Lock()
	t.routeMetrics[routeID] = &protocol.RouteMetrics{}
	t.routeSchemas[routeID] = schema
	t.metricsMu.Unlock()

	// Create REST mapper if mappings are configured.
	if len(cfg.Thrift.Mappings) > 0 {
		mapper, err := newRESTMapper(cfg.Thrift.Service, cfg.Thrift.Mappings)
		if err != nil {
			return nil, fmt.Errorf("failed to create REST mapper: %w", err)
		}
		t.metricsMu.Lock()
		t.routeMappers[routeID] = mapper
		t.metricsMu.Unlock()
	}

	timeout := cfg.Thrift.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.serveHTTP(w, r, routeID, balancer, cfg, schema, timeout)
	}), nil
}

func (t *Translator) serveHTTP(w http.ResponseWriter, r *http.Request, routeID string, balancer loadbalancer.Balancer, cfg config.ProtocolConfig, schema *serviceSchema, timeout time.Duration) {
	start := time.Now()

	t.metricsMu.RLock()
	metrics := t.routeMetrics[routeID]
	mapper := t.routeMappers[routeID]
	t.metricsMu.RUnlock()

	metrics.Requests.Add(1)

	// Read request body.
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		t.writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to read request body: %v", err))
		metrics.Failures.Add(1)
		return
	}

	var methodName string
	var requestBody []byte

	// Priority order:
	// 1. Fixed method (method in config)
	// 2. REST mappings (if configured)
	// 3. Path-based resolution (last segment)
	if cfg.Thrift.Method != "" {
		methodName = cfg.Thrift.Method
		requestBody = rawBody
	} else if mapper != nil {
		match := mapper.match(r.Method, r.URL.Path)
		if match == nil {
			t.writeError(w, http.StatusNotFound, fmt.Sprintf("no mapping found for %s %s", r.Method, r.URL.Path))
			metrics.Failures.Add(1)
			return
		}
		methodName = match.thriftMethod
		requestBody, err = mapper.buildRequestBody(r, match, rawBody)
		if err != nil {
			t.writeError(w, http.StatusBadRequest, err.Error())
			metrics.Failures.Add(1)
			return
		}
	} else {
		methodName = resolveMethodFromPath(r.URL.Path)
		if methodName == "" {
			t.writeError(w, http.StatusBadRequest, "method name is required in path")
			metrics.Failures.Add(1)
			return
		}
		requestBody = rawBody
	}

	// Select backend.
	backend := balancer.Next()
	if backend == nil {
		t.writeError(w, http.StatusServiceUnavailable, "no healthy backend available")
		metrics.Failures.Add(1)
		return
	}

	// Get or create connection.
	conn, err := t.getConnection(backend.URL, cfg.Thrift)
	if err != nil {
		t.writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to connect to backend: %v", err))
		metrics.Failures.Add(1)
		return
	}

	// Lock connection — Thrift TCP is not multiplexed at the transport level.
	conn.mu.Lock()
	defer conn.mu.Unlock()

	// Create context with timeout.
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Build codec from schema.
	c := &codec{
		structs:  schema.structs,
		enums:    schema.enums,
		typedefs: schema.typedefs,
	}

	// Invoke the method.
	respJSON, err := t.invoker.invokeMethod(ctx, conn.iprot, conn.oprot, c, schema, methodName, requestBody, cfg.Thrift.Multiplexed)
	if err != nil {
		httpStatus := ThriftExceptionToHTTP(err)
		t.writeError(w, httpStatus, err.Error())
		metrics.Failures.Add(1)
		return
	}

	// Write successful response.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respJSON)

	metrics.Successes.Add(1)
	metrics.TotalLatencyNs.Add(time.Since(start).Nanoseconds())
}

// resolveMethodFromPath extracts the method name from the URL path (last segment).
func resolveMethodFromPath(path string) string {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

// getConnection returns a pooled Thrift connection, creating one if needed.
func (t *Translator) getConnection(backendURL string, cfg config.ThriftTranslateConfig) (*connEntry, error) {
	if existing, ok := t.connPool.Load(backendURL); ok {
		entry := existing.(*connEntry)
		if entry.transport.IsOpen() {
			return entry, nil
		}
		// Connection closed — remove and recreate.
		t.connPool.Delete(backendURL)
	}

	// Parse backend URL to get host:port.
	target := backendURL
	target = strings.TrimPrefix(target, "thrift://")
	target = strings.TrimPrefix(target, "http://")
	target = strings.TrimPrefix(target, "https://")
	// Strip trailing path if any.
	if idx := strings.Index(target, "/"); idx != -1 {
		target = target[:idx]
	}

	thriftCfg := &athrift.TConfiguration{
		ConnectTimeout: 10 * time.Second,
		SocketTimeout:  cfg.Timeout,
	}

	// Create socket (TLS or plain).
	var socket athrift.TTransport
	if cfg.TLS.Enabled {
		tlsConfig, err := buildTLSConfig(cfg.TLS)
		if err != nil {
			return nil, err
		}
		thriftCfg.TLSConfig = tlsConfig
		socket = athrift.NewTSSLSocketConf(target, thriftCfg)
	} else {
		socket = athrift.NewTSocketConf(target, thriftCfg)
	}

	// Wrap in transport (framed or buffered).
	var transport athrift.TTransport
	switch cfg.Transport {
	case "buffered":
		transport = athrift.NewTBufferedTransport(socket, 4096)
	default: // "framed" or empty (default)
		transport = athrift.NewTFramedTransportConf(socket, thriftCfg)
	}

	// Create protocol (binary or compact).
	var iprot, oprot athrift.TProtocol
	switch cfg.Protocol {
	case "compact":
		iprot = athrift.NewTCompactProtocolConf(transport, thriftCfg)
		oprot = athrift.NewTCompactProtocolConf(transport, thriftCfg)
	default: // "binary" or empty (default)
		iprot = athrift.NewTBinaryProtocolConf(transport, thriftCfg)
		oprot = athrift.NewTBinaryProtocolConf(transport, thriftCfg)
	}

	// Open the connection.
	if err := transport.Open(); err != nil {
		return nil, fmt.Errorf("failed to open thrift connection to %s: %w", target, err)
	}

	entry := &connEntry{
		transport: transport,
		iprot:     iprot,
		oprot:     oprot,
	}

	// Store in pool (race-safe).
	actual, loaded := t.connPool.LoadOrStore(backendURL, entry)
	if loaded {
		// Another goroutine stored first — close ours and use theirs.
		transport.Close()
		return actual.(*connEntry), nil
	}

	return entry, nil
}

// buildTLSConfig creates a TLS config from the protocol TLS settings.
func buildTLSConfig(cfg config.ProtocolTLSConfig) (*tls.Config, error) {
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

	return tlsConfig, nil
}

// writeError writes a JSON error response.
func (t *Translator) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"code":%d,"message":%q}}`, status, message)
}

// Close releases resources for the specified route.
func (t *Translator) Close(routeID string) error {
	t.metricsMu.Lock()
	delete(t.routeMetrics, routeID)
	delete(t.routeMappers, routeID)
	delete(t.routeSchemas, routeID)
	t.metricsMu.Unlock()
	return nil
}

// CloseAll closes all Thrift connections.
func (t *Translator) CloseAll() {
	t.connPool.Range(func(key, value interface{}) bool {
		if entry, ok := value.(*connEntry); ok {
			entry.transport.Close()
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
