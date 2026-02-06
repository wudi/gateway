package config

import (
	"os"
	"testing"
	"time"
)

func TestLoaderParse(t *testing.T) {
	yaml := `
listeners:
  - id: "http-main"
    address: ":9090"
    protocol: "http"
    http:
      read_timeout: 10s
      write_timeout: 20s

registry:
  type: consul
  consul:
    address: "localhost:8500"

routes:
  - id: test-route
    path: /api/test
    path_prefix: true
    backends:
      - url: http://localhost:8080
`

	loader := NewLoader()
	cfg, err := loader.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.Listeners) == 0 {
		t.Fatal("expected at least one listener")
	}
	if cfg.Listeners[0].Address != ":9090" {
		t.Errorf("expected address :9090, got %s", cfg.Listeners[0].Address)
	}

	if cfg.Listeners[0].HTTP.ReadTimeout != 10*time.Second {
		t.Errorf("expected read_timeout 10s, got %v", cfg.Listeners[0].HTTP.ReadTimeout)
	}

	if cfg.Listeners[0].HTTP.WriteTimeout != 20*time.Second {
		t.Errorf("expected write_timeout 20s, got %v", cfg.Listeners[0].HTTP.WriteTimeout)
	}

	if cfg.Registry.Type != "consul" {
		t.Errorf("expected registry type consul, got %s", cfg.Registry.Type)
	}

	if len(cfg.Routes) != 1 {
		t.Errorf("expected 1 route, got %d", len(cfg.Routes))
	}

	if cfg.Routes[0].ID != "test-route" {
		t.Errorf("expected route id test-route, got %s", cfg.Routes[0].ID)
	}
}

func TestLoaderEnvExpansion(t *testing.T) {
	os.Setenv("TEST_PORT", "7777")
	os.Setenv("TEST_SECRET", "my-secret")
	defer os.Unsetenv("TEST_PORT")
	defer os.Unsetenv("TEST_SECRET")

	yaml := `
listeners:
  - id: "http-main"
    address: ":${TEST_PORT}"
    protocol: "http"

authentication:
  jwt:
    enabled: true
    secret: ${TEST_SECRET}

routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:8080
`

	loader := NewLoader()
	cfg, err := loader.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.Listeners[0].Address != ":7777" {
		t.Errorf("expected address :7777 from env, got %s", cfg.Listeners[0].Address)
	}

	if cfg.Authentication.JWT.Secret != "my-secret" {
		t.Errorf("expected secret 'my-secret' from env, got '%s'", cfg.Authentication.JWT.Secret)
	}
}

func TestLoaderValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid config",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: false,
		},
		{
			name: "no listeners",
			yaml: `
listeners: []
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "missing route id",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "duplicate route id",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
  - id: test
    path: /test2
    backends:
      - url: http://localhost:9001
`,
			wantErr: true,
		},
		{
			name: "missing route path",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "missing backends and service",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
`,
			wantErr: true,
		},
		{
			name: "valid with service instead of backends",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    service:
      name: my-service
`,
			wantErr: false,
		},
		{
			name: "invalid registry type",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
registry:
  type: invalid
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "jwt enabled without secret",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
authentication:
  jwt:
    enabled: true
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewLoader()
			_, err := loader.Parse([]byte(tt.yaml))
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if len(cfg.Listeners) == 0 {
		t.Fatal("expected at least one default listener")
	}
	if cfg.Listeners[0].ID != "default-http" {
		t.Errorf("expected default listener id default-http, got %s", cfg.Listeners[0].ID)
	}
	if cfg.Listeners[0].Address != ":8080" {
		t.Errorf("expected default address :8080, got %s", cfg.Listeners[0].Address)
	}
	if cfg.Listeners[0].HTTP.ReadTimeout != 30*time.Second {
		t.Errorf("expected default read_timeout 30s, got %v", cfg.Listeners[0].HTTP.ReadTimeout)
	}

	if cfg.Registry.Type != "memory" {
		t.Errorf("expected default registry type memory, got %s", cfg.Registry.Type)
	}

	if cfg.Authentication.APIKey.Header != "X-API-Key" {
		t.Errorf("expected default API key header X-API-Key, got %s", cfg.Authentication.APIKey.Header)
	}

	if cfg.Admin.Port != 8081 {
		t.Errorf("expected default admin port 8081, got %d", cfg.Admin.Port)
	}
}

