package grpc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	rpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/proto"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/logging"
)

// ReflectionProxy aggregates gRPC reflection from multiple backends on a route.
// It proxies ListServices by merging results from all backends, and routes
// FileContainingSymbol to the backend that owns the symbol.
type ReflectionProxy struct {
	routeID  string
	backends []string
	cacheTTL time.Duration

	mu           sync.RWMutex
	serviceOwner map[string]string // service name → backend URL
	allServices  []string          // cached
	cacheExpiry  time.Time

	requests atomic.Int64
	errors   atomic.Int64
}

// NewReflectionProxy creates a new gRPC reflection aggregation proxy.
func NewReflectionProxy(routeID string, backends []string, cfg config.GRPCReflectionConfig) *ReflectionProxy {
	ttl := cfg.CacheTTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	return &ReflectionProxy{
		routeID:      routeID,
		backends:     backends,
		cacheTTL:     ttl,
		serviceOwner: make(map[string]string),
	}
}

// IsReflectionRequest checks if the request targets the gRPC reflection service.
func IsReflectionRequest(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/grpc.reflection.v1alpha.ServerReflection/") ||
		strings.HasPrefix(p, "/grpc.reflection.v1.ServerReflection/")
}

// ServeHTTP handles gRPC reflection requests by aggregating from backends.
func (rp *ReflectionProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp.requests.Add(1)

	// Read the gRPC length-prefixed message from the body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		rp.errors.Add(1)
		writeGRPCError(w, "failed to read request body")
		return
	}

	// Strip gRPC length-prefix (5 bytes: 1 compression flag + 4 length)
	payload := body
	if len(body) >= 5 {
		payload = body[5:]
	}

	req := &rpb.ServerReflectionRequest{}
	if err := proto.Unmarshal(payload, req); err != nil {
		rp.errors.Add(1)
		writeGRPCError(w, "failed to unmarshal reflection request")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var resp *rpb.ServerReflectionResponse

	switch mr := req.MessageRequest.(type) {
	case *rpb.ServerReflectionRequest_ListServices:
		resp, err = rp.handleListServices(ctx)
	case *rpb.ServerReflectionRequest_FileContainingSymbol:
		resp, err = rp.handleFileContainingSymbol(ctx, mr.FileContainingSymbol)
	case *rpb.ServerReflectionRequest_FileByFilename:
		resp, err = rp.handleFileByFilename(ctx, mr.FileByFilename)
	default:
		err = fmt.Errorf("unsupported reflection request type")
	}

	if err != nil {
		rp.errors.Add(1)
		writeGRPCError(w, err.Error())
		return
	}

	out, err := proto.Marshal(resp)
	if err != nil {
		rp.errors.Add(1)
		writeGRPCError(w, "failed to marshal response")
		return
	}

	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Grpc-Status", "0")
	// gRPC length-prefixed message: 1 byte compression flag + 4 bytes length + payload
	msg := make([]byte, 5+len(out))
	msg[0] = 0 // no compression
	msg[1] = byte(len(out) >> 24)
	msg[2] = byte(len(out) >> 16)
	msg[3] = byte(len(out) >> 8)
	msg[4] = byte(len(out))
	copy(msg[5:], out)
	w.WriteHeader(http.StatusOK)
	w.Write(msg)
}

// handleListServices aggregates service lists from all backends.
func (rp *ReflectionProxy) handleListServices(ctx context.Context) (*rpb.ServerReflectionResponse, error) {
	if err := rp.refreshCache(ctx); err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}

	rp.mu.RLock()
	services := make([]*rpb.ServiceResponse, 0, len(rp.allServices))
	for _, svc := range rp.allServices {
		services = append(services, &rpb.ServiceResponse{Name: svc})
	}
	rp.mu.RUnlock()

	return &rpb.ServerReflectionResponse{
		MessageResponse: &rpb.ServerReflectionResponse_ListServicesResponse{
			ListServicesResponse: &rpb.ListServiceResponse{
				Service: services,
			},
		},
	}, nil
}

// handleFileContainingSymbol routes the request to the backend that owns the symbol.
func (rp *ReflectionProxy) handleFileContainingSymbol(ctx context.Context, symbol string) (*rpb.ServerReflectionResponse, error) {
	if err := rp.refreshCache(ctx); err != nil {
		return nil, fmt.Errorf("file containing symbol: %w", err)
	}

	rp.mu.RLock()
	backendURL := ""
	if url, ok := rp.serviceOwner[symbol]; ok {
		backendURL = url
	} else {
		// Try prefix match for nested types (e.g., "pkg.Service.Method" → "pkg.Service")
		for svc, url := range rp.serviceOwner {
			if strings.HasPrefix(symbol, svc+".") {
				backendURL = url
				break
			}
		}
	}
	rp.mu.RUnlock()

	if backendURL == "" {
		return nil, fmt.Errorf("symbol %q not found on any backend", symbol)
	}

	return rp.fetchFileContainingSymbol(ctx, backendURL, symbol)
}

