package grpchealth

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func startTestServer(t *testing.T, getStatus func() bool) (string, func()) {
	t.Helper()
	srv := NewServer("127.0.0.1:0", getStatus)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	// Wait briefly for the server to start and obtain the listener address.
	time.Sleep(50 * time.Millisecond)

	srv.mu.Lock()
	addr := srv.listener.Addr().String()
	srv.mu.Unlock()

	return addr, func() {
		srv.Stop()
		// Drain the error channel.
		<-errCh
	}
}

func TestCheck_Serving(t *testing.T) {
	addr, stop := startTestServer(t, func() bool { return true })
	defer stop()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := grpc_health_v1.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("expected SERVING, got %s", resp.GetStatus())
	}
}

func TestCheck_NotServing(t *testing.T) {
	addr, stop := startTestServer(t, func() bool { return false })
	defer stop()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := grpc_health_v1.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Errorf("expected NOT_SERVING, got %s", resp.GetStatus())
	}
}