func TestMerge(t *testing.T) {
	base := &Config{
		Listeners: []ListenerConfig{{
			ID: "default-http", Address: ":8080", Protocol: ProtocolHTTP,
		}},
		Registry: RegistryConfig{
			Type: "memory",
		},
	}

	overlay := &Config{
		Listeners: []ListenerConfig{{
			ID: "override-http", Address: ":9090", Protocol: ProtocolHTTP,
		}},
		Registry: RegistryConfig{
			Type: "consul", // Override
		},
		Routes: []RouteConfig{
			{ID: "new-route", Path: "/new"},
		},
	}

	result := Merge(base, overlay)

	if len(result.Listeners) != 1 || result.Listeners[0].Address != ":9090" {
		t.Errorf("expected merged listener address :9090, got %v", result.Listeners)
	}

	if result.Registry.Type != "consul" {
		t.Errorf("expected merged registry type consul, got %s", result.Registry.Type)
	}

	if len(result.Routes) != 1 {
		t.Errorf("expected 1 route, got %d", len(result.Routes))
	}
}

func TestLoadFromEnv(t *testing.T) {
	os.Setenv("REGISTRY_TYPE", "etcd")
	os.Setenv("JWT_SECRET", "env-secret")
	defer os.Unsetenv("REGISTRY_TYPE")
	defer os.Unsetenv("JWT_SECRET")

	loader := NewLoader()
	cfg, err := loader.LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv failed: %v", err)
	}

	if cfg.Registry.Type != "etcd" {
		t.Errorf("expected registry type etcd from env, got %s", cfg.Registry.Type)
	}

	if !cfg.Authentication.JWT.Enabled {
		t.Error("expected JWT to be enabled when secret is set")
	}

	if cfg.Authentication.JWT.Secret != "env-secret" {
		t.Errorf("expected JWT secret 'env-secret', got '%s'", cfg.Authentication.JWT.Secret)
	}
}

