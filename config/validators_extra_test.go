package config

import (
	"strings"
	"testing"
	"time"
)

// --- validateMockAndStaticFiles ---

func TestValidateMockAndStaticFiles(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled mock and static",
			route: RouteConfig{ID: "r1"},
		},
		{
			name: "mock valid with body",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, StatusCode: 200, Body: `{"ok":true}`},
			},
		},
		{
			name: "mock status_code too low",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, StatusCode: 99},
			},
			wantErr: "mock_response.status_code must be 100-599",
		},
		{
			name: "mock status_code too high",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, StatusCode: 600},
			},
			wantErr: "mock_response.status_code must be 100-599",
		},
		{
			name: "mock status_code zero is valid (default)",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, StatusCode: 0, Body: "ok"},
			},
		},
		{
			name: "mock status_code boundary 100",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, StatusCode: 100},
			},
		},
		{
			name: "mock status_code boundary 599",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, StatusCode: 599},
			},
		},
		{
			name: "mock from_spec without openapi",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, FromSpec: true},
			},
			wantErr: "mock_response.from_spec requires openapi.spec_file or openapi.spec_id",
		},
		{
			name: "mock from_spec with spec_file",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, FromSpec: true},
				OpenAPI:      OpenAPIRouteConfig{SpecFile: "spec.yaml"},
			},
		},
		{
			name: "mock from_spec with spec_id",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, FromSpec: true},
				OpenAPI:      OpenAPIRouteConfig{SpecID: "my-spec"},
			},
		},
		{
			name: "mock from_spec with body conflict",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, FromSpec: true, Body: "conflict"},
				OpenAPI:      OpenAPIRouteConfig{SpecFile: "spec.yaml"},
			},
			wantErr: "mock_response.from_spec is mutually exclusive with mock_response.body",
		},
		{
			name: "mock default_status too low",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, DefaultStatus: 99},
			},
			wantErr: "mock_response.default_status must be 100-599",
		},
		{
			name: "mock default_status too high",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, DefaultStatus: 600},
			},
			wantErr: "mock_response.default_status must be 100-599",
		},
		{
			name: "mock default_status valid",
			route: RouteConfig{
				ID:           "r1",
				MockResponse: MockResponseConfig{Enabled: true, DefaultStatus: 200},
			},
		},
		{
			name: "static missing root",
			route: RouteConfig{
				ID:     "r1",
				Static: StaticConfig{Enabled: true},
			},
			wantErr: "static.root is required",
		},
		{
			name: "static valid",
			route: RouteConfig{
				ID:     "r1",
				Static: StaticConfig{Enabled: true, Root: "/var/www"},
			},
		},
		{
			name: "static with echo",
			route: RouteConfig{
				ID:     "r1",
				Echo:   true,
				Static: StaticConfig{Enabled: true, Root: "/var/www"},
			},
			wantErr: "static is mutually exclusive with echo",
		},
		{
			name: "static with backends",
			route: RouteConfig{
				ID:       "r1",
				Backends: []BackendConfig{{URL: "http://localhost"}},
				Static:   StaticConfig{Enabled: true, Root: "/var/www"},
			},
			wantErr: "static is mutually exclusive with backends, service, and upstream",
		},
		{
			name: "static with service",
			route: RouteConfig{
				ID:      "r1",
				Service: ServiceConfig{Name: "svc"},
				Static:  StaticConfig{Enabled: true, Root: "/var/www"},
			},
			wantErr: "static is mutually exclusive with backends, service, and upstream",
		},
		{
			name: "static with upstream",
			route: RouteConfig{
				ID:       "r1",
				Upstream: "http://backend",
				Static:   StaticConfig{Enabled: true, Root: "/var/www"},
			},
			wantErr: "static is mutually exclusive with backends, service, and upstream",
		},
		{
			name: "static with fastcgi",
			route: RouteConfig{
				ID:      "r1",
				Static:  StaticConfig{Enabled: true, Root: "/var/www"},
				FastCGI: FastCGIConfig{Enabled: true},
			},
			wantErr: "static is mutually exclusive with fastcgi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateMockAndStaticFiles(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

// --- validateFastCGI ---

func TestValidateFastCGI(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", FastCGI: FastCGIConfig{Enabled: false}},
		},
		{
			name: "valid minimal",
			route: RouteConfig{
				ID: "r1",
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
				},
			},
		},
		{
			name: "valid with tcp network",
			route: RouteConfig{
				ID: "r1",
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
					Network:      "tcp",
				},
			},
		},
		{
			name: "valid with unix network",
			route: RouteConfig{
				ID: "r1",
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "/var/run/php.sock",
					DocumentRoot: "/var/www",
					Network:      "unix",
				},
			},
		},
		{
			name: "missing address",
			route: RouteConfig{
				ID: "r1",
				FastCGI: FastCGIConfig{
					Enabled:      true,
					DocumentRoot: "/var/www",
				},
			},
			wantErr: "fastcgi.address is required",
		},
		{
			name: "missing document_root",
			route: RouteConfig{
				ID: "r1",
				FastCGI: FastCGIConfig{
					Enabled: true,
					Address: "127.0.0.1:9000",
				},
			},
			wantErr: "fastcgi.document_root is required",
		},
		{
			name: "invalid network",
			route: RouteConfig{
				ID: "r1",
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
					Network:      "udp",
				},
			},
			wantErr: "fastcgi.network must be 'tcp' or 'unix'",
		},
		{
			name: "negative pool_size",
			route: RouteConfig{
				ID: "r1",
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
					PoolSize:     -1,
				},
			},
			wantErr: "fastcgi.pool_size must be >= 0",
		},
		{
			name: "zero pool_size is valid",
			route: RouteConfig{
				ID: "r1",
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
					PoolSize:     0,
				},
			},
		},
		{
			name: "with backends",
			route: RouteConfig{
				ID:       "r1",
				Backends: []BackendConfig{{URL: "http://localhost"}},
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
				},
			},
			wantErr: "fastcgi is mutually exclusive with backends, service, and upstream",
		},
		{
			name: "with service",
			route: RouteConfig{
				ID:      "r1",
				Service: ServiceConfig{Name: "svc"},
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
				},
			},
			wantErr: "fastcgi is mutually exclusive with backends, service, and upstream",
		},
		{
			name: "with upstream",
			route: RouteConfig{
				ID:       "r1",
				Upstream: "http://backend",
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
				},
			},
			wantErr: "fastcgi is mutually exclusive with backends, service, and upstream",
		},
		{
			name: "with echo",
			route: RouteConfig{
				ID:   "r1",
				Echo: true,
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
				},
			},
			wantErr: "fastcgi is mutually exclusive with echo",
		},
		{
			name: "with sequential",
			route: RouteConfig{
				ID:         "r1",
				Sequential: SequentialConfig{Enabled: true},
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
				},
			},
			wantErr: "fastcgi is mutually exclusive with sequential",
		},
		{
			name: "with aggregate",
			route: RouteConfig{
				ID:        "r1",
				Aggregate: AggregateConfig{Enabled: true},
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
				},
			},
			wantErr: "fastcgi is mutually exclusive with aggregate",
		},
		{
			name: "with passthrough",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				FastCGI: FastCGIConfig{
					Enabled:      true,
					Address:      "127.0.0.1:9000",
					DocumentRoot: "/var/www",
				},
			},
			wantErr: "fastcgi is mutually exclusive with passthrough",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateFastCGI(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

// --- validateBackendAuthAndStatusMapping ---

func TestValidateBackendAuthAndStatusMapping(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "both disabled",
			route: RouteConfig{ID: "r1"},
		},
		{
			name: "backend_auth valid",
			route: RouteConfig{
				ID: "r1",
				BackendAuth: BackendAuthConfig{
					Enabled:      true,
					Type:         "oauth2_client_credentials",
					TokenURL:     "https://auth.example.com/token",
					ClientID:     "client-id",
					ClientSecret: "client-secret",
				},
			},
		},
		{
			name: "backend_auth invalid type",
			route: RouteConfig{
				ID: "r1",
				BackendAuth: BackendAuthConfig{
					Enabled:      true,
					Type:         "basic",
					TokenURL:     "https://auth.example.com/token",
					ClientID:     "id",
					ClientSecret: "secret",
				},
			},
			wantErr: "backend_auth.type must be 'oauth2_client_credentials'",
		},
		{
			name: "backend_auth missing token_url",
			route: RouteConfig{
				ID: "r1",
				BackendAuth: BackendAuthConfig{
					Enabled:      true,
					Type:         "oauth2_client_credentials",
					ClientID:     "id",
					ClientSecret: "secret",
				},
			},
			wantErr: "backend_auth.token_url is required",
		},
		{
			name: "backend_auth missing client_id",
			route: RouteConfig{
				ID: "r1",
				BackendAuth: BackendAuthConfig{
					Enabled:      true,
					Type:         "oauth2_client_credentials",
					TokenURL:     "https://auth.example.com/token",
					ClientSecret: "secret",
				},
			},
			wantErr: "backend_auth.client_id is required",
		},
		{
			name: "backend_auth missing client_secret",
			route: RouteConfig{
				ID: "r1",
				BackendAuth: BackendAuthConfig{
					Enabled:      true,
					Type:         "oauth2_client_credentials",
					TokenURL:     "https://auth.example.com/token",
					ClientID:     "id",
				},
			},
			wantErr: "backend_auth.client_secret is required",
		},
		{
			name: "status_mapping valid",
			route: RouteConfig{
				ID: "r1",
				StatusMapping: StatusMappingConfig{
					Enabled:  true,
					Mappings: map[int]int{502: 503, 200: 201},
				},
			},
		},
		{
			name: "status_mapping invalid key too low",
			route: RouteConfig{
				ID: "r1",
				StatusMapping: StatusMappingConfig{
					Enabled:  true,
					Mappings: map[int]int{99: 200},
				},
			},
			wantErr: "status_mapping.mappings key 99 is not a valid HTTP status code",
		},
		{
			name: "status_mapping invalid key too high",
			route: RouteConfig{
				ID: "r1",
				StatusMapping: StatusMappingConfig{
					Enabled:  true,
					Mappings: map[int]int{600: 200},
				},
			},
			wantErr: "status_mapping.mappings key 600 is not a valid HTTP status code",
		},
		{
			name: "status_mapping invalid value too low",
			route: RouteConfig{
				ID: "r1",
				StatusMapping: StatusMappingConfig{
					Enabled:  true,
					Mappings: map[int]int{200: 50},
				},
			},
			wantErr: "status_mapping.mappings value 50 is not a valid HTTP status code",
		},
		{
			name: "status_mapping invalid value too high",
			route: RouteConfig{
				ID: "r1",
				StatusMapping: StatusMappingConfig{
					Enabled:  true,
					Mappings: map[int]int{200: 600},
				},
			},
			wantErr: "status_mapping.mappings value 600 is not a valid HTTP status code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateBackendAuthAndStatusMapping(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

// --- validateSequentialProxy ---

func TestValidateSequentialProxy(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", Sequential: SequentialConfig{Enabled: false}},
		},
		{
			name: "valid with two steps",
			route: RouteConfig{
				ID: "r1",
				Sequential: SequentialConfig{
					Enabled: true,
					Steps: []SequentialStep{
						{URL: "http://svc1/api"},
						{URL: "http://svc2/api"},
					},
				},
			},
		},
		{
			name: "too few steps",
			route: RouteConfig{
				ID: "r1",
				Sequential: SequentialConfig{
					Enabled: true,
					Steps:   []SequentialStep{{URL: "http://svc1/api"}},
				},
			},
			wantErr: "sequential requires at least 2 steps",
		},
		{
			name: "zero steps",
			route: RouteConfig{
				ID:         "r1",
				Sequential: SequentialConfig{Enabled: true},
			},
			wantErr: "sequential requires at least 2 steps",
		},
		{
			name: "step missing URL",
			route: RouteConfig{
				ID: "r1",
				Sequential: SequentialConfig{
					Enabled: true,
					Steps: []SequentialStep{
						{URL: "http://svc1/api"},
						{URL: ""},
					},
				},
			},
			wantErr: "sequential step 1 requires a URL",
		},
		{
			name: "with echo",
			route: RouteConfig{
				ID:   "r1",
				Echo: true,
				Sequential: SequentialConfig{
					Enabled: true,
					Steps: []SequentialStep{
						{URL: "http://svc1/api"},
						{URL: "http://svc2/api"},
					},
				},
			},
			wantErr: "sequential is mutually exclusive with echo",
		},
		{
			name: "with static",
			route: RouteConfig{
				ID:     "r1",
				Static: StaticConfig{Enabled: true, Root: "/www"},
				Sequential: SequentialConfig{
					Enabled: true,
					Steps: []SequentialStep{
						{URL: "http://svc1/api"},
						{URL: "http://svc2/api"},
					},
				},
			},
			wantErr: "sequential is mutually exclusive with static",
		},
		{
			name: "with fastcgi",
			route: RouteConfig{
				ID:      "r1",
				FastCGI: FastCGIConfig{Enabled: true},
				Sequential: SequentialConfig{
					Enabled: true,
					Steps: []SequentialStep{
						{URL: "http://svc1/api"},
						{URL: "http://svc2/api"},
					},
				},
			},
			wantErr: "sequential is mutually exclusive with fastcgi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSequentialProxy(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

// --- validateAggregateProxy ---

func TestValidateAggregateProxy(t *testing.T) {
	l := NewLoader()

	validBackends := []AggregateBackend{
		{Name: "users", URL: "http://users-svc/api"},
		{Name: "orders", URL: "http://orders-svc/api"},
	}

	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", Aggregate: AggregateConfig{Enabled: false}},
		},
		{
			name: "valid with two backends",
			route: RouteConfig{
				ID:        "r1",
				Aggregate: AggregateConfig{Enabled: true, Backends: validBackends},
			},
		},
		{
			name: "too few backends",
			route: RouteConfig{
				ID: "r1",
				Aggregate: AggregateConfig{
					Enabled:  true,
					Backends: []AggregateBackend{{Name: "a", URL: "http://a"}},
				},
			},
			wantErr: "aggregate requires at least 2 backends",
		},
		{
			name: "zero backends",
			route: RouteConfig{
				ID:        "r1",
				Aggregate: AggregateConfig{Enabled: true},
			},
			wantErr: "aggregate requires at least 2 backends",
		},
		{
			name: "backend missing name",
			route: RouteConfig{
				ID: "r1",
				Aggregate: AggregateConfig{
					Enabled: true,
					Backends: []AggregateBackend{
						{Name: "a", URL: "http://a"},
						{URL: "http://b"},
					},
				},
			},
			wantErr: "aggregate backend 1 requires a name",
		},
		{
			name: "duplicate backend name",
			route: RouteConfig{
				ID: "r1",
				Aggregate: AggregateConfig{
					Enabled: true,
					Backends: []AggregateBackend{
						{Name: "dup", URL: "http://a"},
						{Name: "dup", URL: "http://b"},
					},
				},
			},
			wantErr: "duplicate aggregate backend name: dup",
		},
		{
			name: "backend missing URL",
			route: RouteConfig{
				ID: "r1",
				Aggregate: AggregateConfig{
					Enabled: true,
					Backends: []AggregateBackend{
						{Name: "a", URL: "http://a"},
						{Name: "b"},
					},
				},
			},
			wantErr: "aggregate backend b requires a URL",
		},
		{
			name: "valid fail_strategy abort",
			route: RouteConfig{
				ID: "r1",
				Aggregate: AggregateConfig{
					Enabled:      true,
					Backends:     validBackends,
					FailStrategy: "abort",
				},
			},
		},
		{
			name: "valid fail_strategy partial",
			route: RouteConfig{
				ID: "r1",
				Aggregate: AggregateConfig{
					Enabled:      true,
					Backends:     validBackends,
					FailStrategy: "partial",
				},
			},
		},
		{
			name: "valid fail_strategy empty (default)",
			route: RouteConfig{
				ID: "r1",
				Aggregate: AggregateConfig{
					Enabled:  true,
					Backends: validBackends,
				},
			},
		},
		{
			name: "invalid fail_strategy",
			route: RouteConfig{
				ID: "r1",
				Aggregate: AggregateConfig{
					Enabled:      true,
					Backends:     validBackends,
					FailStrategy: "ignore",
				},
			},
			wantErr: "aggregate fail_strategy must be 'abort' or 'partial'",
		},
		{
			name: "with echo",
			route: RouteConfig{
				ID:        "r1",
				Echo:      true,
				Aggregate: AggregateConfig{Enabled: true, Backends: validBackends},
			},
			wantErr: "aggregate is mutually exclusive with echo",
		},
		{
			name: "with sequential",
			route: RouteConfig{
				ID:         "r1",
				Sequential: SequentialConfig{Enabled: true},
				Aggregate:  AggregateConfig{Enabled: true, Backends: validBackends},
			},
			wantErr: "aggregate is mutually exclusive with sequential",
		},
		{
			name: "with static",
			route: RouteConfig{
				ID:        "r1",
				Static:    StaticConfig{Enabled: true, Root: "/www"},
				Aggregate: AggregateConfig{Enabled: true, Backends: validBackends},
			},
			wantErr: "aggregate is mutually exclusive with static",
		},
		{
			name: "with fastcgi",
			route: RouteConfig{
				ID:        "r1",
				FastCGI:   FastCGIConfig{Enabled: true},
				Aggregate: AggregateConfig{Enabled: true, Backends: validBackends},
			},
			wantErr: "aggregate is mutually exclusive with fastcgi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateAggregateProxy(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

// --- validateSmallRouteFeatures ---

func TestValidateSmallRouteFeatures_SpikeArrest(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", SpikeArrest: SpikeArrestConfig{Enabled: false}},
		},
		{
			name:  "valid rate",
			route: RouteConfig{ID: "r1", SpikeArrest: SpikeArrestConfig{Enabled: true, Rate: 10}},
		},
		{
			name:    "zero rate",
			route:   RouteConfig{ID: "r1", SpikeArrest: SpikeArrestConfig{Enabled: true, Rate: 0}},
			wantErr: "spike_arrest rate must be > 0",
		},
		{
			name:    "negative rate",
			route:   RouteConfig{ID: "r1", SpikeArrest: SpikeArrestConfig{Enabled: true, Rate: -5}},
			wantErr: "spike_arrest rate must be > 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_ContentReplacer(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", ContentReplacer: ContentReplacerConfig{Enabled: false}},
		},
		{
			name: "valid replacements",
			route: RouteConfig{
				ID: "r1",
				ContentReplacer: ContentReplacerConfig{
					Enabled:      true,
					Replacements: []ReplacementRule{{Pattern: `\d+`, Replacement: "***"}},
				},
			},
		},
		{
			name: "no replacements",
			route: RouteConfig{
				ID:              "r1",
				ContentReplacer: ContentReplacerConfig{Enabled: true},
			},
			wantErr: "content_replacer requires at least one replacement",
		},
		{
			name: "invalid regex pattern",
			route: RouteConfig{
				ID: "r1",
				ContentReplacer: ContentReplacerConfig{
					Enabled:      true,
					Replacements: []ReplacementRule{{Pattern: "[invalid", Replacement: "x"}},
				},
			},
			wantErr: "invalid pattern",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_FollowRedirects(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", FollowRedirects: FollowRedirectsConfig{Enabled: false}},
		},
		{
			name:  "valid zero max",
			route: RouteConfig{ID: "r1", FollowRedirects: FollowRedirectsConfig{Enabled: true, MaxRedirects: 0}},
		},
		{
			name:  "valid positive max",
			route: RouteConfig{ID: "r1", FollowRedirects: FollowRedirectsConfig{Enabled: true, MaxRedirects: 10}},
		},
		{
			name:    "negative max_redirects",
			route:   RouteConfig{ID: "r1", FollowRedirects: FollowRedirectsConfig{Enabled: true, MaxRedirects: -1}},
			wantErr: "follow_redirects max_redirects must be >= 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_BodyGenerator(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", BodyGenerator: BodyGeneratorConfig{Enabled: false}},
		},
		{
			name: "valid",
			route: RouteConfig{
				ID:            "r1",
				BodyGenerator: BodyGeneratorConfig{Enabled: true, Template: `{"key":"value"}`},
			},
		},
		{
			name: "missing template",
			route: RouteConfig{
				ID:            "r1",
				BodyGenerator: BodyGeneratorConfig{Enabled: true},
			},
			wantErr: "body_generator requires a template",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_ResponseBodyGenerator(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", ResponseBodyGenerator: ResponseBodyGeneratorConfig{Enabled: false}},
		},
		{
			name: "valid",
			route: RouteConfig{
				ID:                    "r1",
				ResponseBodyGenerator: ResponseBodyGeneratorConfig{Enabled: true, Template: `{"ok":true}`},
			},
		},
		{
			name: "missing template",
			route: RouteConfig{
				ID:                    "r1",
				ResponseBodyGenerator: ResponseBodyGeneratorConfig{Enabled: true},
			},
			wantErr: "response_body_generator requires a template",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_ParamForwarding(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", ParamForwarding: ParamForwardingConfig{Enabled: false}},
		},
		{
			name: "valid with headers",
			route: RouteConfig{
				ID:              "r1",
				ParamForwarding: ParamForwardingConfig{Enabled: true, Headers: []string{"X-Custom"}},
			},
		},
		{
			name: "valid with query_params",
			route: RouteConfig{
				ID:              "r1",
				ParamForwarding: ParamForwardingConfig{Enabled: true, QueryParams: []string{"page"}},
			},
		},
		{
			name: "valid with cookies",
			route: RouteConfig{
				ID:              "r1",
				ParamForwarding: ParamForwardingConfig{Enabled: true, Cookies: []string{"session"}},
			},
		},
		{
			name: "no params",
			route: RouteConfig{
				ID:              "r1",
				ParamForwarding: ParamForwardingConfig{Enabled: true},
			},
			wantErr: "param_forwarding requires at least one of headers, query_params, or cookies",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_SSE(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", SSE: SSEConfig{Enabled: false}},
		},
		{
			name:  "valid minimal",
			route: RouteConfig{ID: "r1", SSE: SSEConfig{Enabled: true}},
		},
		{
			name:    "negative heartbeat_interval",
			route:   RouteConfig{ID: "r1", SSE: SSEConfig{Enabled: true, HeartbeatInterval: -1}},
			wantErr: "sse.heartbeat_interval must be >= 0",
		},
		{
			name:    "negative retry_ms",
			route:   RouteConfig{ID: "r1", SSE: SSEConfig{Enabled: true, RetryMS: -1}},
			wantErr: "sse.retry_ms must be >= 0",
		},
		{
			name:    "negative max_idle",
			route:   RouteConfig{ID: "r1", SSE: SSEConfig{Enabled: true, MaxIdle: -1}},
			wantErr: "sse.max_idle must be >= 0",
		},
		{
			name: "with passthrough",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				SSE:         SSEConfig{Enabled: true},
			},
			wantErr: "sse is mutually exclusive with passthrough",
		},
		{
			name: "with response_body_generator",
			route: RouteConfig{
				ID:                    "r1",
				ResponseBodyGenerator: ResponseBodyGeneratorConfig{Enabled: true, Template: "x"},
				SSE:                   SSEConfig{Enabled: true},
			},
			wantErr: "sse is mutually exclusive with response_body_generator",
		},
		{
			name: "fanout valid",
			route: RouteConfig{
				ID: "r1",
				SSE: SSEConfig{
					Enabled: true,
					Fanout:  SSEFanoutConfig{Enabled: true, BufferSize: 100, ClientBufferSize: 50},
				},
			},
		},
		{
			name: "fanout negative buffer_size",
			route: RouteConfig{
				ID: "r1",
				SSE: SSEConfig{
					Enabled: true,
					Fanout:  SSEFanoutConfig{Enabled: true, BufferSize: -1},
				},
			},
			wantErr: "sse.fanout.buffer_size must be >= 0",
		},
		{
			name: "fanout negative client_buffer_size",
			route: RouteConfig{
				ID: "r1",
				SSE: SSEConfig{
					Enabled: true,
					Fanout:  SSEFanoutConfig{Enabled: true, ClientBufferSize: -1},
				},
			},
			wantErr: "sse.fanout.client_buffer_size must be >= 0",
		},
		{
			name: "fanout negative reconnect_delay",
			route: RouteConfig{
				ID: "r1",
				SSE: SSEConfig{
					Enabled: true,
					Fanout:  SSEFanoutConfig{Enabled: true, ReconnectDelay: -1},
				},
			},
			wantErr: "sse.fanout.reconnect_delay must be >= 0",
		},
		{
			name: "fanout negative max_reconnects",
			route: RouteConfig{
				ID: "r1",
				SSE: SSEConfig{
					Enabled: true,
					Fanout:  SSEFanoutConfig{Enabled: true, MaxReconnects: -1},
				},
			},
			wantErr: "sse.fanout.max_reconnects must be >= 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_ContentNegotiation(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", ContentNegotiation: ContentNegotiationConfig{Enabled: false}},
		},
		{
			name: "valid formats",
			route: RouteConfig{
				ID: "r1",
				ContentNegotiation: ContentNegotiationConfig{
					Enabled:   true,
					Supported: []string{"json", "xml", "yaml"},
					Default:   "json",
				},
			},
		},
		{
			name: "invalid supported format",
			route: RouteConfig{
				ID: "r1",
				ContentNegotiation: ContentNegotiationConfig{
					Enabled:   true,
					Supported: []string{"json", "csv"},
				},
			},
			wantErr: `content_negotiation supported format "csv" must be json, xml, or yaml`,
		},
		{
			name: "invalid default format",
			route: RouteConfig{
				ID: "r1",
				ContentNegotiation: ContentNegotiationConfig{
					Enabled: true,
					Default: "html",
				},
			},
			wantErr: `content_negotiation default "html" must be json, xml, or yaml`,
		},
		{
			name: "empty default is valid",
			route: RouteConfig{
				ID: "r1",
				ContentNegotiation: ContentNegotiationConfig{
					Enabled:   true,
					Supported: []string{"json"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_CDNCacheHeaders(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", CDNCacheHeaders: CDNCacheConfig{Enabled: false}},
		},
		{
			name: "valid with cache_control",
			route: RouteConfig{
				ID:              "r1",
				CDNCacheHeaders: CDNCacheConfig{Enabled: true, CacheControl: "public, max-age=3600"},
			},
		},
		{
			name: "valid with surrogate_control",
			route: RouteConfig{
				ID:              "r1",
				CDNCacheHeaders: CDNCacheConfig{Enabled: true, SurrogateControl: "max-age=86400"},
			},
		},
		{
			name: "valid with vary",
			route: RouteConfig{
				ID:              "r1",
				CDNCacheHeaders: CDNCacheConfig{Enabled: true, Vary: []string{"Accept"}},
			},
		},
		{
			name: "none specified",
			route: RouteConfig{
				ID:              "r1",
				CDNCacheHeaders: CDNCacheConfig{Enabled: true},
			},
			wantErr: "cdn_cache_headers requires at least one of cache_control, surrogate_control, or vary",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_EdgeCacheRules(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", EdgeCacheRules: EdgeCacheRulesConfig{Enabled: false}},
		},
		{
			name: "valid rule",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match:        EdgeCacheMatch{StatusCodes: []int{200}},
							CacheControl: "public, max-age=3600",
						},
					},
				},
			},
		},
		{
			name: "invalid status code too low",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match:        EdgeCacheMatch{StatusCodes: []int{50}},
							CacheControl: "no-store",
						},
					},
				},
			},
			wantErr: "status_code 50 must be 100-599",
		},
		{
			name: "invalid status code too high",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match:        EdgeCacheMatch{StatusCodes: []int{700}},
							CacheControl: "no-store",
						},
					},
				},
			},
			wantErr: "status_code 700 must be 100-599",
		},
		{
			name: "empty content type",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match:        EdgeCacheMatch{ContentTypes: []string{"  "}},
							CacheControl: "no-store",
						},
					},
				},
			},
			wantErr: "content_types must be non-empty strings",
		},
		{
			name: "invalid path pattern",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match:        EdgeCacheMatch{PathPatterns: []string{"[bad"}},
							CacheControl: "no-store",
						},
					},
				},
			},
			wantErr: "invalid path_pattern",
		},
		{
			name: "no match conditions",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match:        EdgeCacheMatch{},
							CacheControl: "no-store",
						},
					},
				},
			},
			wantErr: "at least one match condition required",
		},
		{
			name: "no actions",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match: EdgeCacheMatch{StatusCodes: []int{200}},
						},
					},
				},
			},
			wantErr: "at least one action",
		},
		{
			name: "valid rule with s_maxage",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match:   EdgeCacheMatch{StatusCodes: []int{200}},
							SMaxAge: 3600,
						},
					},
				},
			},
		},
		{
			name: "valid rule with max_age",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match:  EdgeCacheMatch{StatusCodes: []int{200}},
							MaxAge: 600,
						},
					},
				},
			},
		},
		{
			name: "valid rule with no_store",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match:   EdgeCacheMatch{StatusCodes: []int{200}},
							NoStore: true,
						},
					},
				},
			},
		},
		{
			name: "valid rule with private",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match:   EdgeCacheMatch{StatusCodes: []int{200}},
							Private: true,
						},
					},
				},
			},
		},
		{
			name: "valid rule with vary",
			route: RouteConfig{
				ID: "r1",
				EdgeCacheRules: EdgeCacheRulesConfig{
					Enabled: true,
					Rules: []EdgeCacheRule{
						{
							Match: EdgeCacheMatch{StatusCodes: []int{200}},
							Vary:  []string{"Accept-Encoding"},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_CacheTagsAndBucket(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name: "valid cache tags",
			route: RouteConfig{
				ID:    "r1",
				Cache: CacheConfig{Tags: []string{"product", "listing"}},
			},
		},
		{
			name: "empty cache tag",
			route: RouteConfig{
				ID:    "r1",
				Cache: CacheConfig{Tags: []string{"valid", "  "}},
			},
			wantErr: "cache tags must be non-empty strings",
		},
		{
			name: "valid cache tag_headers",
			route: RouteConfig{
				ID:    "r1",
				Cache: CacheConfig{TagHeaders: []string{"X-Cache-Tag"}},
			},
		},
		{
			name: "empty cache tag_header",
			route: RouteConfig{
				ID:    "r1",
				Cache: CacheConfig{TagHeaders: []string{" "}},
			},
			wantErr: "cache tag_headers must be non-empty strings",
		},
		{
			name: "valid cache bucket",
			route: RouteConfig{
				ID:    "r1",
				Cache: CacheConfig{Bucket: "my-bucket_1"},
			},
		},
		{
			name: "invalid cache bucket characters",
			route: RouteConfig{
				ID:    "r1",
				Cache: CacheConfig{Bucket: "my bucket!"},
			},
			wantErr: "cache bucket name must be alphanumeric with hyphens/underscores",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_BackendEncoding(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "empty encoding is valid",
			route: RouteConfig{ID: "r1"},
		},
		{
			name:  "xml is valid",
			route: RouteConfig{ID: "r1", BackendEncoding: BackendEncodingConfig{Encoding: "xml"}},
		},
		{
			name:  "yaml is valid",
			route: RouteConfig{ID: "r1", BackendEncoding: BackendEncodingConfig{Encoding: "yaml"}},
		},
		{
			name:    "invalid encoding",
			route:   RouteConfig{ID: "r1", BackendEncoding: BackendEncodingConfig{Encoding: "protobuf"}},
			wantErr: "backend_encoding encoding must be 'xml' or 'yaml'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_Quota(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", Quota: QuotaConfig{Enabled: false}},
		},
		{
			name: "valid",
			route: RouteConfig{
				ID:    "r1",
				Quota: QuotaConfig{Enabled: true, Limit: 1000, Period: "daily", Key: "ip"},
			},
		},
		{
			name: "zero limit",
			route: RouteConfig{
				ID:    "r1",
				Quota: QuotaConfig{Enabled: true, Limit: 0, Period: "daily", Key: "ip"},
			},
			wantErr: "quota limit must be > 0",
		},
		{
			name: "negative limit",
			route: RouteConfig{
				ID:    "r1",
				Quota: QuotaConfig{Enabled: true, Limit: -1, Period: "daily", Key: "ip"},
			},
			wantErr: "quota limit must be > 0",
		},
		{
			name: "invalid period",
			route: RouteConfig{
				ID:    "r1",
				Quota: QuotaConfig{Enabled: true, Limit: 100, Period: "weekly", Key: "ip"},
			},
			wantErr: "quota period must be hourly, daily, monthly, or yearly",
		},
		{
			name: "valid hourly",
			route: RouteConfig{
				ID:    "r1",
				Quota: QuotaConfig{Enabled: true, Limit: 100, Period: "hourly", Key: "ip"},
			},
		},
		{
			name: "valid monthly",
			route: RouteConfig{
				ID:    "r1",
				Quota: QuotaConfig{Enabled: true, Limit: 100, Period: "monthly", Key: "ip"},
			},
		},
		{
			name: "valid yearly",
			route: RouteConfig{
				ID:    "r1",
				Quota: QuotaConfig{Enabled: true, Limit: 100, Period: "yearly", Key: "ip"},
			},
		},
		{
			name: "missing key",
			route: RouteConfig{
				ID:    "r1",
				Quota: QuotaConfig{Enabled: true, Limit: 100, Period: "daily"},
			},
			wantErr: "quota key is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_ProxyRateLimit(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", ProxyRateLimit: ProxyRateLimitConfig{Enabled: false}},
		},
		{
			name: "valid",
			route: RouteConfig{
				ID:             "r1",
				ProxyRateLimit: ProxyRateLimitConfig{Enabled: true, Rate: 100},
			},
		},
		{
			name: "zero rate",
			route: RouteConfig{
				ID:             "r1",
				ProxyRateLimit: ProxyRateLimitConfig{Enabled: true, Rate: 0},
			},
			wantErr: "proxy_rate_limit.rate must be > 0",
		},
		{
			name: "negative rate",
			route: RouteConfig{
				ID:             "r1",
				ProxyRateLimit: ProxyRateLimitConfig{Enabled: true, Rate: -1},
			},
			wantErr: "proxy_rate_limit.rate must be > 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSmallRouteFeatures_ClaimsPropagation(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name:  "disabled",
			route: RouteConfig{ID: "r1", ClaimsPropagation: ClaimsPropagationConfig{Enabled: false}},
		},
		{
			name: "valid",
			route: RouteConfig{
				ID: "r1",
				ClaimsPropagation: ClaimsPropagationConfig{
					Enabled: true,
					Claims:  map[string]string{"sub": "X-User-ID", "email": "X-User-Email"},
				},
			},
		},
		{
			name: "no claims",
			route: RouteConfig{
				ID:                "r1",
				ClaimsPropagation: ClaimsPropagationConfig{Enabled: true},
			},
			wantErr: "claims_propagation: at least one claim mapping is required",
		},
		{
			name: "empty claim name",
			route: RouteConfig{
				ID: "r1",
				ClaimsPropagation: ClaimsPropagationConfig{
					Enabled: true,
					Claims:  map[string]string{"": "X-Header"},
				},
			},
			wantErr: "claims_propagation: empty claim name",
		},
		{
			name: "empty header name",
			route: RouteConfig{
				ID: "r1",
				ClaimsPropagation: ClaimsPropagationConfig{
					Enabled: true,
					Claims:  map[string]string{"sub": ""},
				},
			},
			wantErr: "claims_propagation: empty header name for claim",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateSmallRouteFeatures(tt.route, nil)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

// Suppress unused import warnings for time (used in SSE tests).
var _ = time.Second
