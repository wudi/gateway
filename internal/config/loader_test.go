package config

import (
	"fmt"
	"os"
	"strings"
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
		{
			name: "valid echo route",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: echo-test
    path: /echo
    echo: true
`,
			wantErr: false,
		},
		{
			name: "echo with backends",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: echo-test
    path: /echo
    echo: true
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "echo with websocket",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: echo-test
    path: /echo
    echo: true
    websocket:
      enabled: true
`,
			wantErr: true,
		},
		{
			name: "echo with circuit_breaker",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: echo-test
    path: /echo
    echo: true
    circuit_breaker:
      enabled: true
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

func TestLoaderValidateThriftInlineSchema(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid thrift with idl_file",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    path_prefix: true
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        idl_file: /etc/idl/service.thrift
        service: UserService
`,
			wantErr: false,
		},
		{
			name: "valid thrift with inline methods",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    path_prefix: true
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          GetUser:
            args:
              - id: 1
                name: user_id
                type: string
            result:
              - id: 0
                name: success
                type: struct
                struct: User
          CreateUser:
            args:
              - id: 1
                name: user
                type: struct
                struct: User
            void: true
        structs:
          User:
            - id: 1
              name: name
              type: string
            - id: 2
              name: age
              type: i32
`,
			wantErr: false,
		},
		{
			name: "idl_file and methods mutually exclusive",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        idl_file: /etc/idl/service.thrift
        service: UserService
        methods:
          GetUser:
            args:
              - id: 1
                name: id
                type: string
`,
			wantErr: true,
		},
		{
			name: "neither idl_file nor methods",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
`,
			wantErr: true,
		},
		{
			name: "invalid field type in methods",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          Test:
            args:
              - id: 1
                name: x
                type: invalid_type
`,
			wantErr: true,
		},
		{
			name: "missing field id",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          Test:
            args:
              - id: 0
                name: x
                type: string
`,
			wantErr: true,
		},
		{
			name: "missing field name",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          Test:
            args:
              - id: 1
                type: string
`,
			wantErr: true,
		},
		{
			name: "struct type without struct name",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          Test:
            args:
              - id: 1
                name: x
                type: struct
`,
			wantErr: true,
		},
		{
			name: "struct reference to unknown struct",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          Test:
            args:
              - id: 1
                name: x
                type: struct
                struct: NonExistent
`,
			wantErr: true,
		},
		{
			name: "list without elem",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          Test:
            args:
              - id: 1
                name: x
                type: list
`,
			wantErr: true,
		},
		{
			name: "map without key",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          Test:
            args:
              - id: 1
                name: x
                type: map
                value: string
`,
			wantErr: true,
		},
		{
			name: "empty enum values",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          Test:
            args:
              - id: 1
                name: x
                type: string
        enums:
          Status: {}
`,
			wantErr: true,
		},
		{
			name: "valid enum type in field",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          Test:
            args:
              - id: 1
                name: status
                type: Status
        enums:
          Status:
            ACTIVE: 1
            INACTIVE: 2
`,
			wantErr: false,
		},
		{
			name: "valid oneway method",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: thrift-route
    path: /thrift
    backends:
      - url: http://localhost:9090
    protocol:
      type: http_to_thrift
      thrift:
        service: UserService
        methods:
          Notify:
            args:
              - id: 1
                name: message
                type: string
            oneway: true
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

func TestLoaderValidateDNSResolver(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid dns_resolver",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
dns_resolver:
  nameservers:
    - "10.0.0.53:53"
    - "8.8.8.8:53"
  timeout: 5s
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: false,
		},
		{
			name: "dns_resolver nameserver missing port",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
dns_resolver:
  nameservers:
    - "10.0.0.53"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "dns_resolver negative timeout",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
dns_resolver:
  timeout: -1s
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "dns_resolver empty nameservers is valid (uses OS default)",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
dns_resolver:
  timeout: 3s
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: false,
		},
		{
			name: "dns_resolver multiple valid nameservers",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
dns_resolver:
  nameservers:
    - "10.0.0.53:53"
    - "[::1]:53"
  timeout: 2s
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
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

func TestLoaderValidateLoadBalancer(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid least_conn",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: least_conn
    backends:
      - url: http://localhost:9000
`,
			wantErr: false,
		},
		{
			name: "valid consistent_hash with header key",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: consistent_hash
    consistent_hash:
      key: header
      header_name: X-User-ID
    backends:
      - url: http://localhost:9000
`,
			wantErr: false,
		},
		{
			name: "valid consistent_hash with ip key",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: consistent_hash
    consistent_hash:
      key: ip
    backends:
      - url: http://localhost:9000
`,
			wantErr: false,
		},
		{
			name: "valid consistent_hash with path key",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: consistent_hash
    consistent_hash:
      key: path
    backends:
      - url: http://localhost:9000
`,
			wantErr: false,
		},
		{
			name: "valid least_response_time",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: least_response_time
    backends:
      - url: http://localhost:9000
`,
			wantErr: false,
		},
		{
			name: "invalid load_balancer value",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: random
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "consistent_hash missing key",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: consistent_hash
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "consistent_hash header key missing header_name",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: consistent_hash
    consistent_hash:
      key: header
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "consistent_hash cookie key missing header_name",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: consistent_hash
    consistent_hash:
      key: cookie
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
		},
		{
			name: "least_conn with traffic_split",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: least_conn
    traffic_split:
      - group: a
        weight: 50
        backends:
          - url: http://localhost:9000
      - group: b
        weight: 50
        backends:
          - url: http://localhost:9001
`,
			wantErr: true,
		},
		{
			name: "consistent_hash with traffic_split",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: consistent_hash
    consistent_hash:
      key: ip
    traffic_split:
      - group: a
        weight: 50
        backends:
          - url: http://localhost:9000
      - group: b
        weight: 50
        backends:
          - url: http://localhost:9001
`,
			wantErr: true,
		},
		{
			name: "least_response_time with traffic_split",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: least_response_time
    traffic_split:
      - group: a
        weight: 50
        backends:
          - url: http://localhost:9000
      - group: b
        weight: 50
        backends:
          - url: http://localhost:9001
`,
			wantErr: true,
		},
		{
			name: "round_robin explicit is valid",
			yaml: `
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    load_balancer: round_robin
    backends:
      - url: http://localhost:9000
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

func TestLoaderValidateCacheMode(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid local mode",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    cache:
      enabled: true
      mode: "local"
`,
			wantErr: false,
		},
		{
			name: "valid distributed mode with redis",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
redis:
  address: "localhost:6379"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    cache:
      enabled: true
      mode: "distributed"
`,
			wantErr: false,
		},
		{
			name: "invalid cache mode",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    cache:
      enabled: true
      mode: "invalid"
`,
			wantErr: true,
		},
		{
			name: "distributed mode without redis",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    cache:
      enabled: true
      mode: "distributed"
`,
			wantErr: true,
		},
		{
			name: "empty mode defaults to local",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    cache:
      enabled: true
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

func TestLoaderValidateTimeoutPolicy(t *testing.T) {
	base := func(tp string) string {
		return `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /api
    backends:
      - url: http://localhost:9000
    timeout_policy:
` + tp
	}

	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name:    "valid full config",
			yaml:    base("      request: 30s\n      backend: 5s\n      header_timeout: 3s\n      idle: 60s"),
			wantErr: false,
		},
		{
			name:    "empty config is valid",
			yaml:    base("      {}"),
			wantErr: false,
		},
		{
			name:    "backend > request",
			yaml:    base("      request: 5s\n      backend: 10s"),
			wantErr: true,
		},
		{
			name:    "header_timeout > backend",
			yaml:    base("      request: 30s\n      backend: 5s\n      header_timeout: 10s"),
			wantErr: true,
		},
		{
			name:    "header_timeout > request when no backend",
			yaml:    base("      request: 5s\n      header_timeout: 10s"),
			wantErr: true,
		},
		{
			name:    "header_timeout alone is valid",
			yaml:    base("      header_timeout: 3s"),
			wantErr: false,
		},
		{
			name:    "backend alone is valid",
			yaml:    base("      backend: 5s"),
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

func TestLoaderValidateWebhooks(t *testing.T) {
	base := `
listeners:
  - id: default
    address: ":8080"
    protocol: http
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:8080
`
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid config",
			yaml: base + `
webhooks:
  enabled: true
  timeout: 5s
  workers: 4
  queue_size: 1000
  retry:
    max_retries: 3
    backoff: 1s
    max_backoff: 30s
  endpoints:
    - id: alerts
      url: https://hooks.example.com/gateway
      secret: whsec_abc123
      events:
        - "backend.unhealthy"
        - "circuit_breaker.state_change"
`,
			wantErr: false,
		},
		{
			name: "valid wildcard events",
			yaml: base + `
webhooks:
  enabled: true
  endpoints:
    - id: all
      url: https://hooks.example.com/all
      events:
        - "*"
`,
			wantErr: false,
		},
		{
			name: "valid prefix wildcard",
			yaml: base + `
webhooks:
  enabled: true
  endpoints:
    - id: canary-watcher
      url: https://hooks.example.com/canary
      events:
        - "canary.*"
        - "config.*"
`,
			wantErr: false,
		},
		{
			name: "missing endpoints",
			yaml: base + `
webhooks:
  enabled: true
`,
			wantErr: true,
		},
		{
			name: "missing endpoint id",
			yaml: base + `
webhooks:
  enabled: true
  endpoints:
    - url: https://hooks.example.com
      events:
        - "*"
`,
			wantErr: true,
		},
		{
			name: "duplicate endpoint id",
			yaml: base + `
webhooks:
  enabled: true
  endpoints:
    - id: dup
      url: https://hooks.example.com/1
      events:
        - "*"
    - id: dup
      url: https://hooks.example.com/2
      events:
        - "*"
`,
			wantErr: true,
		},
		{
			name: "missing url",
			yaml: base + `
webhooks:
  enabled: true
  endpoints:
    - id: test
      events:
        - "*"
`,
			wantErr: true,
		},
		{
			name: "invalid url scheme",
			yaml: base + `
webhooks:
  enabled: true
  endpoints:
    - id: test
      url: ftp://example.com
      events:
        - "*"
`,
			wantErr: true,
		},
		{
			name: "missing events",
			yaml: base + `
webhooks:
  enabled: true
  endpoints:
    - id: test
      url: https://example.com
`,
			wantErr: true,
		},
		{
			name: "invalid event prefix",
			yaml: base + `
webhooks:
  enabled: true
  endpoints:
    - id: test
      url: https://example.com
      events:
        - "invalid.event"
`,
			wantErr: true,
		},
		{
			name: "negative timeout",
			yaml: base + `
webhooks:
  enabled: true
  timeout: -1s
  endpoints:
    - id: test
      url: https://example.com
      events:
        - "*"
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

func TestLoaderValidateHealthCheck(t *testing.T) {
	base := `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: "test"
    path: "/test"
    backends:
      - url: "http://localhost:9000"
`

	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid global health check",
			yaml: base + `
health_check:
  path: "/status"
  method: "HEAD"
  interval: 15s
  timeout: 5s
  healthy_after: 3
  unhealthy_after: 2
  expected_status: ["2xx"]
`,
			wantErr: false,
		},
		{
			name: "valid per-backend health check",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: "test"
    path: "/test"
    backends:
      - url: "http://localhost:9000"
        health_check:
          path: "/healthz"
          method: "GET"
          expected_status: ["200"]
`,
			wantErr: false,
		},
		{
			name: "invalid method",
			yaml: base + `
health_check:
  method: "PATCH"
`,
			wantErr: true,
		},
		{
			name: "timeout greater than interval",
			yaml: base + `
health_check:
  timeout: 15s
  interval: 5s
`,
			wantErr: true,
		},
		{
			name: "invalid expected_status pattern",
			yaml: base + `
health_check:
  expected_status: ["abc"]
`,
			wantErr: true,
		},
		{
			name: "negative interval",
			yaml: base + `
health_check:
  interval: -1s
`,
			wantErr: true,
		},
		{
			name:    "valid empty config",
			yaml:    base,
			wantErr: false,
		},
		{
			name: "valid multiple status ranges",
			yaml: base + `
health_check:
  expected_status: ["200", "2xx", "300-399"]
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

func TestLoaderValidateUpstreams(t *testing.T) {
	base := `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
`

	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid upstream with backends",
			yaml: base + `
upstreams:
  api-pool:
    backends:
      - url: http://localhost:9000
      - url: http://localhost:9001
routes:
  - id: test
    path: /test
    upstream: api-pool
`,
			wantErr: false,
		},
		{
			name: "valid upstream with service",
			yaml: base + `
upstreams:
  svc-pool:
    service:
      name: my-service
routes:
  - id: test
    path: /test
    upstream: svc-pool
`,
			wantErr: false,
		},
		{
			name: "valid upstream with LB algorithm",
			yaml: base + `
upstreams:
  api-pool:
    backends:
      - url: http://localhost:9000
    load_balancer: least_conn
routes:
  - id: test
    path: /test
    upstream: api-pool
`,
			wantErr: false,
		},
		{
			name: "upstream missing backends and service",
			yaml: base + `
upstreams:
  empty-pool: {}
routes:
  - id: test
    path: /test
    upstream: empty-pool
`,
			wantErr: true,
		},
		{
			name: "upstream with both backends and service",
			yaml: base + `
upstreams:
  both:
    backends:
      - url: http://localhost:9000
    service:
      name: my-service
routes:
  - id: test
    path: /test
    upstream: both
`,
			wantErr: true,
		},
		{
			name: "upstream with invalid LB",
			yaml: base + `
upstreams:
  api-pool:
    backends:
      - url: http://localhost:9000
    load_balancer: random
routes:
  - id: test
    path: /test
    upstream: api-pool
`,
			wantErr: true,
		},
		{
			name: "upstream with consistent_hash missing key",
			yaml: base + `
upstreams:
  api-pool:
    backends:
      - url: http://localhost:9000
    load_balancer: consistent_hash
routes:
  - id: test
    path: /test
    upstream: api-pool
`,
			wantErr: true,
		},
		{
			name: "upstream with valid consistent_hash",
			yaml: base + `
upstreams:
  api-pool:
    backends:
      - url: http://localhost:9000
    load_balancer: consistent_hash
    consistent_hash:
      key: ip
routes:
  - id: test
    path: /test
    upstream: api-pool
`,
			wantErr: false,
		},
		{
			name: "upstream with invalid health check",
			yaml: base + `
upstreams:
  api-pool:
    backends:
      - url: http://localhost:9000
    health_check:
      method: PATCH
routes:
  - id: test
    path: /test
    upstream: api-pool
`,
			wantErr: true,
		},
		{
			name: "route references unknown upstream",
			yaml: base + `
routes:
  - id: test
    path: /test
    upstream: nonexistent
`,
			wantErr: true,
		},
		{
			name: "route with upstream and backends is error",
			yaml: base + `
upstreams:
  api-pool:
    backends:
      - url: http://localhost:9000
routes:
  - id: test
    path: /test
    upstream: api-pool
    backends:
      - url: http://localhost:9001
`,
			wantErr: true,
		},
		{
			name: "route with upstream and service is error",
			yaml: base + `
upstreams:
  api-pool:
    backends:
      - url: http://localhost:9000
routes:
  - id: test
    path: /test
    upstream: api-pool
    service:
      name: my-service
`,
			wantErr: true,
		},
		{
			name: "traffic_split with upstream ref",
			yaml: base + `
upstreams:
  group-a:
    backends:
      - url: http://localhost:9000
  group-b:
    backends:
      - url: http://localhost:9001
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:8000
    traffic_split:
      - name: a
        weight: 50
        upstream: group-a
      - name: b
        weight: 50
        upstream: group-b
`,
			wantErr: false,
		},
		{
			name: "traffic_split with unknown upstream",
			yaml: base + `
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:8000
    traffic_split:
      - name: a
        weight: 50
        upstream: nonexistent
      - name: b
        weight: 50
        backends:
          - url: http://localhost:9001
`,
			wantErr: true,
		},
		{
			name: "traffic_split with upstream and backends is error",
			yaml: base + `
upstreams:
  group-a:
    backends:
      - url: http://localhost:9000
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:8000
    traffic_split:
      - name: a
        weight: 50
        upstream: group-a
        backends:
          - url: http://localhost:9999
      - name: b
        weight: 50
        backends:
          - url: http://localhost:9001
`,
			wantErr: true,
		},
		{
			name: "versioning with upstream ref",
			yaml: base + `
upstreams:
  v1-pool:
    backends:
      - url: http://localhost:9000
  v2-pool:
    backends:
      - url: http://localhost:9001
routes:
  - id: test
    path: /api
    versioning:
      enabled: true
      source: header
      default_version: "v1"
      versions:
        v1:
          upstream: v1-pool
        v2:
          upstream: v2-pool
`,
			wantErr: false,
		},
		{
			name: "versioning with unknown upstream",
			yaml: base + `
routes:
  - id: test
    path: /api
    versioning:
      enabled: true
      source: header
      default_version: "v1"
      versions:
        v1:
          upstream: nonexistent
`,
			wantErr: true,
		},
		{
			name: "versioning with upstream and backends is error",
			yaml: base + `
upstreams:
  v1-pool:
    backends:
      - url: http://localhost:9000
routes:
  - id: test
    path: /api
    versioning:
      enabled: true
      source: header
      default_version: "v1"
      versions:
        v1:
          upstream: v1-pool
          backends:
            - url: http://localhost:9999
`,
			wantErr: true,
		},
		{
			name: "mirror with upstream ref",
			yaml: base + `
upstreams:
  mirror-pool:
    backends:
      - url: http://localhost:9100
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    mirror:
      enabled: true
      upstream: mirror-pool
`,
			wantErr: false,
		},
		{
			name: "mirror with unknown upstream",
			yaml: base + `
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    mirror:
      enabled: true
      upstream: nonexistent
`,
			wantErr: true,
		},
		{
			name: "mirror with upstream and backends is error",
			yaml: base + `
upstreams:
  mirror-pool:
    backends:
      - url: http://localhost:9100
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    mirror:
      enabled: true
      upstream: mirror-pool
      backends:
        - url: http://localhost:9200
`,
			wantErr: true,
		},
		{
			name: "two routes sharing same upstream",
			yaml: base + `
upstreams:
  shared:
    backends:
      - url: http://localhost:9000
      - url: http://localhost:9001
routes:
  - id: route-a
    path: /a
    upstream: shared
  - id: route-b
    path: /b
    upstream: shared
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

func TestLoaderValidateHTTP3(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "enable_http3 without TLS rejected",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
    http:
      enable_http3: true
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: true,
			errMsg:  "enable_http3 requires tls.enabled",
		},
		{
			name: "enable_http3 with TLS passes",
			yaml: `
listeners:
  - id: "https"
    address: ":443"
    protocol: "http"
    tls:
      enabled: true
      cert_file: /dev/null
      key_file: /dev/null
    http:
      enable_http3: true
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`,
			wantErr: false,
		},
		{
			name: "enable_http3 and force_http2 on transport rejected",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
transport:
  enable_http3: true
  force_http2: true
`,
			wantErr: true,
			errMsg:  "enable_http3 and transport.force_http2 are mutually exclusive",
		},
		{
			name: "enable_http3 on transport without force_http2 passes",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
transport:
  enable_http3: true
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewLoader()
			_, err := loader.Parse([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoaderValidateRequestDecompression(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid global decompression",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
request_decompression:
  enabled: true
  algorithms: ["gzip", "br"]
`,
			wantErr: false,
		},
		{
			name: "invalid algorithm rejected",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
request_decompression:
  enabled: true
  algorithms: ["gzip", "lz4"]
`,
			wantErr: true,
			errMsg:  "unsupported algorithm",
		},
		{
			name: "negative max size rejected",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
request_decompression:
  enabled: true
  max_decompressed_size: -1
`,
			wantErr: true,
			errMsg:  "max_decompressed_size must be >= 0",
		},
		{
			name: "per-route decompression valid",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    request_decompression:
      enabled: true
      algorithms: ["zstd", "deflate"]
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewLoader()
			_, err := loader.Parse([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoaderValidateSecurityHeaders(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid global security headers",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
security_headers:
  enabled: true
  strict_transport_security: "max-age=31536000"
  x_frame_options: "DENY"
`,
			wantErr: false,
		},
		{
			name: "valid per-route security headers",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    security_headers:
      enabled: true
      referrer_policy: "no-referrer"
      custom_headers:
        X-Custom: "value"
`,
			wantErr: false,
		},
		{
			name: "empty custom header name rejected",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
security_headers:
  enabled: true
  custom_headers:
    "": "bad"
`,
			wantErr: true,
			errMsg:  "header name must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewLoader()
			_, err := loader.Parse([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoaderValidateMaintenance(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid global maintenance",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
maintenance:
  enabled: true
  status_code: 503
  retry_after: "3600"
  exclude_paths: ["/health"]
  exclude_ips: ["10.0.0.0/8"]
`,
			wantErr: false,
		},
		{
			name: "invalid status code",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
maintenance:
  enabled: true
  status_code: 999
`,
			wantErr: true,
			errMsg:  "valid HTTP status",
		},
		{
			name: "invalid CIDR",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
maintenance:
  enabled: true
  exclude_ips: ["not-a-cidr/33"]
`,
			wantErr: true,
			errMsg:  "invalid CIDR",
		},
		{
			name: "invalid IP",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
maintenance:
  enabled: true
  exclude_ips: ["not-an-ip"]
`,
			wantErr: true,
			errMsg:  "invalid IP",
		},
		{
			name: "valid per-route maintenance",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
    maintenance:
      enabled: true
      body: "Under maintenance"
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewLoader()
			_, err := loader.Parse([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoaderValidateShutdown(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid shutdown config",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
shutdown:
  timeout: 60s
  drain_delay: 10s
`,
			wantErr: false,
		},
		{
			name: "negative timeout",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
shutdown:
  timeout: -1s
`,
			wantErr: true,
			errMsg:  "shutdown.timeout must be >= 0",
		},
		{
			name: "negative drain_delay",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
shutdown:
  drain_delay: -5s
`,
			wantErr: true,
			errMsg:  "shutdown.drain_delay must be >= 0",
		},
		{
			name: "drain_delay >= timeout",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
shutdown:
  timeout: 10s
  drain_delay: 10s
`,
			wantErr: true,
			errMsg:  "shutdown.drain_delay",
		},
		{
			name: "zero values valid (defaults apply)",
			yaml: `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
shutdown: {}
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewLoader()
			_, err := loader.Parse([]byte(tt.yaml))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoaderValidateTransportCertFiles(t *testing.T) {
	base := `
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"
routes:
  - id: test
    path: /test
    backends:
      - url: http://localhost:9000
`

	dir := t.TempDir()
	certFile := dir + "/client.crt"
	keyFile := dir + "/client.key"
	os.WriteFile(certFile, []byte("fake-cert"), 0600)
	os.WriteFile(keyFile, []byte("fake-key"), 0600)

	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "valid both cert and key",
			yaml: base + fmt.Sprintf(`
transport:
  cert_file: %s
  key_file: %s
`, certFile, keyFile),
			wantErr: false,
		},
		{
			name: "cert without key",
			yaml: base + fmt.Sprintf(`
transport:
  cert_file: %s
`, certFile),
			wantErr: true,
		},
		{
			name: "key without cert",
			yaml: base + fmt.Sprintf(`
transport:
  key_file: %s
`, keyFile),
			wantErr: true,
		},
		{
			name: "cert file does not exist",
			yaml: base + `
transport:
  cert_file: /nonexistent/client.crt
  key_file: /nonexistent/client.key
`,
			wantErr: true,
		},
		{
			name: "upstream cert without key",
			yaml: base + fmt.Sprintf(`
upstreams:
  api:
    backends:
      - url: http://localhost:9001
    transport:
      cert_file: %s
`, certFile),
			wantErr: true,
		},
		{
			name: "valid upstream cert and key",
			yaml: base + fmt.Sprintf(`
upstreams:
  api:
    backends:
      - url: http://localhost:9001
    transport:
      cert_file: %s
      key_file: %s
`, certFile, keyFile),
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
