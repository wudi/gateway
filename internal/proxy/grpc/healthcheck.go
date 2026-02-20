package grpc

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// HealthChecker performs gRPC health checks using grpc.health.v1.
type HealthChecker struct {
	service string
}

// NewHealthChecker creates a gRPC health checker for the given service name.
// Empty service name checks the overall server health.
func NewHealthChecker(service string) *HealthChecker {
	return &HealthChecker{service: service}
}

// Check performs a gRPC health check against the given address.
// Returns nil if the service is SERVING, an error otherwise.
func (hc *HealthChecker) Check(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("grpc health check: dial %s: %w", address, err)
	}
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)
	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{
		Service: hc.service,
	})
	if err != nil {
		return fmt.Errorf("grpc health check: %w", err)
	}

	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("grpc health check: service %q status %s", hc.service, resp.GetStatus().String())
	}

	return nil
}
