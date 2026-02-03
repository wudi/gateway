package health

import (
	"net"
	"testing"
	"time"
)

func TestTCPCheckerAddBackend(t *testing.T) {
	checker := NewTCPChecker(TCPCheckerConfig{
		DefaultTimeout:  1 * time.Second,
		DefaultInterval: 10 * time.Second,
	})
	defer checker.Stop()

	// Create a test server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}
	defer ln.Close()

	// Accept connections in background
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Add backend
	checker.AddBackend(TCPBackend{
		Address:        ln.Addr().String(),
		Timeout:        1 * time.Second,
		Interval:       100 * time.Millisecond,
		HealthyAfter:   1,
		UnhealthyAfter: 2,
	})

	// Wait for initial check
	time.Sleep(200 * time.Millisecond)

	// Check status
	status := checker.GetStatus(ln.Addr().String())
	if status != StatusHealthy {
		t.Errorf("Expected healthy status, got %s", status)
	}
}

func TestTCPCheckerUnhealthyBackend(t *testing.T) {
	checker := NewTCPChecker(TCPCheckerConfig{
		DefaultTimeout:  100 * time.Millisecond,
		DefaultInterval: 50 * time.Millisecond,
	})
	defer checker.Stop()

	// Add backend for non-listening port
	checker.AddBackend(TCPBackend{
		Address:        "127.0.0.1:59999", // unlikely to be listening
		Timeout:        100 * time.Millisecond,
		Interval:       50 * time.Millisecond,
		HealthyAfter:   1,
		UnhealthyAfter: 2,
	})

	// Wait for checks to run
	time.Sleep(300 * time.Millisecond)

	// Check status
	status := checker.GetStatus("127.0.0.1:59999")
	if status != StatusUnhealthy {
		t.Errorf("Expected unhealthy status, got %s", status)
	}
}

func TestTCPCheckerRemoveBackend(t *testing.T) {
	checker := NewTCPChecker(TCPCheckerConfig{})
	defer checker.Stop()

	checker.AddBackend(TCPBackend{
		Address: "127.0.0.1:12345",
	})

	// Remove backend
	checker.RemoveBackend("127.0.0.1:12345")

	// Status should be unknown
	status := checker.GetStatus("127.0.0.1:12345")
	if status != StatusUnknown {
		t.Errorf("Expected unknown status after removal, got %s", status)
	}
}

func TestTCPCheckerGetAllStatus(t *testing.T) {
	checker := NewTCPChecker(TCPCheckerConfig{})
	defer checker.Stop()

	checker.AddBackend(TCPBackend{Address: "127.0.0.1:12345"})
	checker.AddBackend(TCPBackend{Address: "127.0.0.1:12346"})

	statuses := checker.GetAllStatus()
	if len(statuses) != 2 {
		t.Errorf("Expected 2 statuses, got %d", len(statuses))
	}
}

func TestTCPCheckerIsHealthy(t *testing.T) {
	checker := NewTCPChecker(TCPCheckerConfig{})
	defer checker.Stop()

	// Non-existent backend should not be healthy
	if checker.IsHealthy("nonexistent:12345") {
		t.Error("Non-existent backend should not be healthy")
	}
}

func TestTCPCheckerHealthyBackends(t *testing.T) {
	checker := NewTCPChecker(TCPCheckerConfig{})
	defer checker.Stop()

	// Create a test server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}
	defer ln.Close()

	// Accept connections in background
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	checker.AddBackend(TCPBackend{
		Address:        ln.Addr().String(),
		Timeout:        1 * time.Second,
		Interval:       100 * time.Millisecond,
		HealthyAfter:   1,
		UnhealthyAfter: 2,
	})

	// Wait for check
	time.Sleep(200 * time.Millisecond)

	healthy := checker.HealthyBackends()
	if len(healthy) != 1 {
		t.Errorf("Expected 1 healthy backend, got %d", len(healthy))
	}
}

func TestCheckTCPConnection(t *testing.T) {
	// Create a test server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}
	defer ln.Close()

	// Accept connections in background
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Check healthy connection
	err = CheckTCPConnection(ln.Addr().String(), 1*time.Second)
	if err != nil {
		t.Errorf("CheckTCPConnection should succeed for listening port: %v", err)
	}

	// Check unhealthy connection
	err = CheckTCPConnection("127.0.0.1:59999", 100*time.Millisecond)
	if err == nil {
		t.Error("CheckTCPConnection should fail for non-listening port")
	}
}

func TestTCPCheckerOnChange(t *testing.T) {
	var changeAddr string
	var changeStatus Status

	checker := NewTCPChecker(TCPCheckerConfig{
		DefaultTimeout:  100 * time.Millisecond,
		DefaultInterval: 50 * time.Millisecond,
		OnChange: func(addr string, status Status) {
			changeAddr = addr
			changeStatus = status
		},
	})
	_ = changeStatus // suppress unused variable warning
	defer checker.Stop()

	// Create and close a test server to trigger status change
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	addr := ln.Addr().String()

	// Accept connections in background
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	checker.AddBackend(TCPBackend{
		Address:        addr,
		Timeout:        100 * time.Millisecond,
		Interval:       50 * time.Millisecond,
		HealthyAfter:   1,
		UnhealthyAfter: 1,
	})

	// Wait for healthy status
	time.Sleep(200 * time.Millisecond)

	// Close server to trigger unhealthy
	ln.Close()

	// Wait for unhealthy status
	time.Sleep(200 * time.Millisecond)

	// OnChange should have been called
	if changeAddr == "" {
		t.Error("OnChange callback should have been called")
	}
}
