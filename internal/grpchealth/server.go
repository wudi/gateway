package grpchealth

import (
	"context"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// Server implements a gRPC health check server (grpc.health.v1.Health).
type Server struct {
	grpc_health_v1.UnimplementedHealthServer

	grpcServer *grpc.Server
	address    string
	getStatus  func() bool

	mu       sync.Mutex
	listener net.Listener
}

// NewServer creates a new gRPC health check server.
// The getStatus function is called on each Check or Watch to determine
// whether the runway is healthy (SERVING) or not (NOT_SERVING).
func NewServer(address string, getStatus func() bool) *Server {
	s := &Server{
		grpcServer: grpc.NewServer(),
		address:    address,
		getStatus:  getStatus,
	}
	grpc_health_v1.RegisterHealthServer(s.grpcServer, s)
	return s
}

// Check implements grpc_health_v1.HealthServer.
func (s *Server) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	status := grpc_health_v1.HealthCheckResponse_NOT_SERVING
	if s.getStatus() {
		status = grpc_health_v1.HealthCheckResponse_SERVING
	}
	return &grpc_health_v1.HealthCheckResponse{Status: status}, nil
}

// Watch implements grpc_health_v1.HealthServer.
// It streams health status changes every 5 seconds.
func (s *Server) Watch(_ *grpc_health_v1.HealthCheckRequest, stream grpc.ServerStreamingServer[grpc_health_v1.HealthCheckResponse]) error {
	var lastStatus grpc_health_v1.HealthCheckResponse_ServingStatus = -1

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Send initial status immediately.
	current := s.currentStatus()
	if err := stream.Send(&grpc_health_v1.HealthCheckResponse{Status: current}); err != nil {
		return err
	}
	lastStatus = current

	for {
		select {
		case <-ticker.C:
			current = s.currentStatus()
			if current != lastStatus {
				if err := stream.Send(&grpc_health_v1.HealthCheckResponse{Status: current}); err != nil {
					return err
				}
				lastStatus = current
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *Server) currentStatus() grpc_health_v1.HealthCheckResponse_ServingStatus {
	if s.getStatus() {
		return grpc_health_v1.HealthCheckResponse_SERVING
	}
	return grpc_health_v1.HealthCheckResponse_NOT_SERVING
}

// Start begins listening on the configured address and serving gRPC requests.
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", s.address)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listener = lis
	s.mu.Unlock()
	return s.grpcServer.Serve(lis)
}

// Stop gracefully stops the gRPC server.
func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
}
