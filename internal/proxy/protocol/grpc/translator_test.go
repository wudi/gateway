package grpc

import (
	"testing"
)

func TestResolveMethod(t *testing.T) {
	translator := New()

	tests := []struct {
		name            string
		path            string
		serviceOverride string
		wantService     string
		wantMethod      string
		wantErr         bool
	}{
		{
			name:        "default mode - full path",
			path:        "/mypackage.UserService/GetUser",
			wantService: "mypackage.UserService",
			wantMethod:  "GetUser",
		},
		{
			name:        "default mode - with leading slash",
			path:        "/pkg.v1.OrderService/CreateOrder",
			wantService: "pkg.v1.OrderService",
			wantMethod:  "CreateOrder",
		},
		{
			name:            "service-scoped mode",
			path:            "/GetUser",
			serviceOverride: "mypackage.UserService",
			wantService:     "mypackage.UserService",
			wantMethod:      "GetUser",
		},
		{
			name:            "service-scoped mode with prefix path",
			path:            "/api/users/GetUser",
			serviceOverride: "mypackage.UserService",
			wantService:     "mypackage.UserService",
			wantMethod:      "GetUser",
		},
		{
			name:    "empty path",
			path:    "/",
			wantErr: true,
		},
		{
			name:    "no method in default mode",
			path:    "/mypackage.UserService/",
			wantErr: true,
		},
		{
			name:    "invalid format - no slash",
			path:    "/mypackage.UserService",
			wantErr: true,
		},
		{
			name:            "service-scoped mode - empty method",
			path:            "/",
			serviceOverride: "mypackage.UserService",
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, method, err := translator.resolveMethod(tt.path, tt.serviceOverride)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got service=%q, method=%q", service, method)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if service != tt.wantService {
				t.Errorf("service = %q, want %q", service, tt.wantService)
			}
			if method != tt.wantMethod {
				t.Errorf("method = %q, want %q", method, tt.wantMethod)
			}
		})
	}
}

func TestTranslatorName(t *testing.T) {
	translator := New()
	if translator.Name() != "http_to_grpc" {
		t.Errorf("Name() = %q, want %q", translator.Name(), "http_to_grpc")
	}
}

func TestTranslatorMetrics(t *testing.T) {
	translator := New()

	// Before any requests, metrics should be nil for nonexistent route
	metrics := translator.Metrics("nonexistent-route")
	if metrics != nil {
		t.Error("expected nil metrics for nonexistent route")
	}
}