// handleFileByFilename routes file requests to all backends and returns the first match.
func (rp *ReflectionProxy) handleFileByFilename(ctx context.Context, filename string) (*rpb.ServerReflectionResponse, error) {
	for _, backend := range rp.backends {
		resp, err := rp.fetchFileByFilename(ctx, backend, filename)
		if err == nil {
			return resp, nil
		}
	}
	return nil, fmt.Errorf("file %q not found on any backend", filename)
}

// refreshCache refreshes the service→backend mapping if expired.
func (rp *ReflectionProxy) refreshCache(ctx context.Context) error {
	rp.mu.RLock()
	if time.Now().Before(rp.cacheExpiry) {
		rp.mu.RUnlock()
		return nil
	}
	rp.mu.RUnlock()

	rp.mu.Lock()
	defer rp.mu.Unlock()

	// Double-check after acquiring write lock
	if time.Now().Before(rp.cacheExpiry) {
		return nil
	}

	serviceOwner := make(map[string]string)
	var allServices []string

	for _, backend := range rp.backends {
		services, err := rp.listBackendServices(ctx, backend)
		if err != nil {
			logging.Error("gRPC reflection: failed to list services from backend",
				zap.String("route", rp.routeID),
				zap.String("backend", backend),
				zap.Error(err),
			)
			continue
		}
		for _, svc := range services {
			if _, exists := serviceOwner[svc]; !exists {
				serviceOwner[svc] = backend
				allServices = append(allServices, svc)
			}
		}
	}

	if len(allServices) == 0 {
		return fmt.Errorf("no services discovered from any backend")
	}

	rp.serviceOwner = serviceOwner
	rp.allServices = allServices
	rp.cacheExpiry = time.Now().Add(rp.cacheTTL)

	return nil
}

// listBackendServices queries a single backend for its service list via gRPC reflection.
func (rp *ReflectionProxy) listBackendServices(ctx context.Context, backendURL string) ([]string, error) {
	addr := stripScheme(backendURL)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	client := rpb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("reflection stream: %w", err)
	}
	defer stream.CloseSend()

	if err := stream.Send(&rpb.ServerReflectionRequest{
		MessageRequest: &rpb.ServerReflectionRequest_ListServices{},
	}); err != nil {
		return nil, fmt.Errorf("send list services: %w", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("recv list services: %w", err)
	}

	listResp, ok := resp.MessageResponse.(*rpb.ServerReflectionResponse_ListServicesResponse)
	if !ok {
		return nil, fmt.Errorf("unexpected response type")
	}

	var services []string
	for _, svc := range listResp.ListServicesResponse.Service {
		if svc.Name == "grpc.reflection.v1alpha.ServerReflection" ||
			svc.Name == "grpc.reflection.v1.ServerReflection" {
			continue
		}
		services = append(services, svc.Name)
	}
	return services, nil
}

// fetchFileContainingSymbol queries a specific backend for the file containing a symbol.
func (rp *ReflectionProxy) fetchFileContainingSymbol(ctx context.Context, backendURL, symbol string) (*rpb.ServerReflectionResponse, error) {
	addr := stripScheme(backendURL)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	client := rpb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("reflection stream: %w", err)
	}
	defer stream.CloseSend()

	if err := stream.Send(&rpb.ServerReflectionRequest{
		MessageRequest: &rpb.ServerReflectionRequest_FileContainingSymbol{
			FileContainingSymbol: symbol,
		},
	}); err != nil {
		return nil, fmt.Errorf("send file containing symbol: %w", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("recv file containing symbol: %w", err)
	}

	return resp, nil
}

// fetchFileByFilename queries a specific backend for a file by name.
func (rp *ReflectionProxy) fetchFileByFilename(ctx context.Context, backendURL, filename string) (*rpb.ServerReflectionResponse, error) {
	addr := stripScheme(backendURL)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	client := rpb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("reflection stream: %w", err)
	}
	defer stream.CloseSend()

	if err := stream.Send(&rpb.ServerReflectionRequest{
		MessageRequest: &rpb.ServerReflectionRequest_FileByFilename{
			FileByFilename: filename,
		},
	}); err != nil {
		return nil, fmt.Errorf("send file by filename: %w", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("recv file by filename: %w", err)
	}

	return resp, nil
}

// Middleware returns a middleware that intercepts gRPC reflection requests.
func (rp *ReflectionProxy) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsReflectionRequest(r) {
				rp.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns reflection proxy statistics.
func (rp *ReflectionProxy) Stats() map[string]interface{} {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	return map[string]interface{}{
		"backends":  len(rp.backends),
		"services":  len(rp.allServices),
		"cache_ttl": rp.cacheTTL.String(),
		"requests":  rp.requests.Load(),
		"errors":    rp.errors.Load(),
	}
}

// stripScheme removes http:// or https:// from a URL for gRPC dialing.
func stripScheme(url string) string {
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "https://")
	return url
}

// writeGRPCError writes a gRPC error response.
func writeGRPCError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Grpc-Status", "2") // UNKNOWN
	w.Header().Set("Grpc-Message", msg)
	w.WriteHeader(http.StatusOK)
}