func TestLoaderValidateListeners(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid listener",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
`,
			wantErr: false,
		},
		{
			name: "missing listener id",
			yaml: `
listeners:
  - address: ":8080"
    protocol: "http"
`,
			wantErr: true,
			errMsg:  "listener 0: id is required",
		},
		{
			name: "duplicate listener id",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
  - id: "http-main"
    address: ":8081"
    protocol: "http"
`,
			wantErr: true,
			errMsg:  "duplicate listener id",
		},
		{
			name: "missing listener address",
			yaml: `
listeners:
  - id: "http-main"
    protocol: "http"
`,
			wantErr: true,
			errMsg:  "address is required",
		},
		{
			name: "missing listener protocol",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
`,
			wantErr: true,
			errMsg:  "protocol is required",
		},
		{
			name: "invalid protocol",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "invalid"
`,
			wantErr: true,
			errMsg:  "invalid protocol",
		},
		{
			name: "TLS enabled without cert",
			yaml: `
listeners:
  - id: "https-main"
    address: ":8443"
    protocol: "http"
    tls:
      enabled: true
      key_file: "/path/to/key"
`,
			wantErr: true,
			errMsg:  "cert_file not provided",
		},
		{
			name: "TLS enabled without key",
			yaml: `
listeners:
  - id: "https-main"
    address: ":8443"
    protocol: "http"
    tls:
      enabled: true
      cert_file: "/path/to/cert"
`,
			wantErr: true,
			errMsg:  "key_file not provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewLoader()
			_, err := loader.Parse([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoaderValidateTCPRoutes(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid TCP route",
			yaml: `
listeners:
  - id: "tcp-main"
    address: ":3306"
    protocol: "tcp"
tcp_routes:
  - id: "mysql"
    listener: "tcp-main"
    backends:
      - url: "tcp://mysql:3306"
`,
			wantErr: false,
		},
		{
			name: "missing TCP route id",
			yaml: `
listeners:
  - id: "tcp-main"
    address: ":3306"
    protocol: "tcp"
tcp_routes:
  - listener: "tcp-main"
    backends:
      - url: "tcp://mysql:3306"
`,
			wantErr: true,
		},
		{
			name: "TCP route references unknown listener",
			yaml: `
listeners:
  - id: "tcp-main"
    address: ":3306"
    protocol: "tcp"
tcp_routes:
  - id: "mysql"
    listener: "unknown-listener"
    backends:
      - url: "tcp://mysql:3306"
`,
			wantErr: true,
		},
		{
			name: "TCP route without backends",
			yaml: `
listeners:
  - id: "tcp-main"
    address: ":3306"
    protocol: "tcp"
tcp_routes:
  - id: "mysql"
    listener: "tcp-main"
`,
			wantErr: true,
		},
		{
			name: "TCP route with invalid CIDR",
			yaml: `
listeners:
  - id: "tcp-main"
    address: ":3306"
    protocol: "tcp"
tcp_routes:
  - id: "mysql"
    listener: "tcp-main"
    match:
      source_cidr:
        - "invalid-cidr"
    backends:
      - url: "tcp://mysql:3306"
`,
			wantErr: true,
		},
		{
			name: "TCP route with valid CIDR",
			yaml: `
listeners:
  - id: "tcp-main"
    address: ":3306"
    protocol: "tcp"
tcp_routes:
  - id: "mysql"
    listener: "tcp-main"
    match:
      source_cidr:
        - "10.0.0.0/8"
        - "192.168.0.0/16"
    backends:
      - url: "tcp://mysql:3306"
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewLoader()
			_, err := loader.Parse([]byte(tt.yaml))
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoaderValidateUDPRoutes(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid UDP route",
			yaml: `
listeners:
  - id: "udp-dns"
    address: ":5353"
    protocol: "udp"
udp_routes:
  - id: "dns"
    listener: "udp-dns"
    backends:
      - url: "udp://8.8.8.8:53"
`,
			wantErr: false,
		},
		{
			name: "missing UDP route id",
			yaml: `
listeners:
  - id: "udp-dns"
    address: ":5353"
    protocol: "udp"
udp_routes:
  - listener: "udp-dns"
    backends:
      - url: "udp://8.8.8.8:53"
`,
			wantErr: true,
		},
		{
			name: "UDP route references unknown listener",
			yaml: `
listeners:
  - id: "udp-dns"
    address: ":5353"
    protocol: "udp"
udp_routes:
  - id: "dns"
    listener: "unknown"
    backends:
      - url: "udp://8.8.8.8:53"
`,
			wantErr: true,
		},
		{
			name: "UDP route without backends",
			yaml: `
listeners:
  - id: "udp-dns"
    address: ":5353"
    protocol: "udp"
udp_routes:
  - id: "dns"
    listener: "udp-dns"
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewLoader()
			_, err := loader.Parse([]byte(tt.yaml))
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoaderValidateProtocolTranslation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid http_to_grpc protocol",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/grpc
    path_prefix: true
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        service: myapp.UserService
        timeout: 10s
`,
			wantErr: false,
		},
		{
			name: "invalid protocol type",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/grpc
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: invalid_protocol
`,
			wantErr: true,
		},
		{
			name: "protocol and grpc.enabled conflict",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/grpc
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
    grpc:
      enabled: true
`,
			wantErr: true,
		},
		{
			name: "protocol TLS enabled without CA file",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/grpc
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        tls:
          enabled: true
`,
			wantErr: true,
		},
		{
			name: "protocol TLS with CA file",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/grpc
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        tls:
          enabled: true
          ca_file: /path/to/ca.crt
`,
			wantErr: false,
		},
		{
			name: "valid REST-to-gRPC mappings",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/users
    path_prefix: true
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        service: myapp.UserService
        mappings:
          - http_method: GET
            http_path: /api/users/:user_id
            grpc_method: GetUser
          - http_method: POST
            http_path: /api/users
            grpc_method: CreateUser
            body: "*"
`,
			wantErr: false,
		},
		{
			name: "mappings without service",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/users
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        mappings:
          - http_method: GET
            http_path: /api/users/:user_id
            grpc_method: GetUser
`,
			wantErr: true,
		},
		{
			name: "mapping with missing http_method",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/users
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        service: myapp.UserService
        mappings:
          - http_path: /api/users/:user_id
            grpc_method: GetUser
`,
			wantErr: true,
		},
		{
			name: "mapping with invalid http_method",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/users
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        service: myapp.UserService
        mappings:
          - http_method: INVALID
            http_path: /api/users/:user_id
            grpc_method: GetUser
`,
			wantErr: true,
		},
		{
			name: "mapping with missing grpc_method",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/users
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        service: myapp.UserService
        mappings:
          - http_method: GET
            http_path: /api/users/:user_id
`,
			wantErr: true,
		},
		{
			name: "duplicate mapping",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: grpc-route
    path: /api/users
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        service: myapp.UserService
        mappings:
          - http_method: GET
            http_path: /api/users/:user_id
            grpc_method: GetUser
          - http_method: GET
            http_path: /api/users/:user_id
            grpc_method: GetUserV2
`,
			wantErr: true,
		},
		{
			name: "valid fixed method",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: demo-route
    path: /demo
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        service: myapp.UserService
        method: GetUser
        timeout: 10s
`,
			wantErr: false,
		},
		{
			name: "fixed method without service",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: demo-route
    path: /demo
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        method: GetUser
`,
			wantErr: true,
		},
		{
			name: "fixed method with mappings conflict",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: demo-route
    path: /demo
    backends:
      - url: grpc://localhost:50051
    protocol:
      type: http_to_grpc
      grpc:
        service: myapp.UserService
        method: GetUser
        mappings:
          - http_method: GET
            http_path: /demo
            grpc_method: ListUsers
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewLoader()
			_, err := loader.Parse([]byte(tt.yaml))
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
