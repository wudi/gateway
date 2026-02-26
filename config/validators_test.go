package config

import (
	"strings"
	"testing"
	"time"
)

// --- validateOutlierDetection ---

func TestValidateOutlierDetection_Disabled(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:               "r1",
		OutlierDetection: OutlierDetectionConfig{Enabled: false},
	}
	if err := l.validateOutlierDetection(route, nil); err != nil {
		t.Fatalf("expected no error for disabled outlier detection, got: %v", err)
	}
}

func TestValidateOutlierDetection_ValidConfig(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID: "r1",
		OutlierDetection: OutlierDetectionConfig{
			Enabled:              true,
			Interval:             10 * time.Second,
			Window:               30 * time.Second,
			MinRequests:          10,
			ErrorRateThreshold:   0.5,
			ErrorRateMultiplier:  2.0,
			LatencyMultiplier:    3.0,
			BaseEjectionDuration: 30 * time.Second,
			MaxEjectionDuration:  5 * time.Minute,
			MaxEjectionPercent:   50,
		},
	}
	if err := l.validateOutlierDetection(route, nil); err != nil {
		t.Fatalf("expected no error for valid config, got: %v", err)
	}
}

func TestValidateOutlierDetection_ZeroValues(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID: "r1",
		OutlierDetection: OutlierDetectionConfig{
			Enabled: true,
			// All zero values should be valid
		},
	}
	if err := l.validateOutlierDetection(route, nil); err != nil {
		t.Fatalf("expected no error for zero values, got: %v", err)
	}
}

func TestValidateOutlierDetection_Errors(t *testing.T) {
	tests := []struct {
		name    string
		od      OutlierDetectionConfig
		wantErr string
	}{
		{
			name:    "negative interval",
			od:      OutlierDetectionConfig{Enabled: true, Interval: -1},
			wantErr: "outlier_detection.interval must be >= 0",
		},
		{
			name:    "negative window",
			od:      OutlierDetectionConfig{Enabled: true, Window: -1},
			wantErr: "outlier_detection.window must be >= 0",
		},
		{
			name:    "negative min_requests",
			od:      OutlierDetectionConfig{Enabled: true, MinRequests: -1},
			wantErr: "outlier_detection.min_requests must be >= 0",
		},
		{
			name:    "error_rate_threshold below zero",
			od:      OutlierDetectionConfig{Enabled: true, ErrorRateThreshold: -0.1},
			wantErr: "outlier_detection.error_rate_threshold must be between 0.0 and 1.0",
		},
		{
			name:    "error_rate_threshold above one",
			od:      OutlierDetectionConfig{Enabled: true, ErrorRateThreshold: 1.1},
			wantErr: "outlier_detection.error_rate_threshold must be between 0.0 and 1.0",
		},
		{
			name:    "negative error_rate_multiplier",
			od:      OutlierDetectionConfig{Enabled: true, ErrorRateMultiplier: -1},
			wantErr: "outlier_detection.error_rate_multiplier must be >= 0",
		},
		{
			name:    "negative latency_multiplier",
			od:      OutlierDetectionConfig{Enabled: true, LatencyMultiplier: -1},
			wantErr: "outlier_detection.latency_multiplier must be >= 0",
		},
		{
			name:    "negative base_ejection_duration",
			od:      OutlierDetectionConfig{Enabled: true, BaseEjectionDuration: -1},
			wantErr: "outlier_detection.base_ejection_duration must be >= 0",
		},
		{
			name:    "negative max_ejection_duration",
			od:      OutlierDetectionConfig{Enabled: true, MaxEjectionDuration: -1},
			wantErr: "outlier_detection.max_ejection_duration must be >= 0",
		},
		{
			name: "max_ejection < base_ejection when both positive",
			od: OutlierDetectionConfig{
				Enabled:              true,
				BaseEjectionDuration: 5 * time.Minute,
				MaxEjectionDuration:  1 * time.Minute,
			},
			wantErr: "outlier_detection.max_ejection_duration must be >= base_ejection_duration",
		},
		{
			name:    "max_ejection_percent below zero",
			od:      OutlierDetectionConfig{Enabled: true, MaxEjectionPercent: -1},
			wantErr: "outlier_detection.max_ejection_percent must be between 0 and 100",
		},
		{
			name:    "max_ejection_percent above 100",
			od:      OutlierDetectionConfig{Enabled: true, MaxEjectionPercent: 101},
			wantErr: "outlier_detection.max_ejection_percent must be between 0 and 100",
		},
	}

	l := NewLoader()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := RouteConfig{ID: "r1", OutlierDetection: tt.od}
			err := l.validateOutlierDetection(route, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateOutlierDetection_BoundaryValues(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name string
		od   OutlierDetectionConfig
	}{
		{
			name: "error_rate_threshold at 0",
			od:   OutlierDetectionConfig{Enabled: true, ErrorRateThreshold: 0},
		},
		{
			name: "error_rate_threshold at 1",
			od:   OutlierDetectionConfig{Enabled: true, ErrorRateThreshold: 1.0},
		},
		{
			name: "max_ejection_percent at 0",
			od:   OutlierDetectionConfig{Enabled: true, MaxEjectionPercent: 0},
		},
		{
			name: "max_ejection_percent at 100",
			od:   OutlierDetectionConfig{Enabled: true, MaxEjectionPercent: 100},
		},
		{
			name: "max_ejection_duration equals base",
			od: OutlierDetectionConfig{
				Enabled:              true,
				BaseEjectionDuration: 30 * time.Second,
				MaxEjectionDuration:  30 * time.Second,
			},
		},
		{
			name: "max_ejection_duration positive with zero base",
			od: OutlierDetectionConfig{
				Enabled:              true,
				BaseEjectionDuration: 0,
				MaxEjectionDuration:  5 * time.Minute,
			},
		},
		{
			name: "base_ejection positive with zero max",
			od: OutlierDetectionConfig{
				Enabled:              true,
				BaseEjectionDuration: 30 * time.Second,
				MaxEjectionDuration:  0,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := RouteConfig{ID: "r1", OutlierDetection: tt.od}
			if err := l.validateOutlierDetection(route, nil); err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

// --- validatePassthroughExclusions ---

func TestValidatePassthroughExclusions_NotPassthrough(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:          "r1",
		Passthrough: false,
		Validation:  ValidationConfig{Enabled: true}, // would fail if passthrough
	}
	if err := l.validatePassthroughExclusions(route, nil); err != nil {
		t.Fatalf("expected no error for non-passthrough route, got: %v", err)
	}
}

func TestValidatePassthroughExclusions_Errors(t *testing.T) {
	tests := []struct {
		name    string
		route   RouteConfig
		wantErr string
	}{
		{
			name: "body transform request",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				Transform:   TransformConfig{Request: RequestTransform{Body: BodyTransformConfig{Template: "x"}}},
			},
			wantErr: "passthrough is mutually exclusive with body transforms",
		},
		{
			name: "body transform response",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				Transform:   TransformConfig{Response: ResponseTransform{Body: BodyTransformConfig{Template: "x"}}},
			},
			wantErr: "passthrough is mutually exclusive with body transforms",
		},
		{
			name: "validation",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				Validation:  ValidationConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with validation",
		},
		{
			name: "compression",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				Compression: CompressionConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with compression",
		},
		{
			name: "cache",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				Cache:       CacheConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with cache",
		},
		{
			name: "graphql",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				GraphQL:     GraphQLConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with graphql",
		},
		{
			name: "openapi spec_file",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				OpenAPI:     OpenAPIRouteConfig{SpecFile: "spec.yaml"},
			},
			wantErr: "passthrough is mutually exclusive with openapi",
		},
		{
			name: "openapi spec_id",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				OpenAPI:     OpenAPIRouteConfig{SpecID: "my-spec"},
			},
			wantErr: "passthrough is mutually exclusive with openapi",
		},
		{
			name: "request_decompression",
			route: RouteConfig{
				ID:                   "r1",
				Passthrough:          true,
				RequestDecompression: RequestDecompressionConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with request_decompression",
		},
		{
			name: "response_limit",
			route: RouteConfig{
				ID:            "r1",
				Passthrough:   true,
				ResponseLimit: ResponseLimitConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with response_limit",
		},
		{
			name: "content_replacer",
			route: RouteConfig{
				ID:              "r1",
				Passthrough:     true,
				ContentReplacer: ContentReplacerConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with content_replacer",
		},
		{
			name: "body_generator",
			route: RouteConfig{
				ID:            "r1",
				Passthrough:   true,
				BodyGenerator: BodyGeneratorConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with body_generator",
		},
		{
			name: "sequential",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				Sequential:  SequentialConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with sequential",
		},
		{
			name: "aggregate",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				Aggregate:   AggregateConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with aggregate",
		},
		{
			name: "response_body_generator",
			route: RouteConfig{
				ID:                    "r1",
				Passthrough:           true,
				ResponseBodyGenerator: ResponseBodyGeneratorConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with response_body_generator",
		},
		{
			name: "content_negotiation",
			route: RouteConfig{
				ID:                 "r1",
				Passthrough:        true,
				ContentNegotiation: ContentNegotiationConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with content_negotiation",
		},
		{
			name: "backend_encoding",
			route: RouteConfig{
				ID:              "r1",
				Passthrough:     true,
				BackendEncoding: BackendEncodingConfig{Encoding: "xml"},
			},
			wantErr: "passthrough is mutually exclusive with backend_encoding",
		},
		{
			name: "pii_redaction",
			route: RouteConfig{
				ID:           "r1",
				Passthrough:  true,
				PIIRedaction: PIIRedactionConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with pii_redaction",
		},
		{
			name: "field_encryption",
			route: RouteConfig{
				ID:              "r1",
				Passthrough:     true,
				FieldEncryption: FieldEncryptionConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with field_encryption",
		},
		{
			name: "fastcgi",
			route: RouteConfig{
				ID:          "r1",
				Passthrough: true,
				FastCGI:     FastCGIConfig{Enabled: true},
			},
			wantErr: "passthrough is mutually exclusive with fastcgi",
		},
	}

	l := NewLoader()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validatePassthroughExclusions(tt.route, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidatePassthroughExclusions_PassthroughWithNoConflicts(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:          "r1",
		Passthrough: true,
		// None of the exclusive features enabled
	}
	if err := l.validatePassthroughExclusions(route, nil); err != nil {
		t.Fatalf("expected no error for passthrough with no conflicts, got: %v", err)
	}
}

// --- validateTokenExchangeConfig ---

func TestValidateTokenExchangeConfig_Disabled(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:            "r1",
		TokenExchange: TokenExchangeConfig{Enabled: false},
	}
	if err := l.validateTokenExchangeConfig("route r1", route); err != nil {
		t.Fatalf("expected no error for disabled token exchange, got: %v", err)
	}
}

func TestValidateTokenExchangeConfig_RequiresAuth(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:   "r1",
		Auth: RouteAuthConfig{Required: false},
		TokenExchange: TokenExchangeConfig{
			Enabled:        true,
			ValidationMode: "jwt",
		},
	}
	err := l.validateTokenExchangeConfig("route r1", route)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "token_exchange requires auth.required to be true") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTokenExchangeConfig_JWTMode_Valid(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:   "r1",
		Auth: RouteAuthConfig{Required: true},
		TokenExchange: TokenExchangeConfig{
			Enabled:          true,
			ValidationMode:   "jwt",
			JWKSURL:          "https://example.com/.well-known/jwks.json",
			TrustedIssuers:   []string{"https://example.com"},
			Issuer:           "https://my-app.com",
			SigningAlgorithm: "RS256",
			SigningKey:        "my-rsa-key",
			TokenLifetime:    time.Hour,
		},
	}
	if err := l.validateTokenExchangeConfig("route r1", route); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateTokenExchangeConfig_IntrospectionMode_Valid(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:   "r1",
		Auth: RouteAuthConfig{Required: true},
		TokenExchange: TokenExchangeConfig{
			Enabled:          true,
			ValidationMode:   "introspection",
			IntrospectionURL: "https://example.com/introspect",
			ClientID:         "client-id",
			ClientSecret:     "client-secret",
			Issuer:           "https://my-app.com",
			SigningAlgorithm: "HS256",
			SigningSecret:    "c2VjcmV0",
			TokenLifetime:    30 * time.Minute,
		},
	}
	if err := l.validateTokenExchangeConfig("route r1", route); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateTokenExchangeConfig_Errors(t *testing.T) {
	baseRoute := func() RouteConfig {
		return RouteConfig{
			ID:   "r1",
			Auth: RouteAuthConfig{Required: true},
			TokenExchange: TokenExchangeConfig{
				Enabled:          true,
				ValidationMode:   "jwt",
				JWKSURL:          "https://example.com/.well-known/jwks.json",
				TrustedIssuers:   []string{"https://example.com"},
				Issuer:           "https://my-app.com",
				SigningAlgorithm: "RS256",
				SigningKey:        "my-rsa-key",
				TokenLifetime:    time.Hour,
			},
		}
	}

	tests := []struct {
		name    string
		modify  func(*RouteConfig)
		wantErr string
	}{
		{
			name: "invalid validation_mode",
			modify: func(r *RouteConfig) {
				r.TokenExchange.ValidationMode = "invalid"
			},
			wantErr: `token_exchange.validation_mode must be "jwt" or "introspection"`,
		},
		{
			name: "jwt mode missing jwks_url",
			modify: func(r *RouteConfig) {
				r.TokenExchange.JWKSURL = ""
			},
			wantErr: "token_exchange.jwks_url is required for jwt validation mode",
		},
		{
			name: "jwt mode missing trusted_issuers",
			modify: func(r *RouteConfig) {
				r.TokenExchange.TrustedIssuers = nil
			},
			wantErr: "token_exchange.trusted_issuers is required for jwt validation mode",
		},
		{
			name: "introspection mode missing introspection_url",
			modify: func(r *RouteConfig) {
				r.TokenExchange.ValidationMode = "introspection"
				r.TokenExchange.IntrospectionURL = ""
				r.TokenExchange.ClientID = "cid"
				r.TokenExchange.ClientSecret = "csec"
			},
			wantErr: "token_exchange.introspection_url is required for introspection mode",
		},
		{
			name: "introspection mode missing client_id",
			modify: func(r *RouteConfig) {
				r.TokenExchange.ValidationMode = "introspection"
				r.TokenExchange.IntrospectionURL = "https://example.com/introspect"
				r.TokenExchange.ClientID = ""
				r.TokenExchange.ClientSecret = "csec"
			},
			wantErr: "token_exchange.client_id is required for introspection mode",
		},
		{
			name: "introspection mode missing client_secret",
			modify: func(r *RouteConfig) {
				r.TokenExchange.ValidationMode = "introspection"
				r.TokenExchange.IntrospectionURL = "https://example.com/introspect"
				r.TokenExchange.ClientID = "cid"
				r.TokenExchange.ClientSecret = ""
			},
			wantErr: "token_exchange.client_secret is required for introspection mode",
		},
		{
			name: "missing issuer",
			modify: func(r *RouteConfig) {
				r.TokenExchange.Issuer = ""
			},
			wantErr: "token_exchange.issuer is required",
		},
		{
			name: "invalid signing_algorithm",
			modify: func(r *RouteConfig) {
				r.TokenExchange.SigningAlgorithm = "ES256"
			},
			wantErr: "token_exchange.signing_algorithm must be RS256, RS512, HS256, or HS512",
		},
		{
			name: "RS256 missing signing_key and signing_key_file",
			modify: func(r *RouteConfig) {
				r.TokenExchange.SigningAlgorithm = "RS256"
				r.TokenExchange.SigningKey = ""
				r.TokenExchange.SigningKeyFile = ""
			},
			wantErr: "token_exchange.signing_key or signing_key_file required for RS256",
		},
		{
			name: "RS512 missing signing_key and signing_key_file",
			modify: func(r *RouteConfig) {
				r.TokenExchange.SigningAlgorithm = "RS512"
				r.TokenExchange.SigningKey = ""
				r.TokenExchange.SigningKeyFile = ""
			},
			wantErr: "token_exchange.signing_key or signing_key_file required for RS512",
		},
		{
			name: "HS256 missing signing_secret",
			modify: func(r *RouteConfig) {
				r.TokenExchange.SigningAlgorithm = "HS256"
				r.TokenExchange.SigningSecret = ""
			},
			wantErr: "token_exchange.signing_secret required for HS256",
		},
		{
			name: "HS512 missing signing_secret",
			modify: func(r *RouteConfig) {
				r.TokenExchange.SigningAlgorithm = "HS512"
				r.TokenExchange.SigningSecret = ""
			},
			wantErr: "token_exchange.signing_secret required for HS512",
		},
		{
			name: "token_lifetime zero",
			modify: func(r *RouteConfig) {
				r.TokenExchange.TokenLifetime = 0
			},
			wantErr: "token_exchange.token_lifetime must be > 0",
		},
		{
			name: "token_lifetime negative",
			modify: func(r *RouteConfig) {
				r.TokenExchange.TokenLifetime = -1
			},
			wantErr: "token_exchange.token_lifetime must be > 0",
		},
	}

	l := NewLoader()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := baseRoute()
			tt.modify(&route)
			err := l.validateTokenExchangeConfig("route r1", route)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateTokenExchangeConfig_RS256WithKeyFile(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:   "r1",
		Auth: RouteAuthConfig{Required: true},
		TokenExchange: TokenExchangeConfig{
			Enabled:          true,
			ValidationMode:   "jwt",
			JWKSURL:          "https://example.com/.well-known/jwks.json",
			TrustedIssuers:   []string{"https://example.com"},
			Issuer:           "https://my-app.com",
			SigningAlgorithm: "RS256",
			SigningKeyFile:   "/path/to/key.pem",
			TokenLifetime:    time.Hour,
		},
	}
	if err := l.validateTokenExchangeConfig("route r1", route); err != nil {
		t.Fatalf("expected no error for RS256 with signing_key_file, got: %v", err)
	}
}

func TestValidateTokenExchangeConfig_HS512Valid(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:   "r1",
		Auth: RouteAuthConfig{Required: true},
		TokenExchange: TokenExchangeConfig{
			Enabled:          true,
			ValidationMode:   "introspection",
			IntrospectionURL: "https://example.com/introspect",
			ClientID:         "cid",
			ClientSecret:     "csec",
			Issuer:           "https://my-app.com",
			SigningAlgorithm: "HS512",
			SigningSecret:    "c2VjcmV0",
			TokenLifetime:    5 * time.Minute,
		},
	}
	if err := l.validateTokenExchangeConfig("route r1", route); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// --- validateAICrawlConfig ---

func TestValidateAICrawlConfig_EmptyDefaults(t *testing.T) {
	l := NewLoader()
	cfg := AICrawlConfig{Enabled: true}
	if err := l.validateAICrawlConfig("global", cfg); err != nil {
		t.Fatalf("expected no error for empty defaults, got: %v", err)
	}
}

func TestValidateAICrawlConfig_ValidFullConfig(t *testing.T) {
	l := NewLoader()
	cfg := AICrawlConfig{
		Enabled:       true,
		DefaultAction: "block",
		BlockStatus:   403,
		Policies: []AICrawlPolicyConfig{
			{Crawler: "GPTBot", Action: "block"},
			{Crawler: "Google-Extended", Action: "allow", AllowPaths: []string{"/public"}},
		},
		CustomCrawlers: []CustomCrawlerConfig{
			{Name: "my-bot", Pattern: "MyBot/[0-9]+"},
		},
	}
	if err := l.validateAICrawlConfig("global", cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateAICrawlConfig_Errors(t *testing.T) {
	tests := []struct {
		name    string
		cfg     AICrawlConfig
		wantErr string
	}{
		{
			name: "invalid default_action",
			cfg: AICrawlConfig{
				Enabled:       true,
				DefaultAction: "reject",
			},
			wantErr: "ai_crawl_control.default_action must be monitor, allow, or block",
		},
		{
			name: "block_status below range",
			cfg: AICrawlConfig{
				Enabled:     true,
				BlockStatus: 50,
			},
			wantErr: "ai_crawl_control.block_status must be 100-599",
		},
		{
			name: "block_status above range",
			cfg: AICrawlConfig{
				Enabled:     true,
				BlockStatus: 600,
			},
			wantErr: "ai_crawl_control.block_status must be 100-599",
		},
		{
			name: "policy missing crawler",
			cfg: AICrawlConfig{
				Enabled:  true,
				Policies: []AICrawlPolicyConfig{{Action: "block"}},
			},
			wantErr: "ai_crawl_control.policies[0].crawler is required",
		},
		{
			name: "policy missing action",
			cfg: AICrawlConfig{
				Enabled:  true,
				Policies: []AICrawlPolicyConfig{{Crawler: "GPTBot"}},
			},
			wantErr: "ai_crawl_control.policies[0].action is required",
		},
		{
			name: "policy invalid action",
			cfg: AICrawlConfig{
				Enabled:  true,
				Policies: []AICrawlPolicyConfig{{Crawler: "GPTBot", Action: "reject"}},
			},
			wantErr: "ai_crawl_control.policies[0].action must be monitor, allow, or block",
		},
		{
			name: "policy allow_paths and disallow_paths together",
			cfg: AICrawlConfig{
				Enabled: true,
				Policies: []AICrawlPolicyConfig{{
					Crawler:       "GPTBot",
					Action:        "block",
					AllowPaths:    []string{"/public"},
					DisallowPaths: []string{"/private"},
				}},
			},
			wantErr: "allow_paths and disallow_paths are mutually exclusive",
		},
		{
			name: "custom crawler missing name",
			cfg: AICrawlConfig{
				Enabled:        true,
				CustomCrawlers: []CustomCrawlerConfig{{Pattern: "Bot/.*"}},
			},
			wantErr: "ai_crawl_control.custom_crawlers[0].name is required",
		},
		{
			name: "custom crawler missing pattern",
			cfg: AICrawlConfig{
				Enabled:        true,
				CustomCrawlers: []CustomCrawlerConfig{{Name: "my-bot"}},
			},
			wantErr: "ai_crawl_control.custom_crawlers[0].pattern is required",
		},
		{
			name: "custom crawler invalid regex",
			cfg: AICrawlConfig{
				Enabled:        true,
				CustomCrawlers: []CustomCrawlerConfig{{Name: "my-bot", Pattern: "[invalid"}},
			},
			wantErr: "invalid regex",
		},
		{
			name: "custom crawler duplicate name",
			cfg: AICrawlConfig{
				Enabled: true,
				CustomCrawlers: []CustomCrawlerConfig{
					{Name: "bot-a", Pattern: "BotA/.*"},
					{Name: "bot-a", Pattern: "BotA2/.*"},
				},
			},
			wantErr: `duplicate name "bot-a"`,
		},
	}

	l := NewLoader()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateAICrawlConfig("global", tt.cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateAICrawlConfig_ValidDefaultActions(t *testing.T) {
	l := NewLoader()
	for _, action := range []string{"", "monitor", "allow", "block"} {
		t.Run("default_action_"+action, func(t *testing.T) {
			cfg := AICrawlConfig{Enabled: true, DefaultAction: action}
			if err := l.validateAICrawlConfig("global", cfg); err != nil {
				t.Errorf("expected no error for default_action=%q, got: %v", action, err)
			}
		})
	}
}

func TestValidateAICrawlConfig_BlockStatusBoundaries(t *testing.T) {
	l := NewLoader()
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"zero is valid (default)", 0, false},
		{"100 is valid", 100, false},
		{"599 is valid", 599, false},
		{"99 is invalid", 99, true},
		{"600 is invalid", 600, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := AICrawlConfig{Enabled: true, BlockStatus: tt.status}
			err := l.validateAICrawlConfig("global", cfg)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

// --- validateBaggageConfig ---

func TestValidateBaggageConfig_ValidWithTags(t *testing.T) {
	l := NewLoader()
	cfg := BaggageConfig{
		Enabled: true,
		Tags: []BaggageTagDef{
			{Name: "tenant", Source: "header:X-Tenant-ID", Header: "X-Baggage-Tenant"},
		},
	}
	globalCfg := &Config{}
	if err := l.validateBaggageConfig("global", cfg, globalCfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateBaggageConfig_PropagateTraceRequiresTracing(t *testing.T) {
	l := NewLoader()
	cfg := BaggageConfig{
		Enabled:        true,
		PropagateTrace: true,
	}
	globalCfg := &Config{Tracing: TracingConfig{Enabled: false}}
	err := l.validateBaggageConfig("global", cfg, globalCfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "baggage.propagate_trace requires tracing.enabled") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBaggageConfig_PropagateTraceValidWhenTracingEnabled(t *testing.T) {
	l := NewLoader()
	cfg := BaggageConfig{
		Enabled:        true,
		PropagateTrace: true,
	}
	globalCfg := &Config{Tracing: TracingConfig{Enabled: true}}
	if err := l.validateBaggageConfig("global", cfg, globalCfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateBaggageConfig_RequiresTagsOrPropagateTrace(t *testing.T) {
	l := NewLoader()
	cfg := BaggageConfig{
		Enabled: true,
		// No tags, no propagate_trace
	}
	globalCfg := &Config{}
	err := l.validateBaggageConfig("global", cfg, globalCfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "baggage requires at least one tag or propagate_trace") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBaggageConfig_W3CBaggageRequiresTags(t *testing.T) {
	l := NewLoader()
	cfg := BaggageConfig{
		Enabled:        true,
		W3CBaggage:     true,
		PropagateTrace: true, // so it passes the "at least one tag or propagate_trace" check
	}
	globalCfg := &Config{Tracing: TracingConfig{Enabled: true}}
	err := l.validateBaggageConfig("global", cfg, globalCfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "baggage.w3c_baggage requires at least one tag") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBaggageConfig_TagErrors(t *testing.T) {
	tests := []struct {
		name    string
		cfg     BaggageConfig
		global  *Config
		wantErr string
	}{
		{
			name: "tag missing name",
			cfg: BaggageConfig{
				Enabled: true,
				Tags:    []BaggageTagDef{{Source: "header:X-Foo", Header: "X-Out"}},
			},
			global:  &Config{},
			wantErr: "baggage.tags[0].name is required",
		},
		{
			name: "tag missing header when w3c_baggage off",
			cfg: BaggageConfig{
				Enabled: true,
				Tags:    []BaggageTagDef{{Name: "foo", Source: "header:X-Foo"}},
			},
			global:  &Config{},
			wantErr: "baggage.tags[0].header is required",
		},
		{
			name: "tag invalid source prefix",
			cfg: BaggageConfig{
				Enabled: true,
				Tags:    []BaggageTagDef{{Name: "foo", Source: "env:MY_VAR", Header: "X-Out"}},
			},
			global:  &Config{},
			wantErr: "baggage.tags[0].source must start with header:, jwt_claim:, query:, cookie:, or static:",
		},
	}

	l := NewLoader()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.validateBaggageConfig("global", tt.cfg, tt.global)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateBaggageConfig_ValidSourcePrefixes(t *testing.T) {
	l := NewLoader()
	validSources := []string{
		"header:X-Custom",
		"jwt_claim:sub",
		"query:tenant",
		"cookie:session",
		"static:my-value",
	}
	for _, src := range validSources {
		t.Run("source_"+src, func(t *testing.T) {
			cfg := BaggageConfig{
				Enabled: true,
				Tags:    []BaggageTagDef{{Name: "tag1", Source: src, Header: "X-Out"}},
			}
			if err := l.validateBaggageConfig("global", cfg, &Config{}); err != nil {
				t.Errorf("expected no error for source %q, got: %v", src, err)
			}
		})
	}
}

func TestValidateBaggageConfig_W3CBaggageKeyValidation(t *testing.T) {
	l := NewLoader()

	t.Run("valid w3c keys", func(t *testing.T) {
		cfg := BaggageConfig{
			Enabled:    true,
			W3CBaggage: true,
			Tags: []BaggageTagDef{
				{Name: "tenant-id", Source: "header:X-Tenant", Header: "X-Out"},
				{Name: "request_type", Source: "header:X-Type", Header: "X-Out2"},
			},
		}
		if err := l.validateBaggageConfig("global", cfg, &Config{}); err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
	})

	t.Run("w3c key with spaces rejected", func(t *testing.T) {
		cfg := BaggageConfig{
			Enabled:    true,
			W3CBaggage: true,
			Tags: []BaggageTagDef{
				{Name: "bad key", Source: "header:X-Foo", Header: "X-Out"},
			},
		}
		err := l.validateBaggageConfig("global", cfg, &Config{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "contains invalid characters") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("w3c key with comma rejected", func(t *testing.T) {
		cfg := BaggageConfig{
			Enabled:    true,
			W3CBaggage: true,
			Tags: []BaggageTagDef{
				{Name: "a,b", Source: "header:X-Foo", Header: "X-Out"},
			},
		}
		err := l.validateBaggageConfig("global", cfg, &Config{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "contains invalid characters") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("w3c key with semicolon rejected", func(t *testing.T) {
		cfg := BaggageConfig{
			Enabled:    true,
			W3CBaggage: true,
			Tags: []BaggageTagDef{
				{Name: "a;b", Source: "header:X-Foo", Header: "X-Out"},
			},
		}
		err := l.validateBaggageConfig("global", cfg, &Config{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "contains invalid characters") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("w3c key with equals rejected", func(t *testing.T) {
		cfg := BaggageConfig{
			Enabled:    true,
			W3CBaggage: true,
			Tags: []BaggageTagDef{
				{Name: "a=b", Source: "header:X-Foo", Header: "X-Out"},
			},
		}
		err := l.validateBaggageConfig("global", cfg, &Config{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "contains invalid characters") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("w3c key with double-quote rejected", func(t *testing.T) {
		cfg := BaggageConfig{
			Enabled:    true,
			W3CBaggage: true,
			Tags: []BaggageTagDef{
				{Name: `a"b`, Source: "header:X-Foo", Header: "X-Out"},
			},
		}
		err := l.validateBaggageConfig("global", cfg, &Config{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "contains invalid characters") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate w3c keys rejected", func(t *testing.T) {
		cfg := BaggageConfig{
			Enabled:    true,
			W3CBaggage: true,
			Tags: []BaggageTagDef{
				{Name: "tenant", Source: "header:X-Foo", Header: "X-Out1"},
				{Name: "tenant", Source: "header:X-Bar", Header: "X-Out2"},
			},
		}
		err := l.validateBaggageConfig("global", cfg, &Config{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "duplicate w3c baggage key") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("custom baggage_key used for w3c dedup", func(t *testing.T) {
		cfg := BaggageConfig{
			Enabled:    true,
			W3CBaggage: true,
			Tags: []BaggageTagDef{
				{Name: "tenant", Source: "header:X-Foo", Header: "X-Out1", BaggageKey: "custom-key"},
				{Name: "tenant2", Source: "header:X-Bar", Header: "X-Out2", BaggageKey: "custom-key"},
			},
		}
		err := l.validateBaggageConfig("global", cfg, &Config{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), `duplicate w3c baggage key "custom-key"`) {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("w3c header not required when w3c_baggage on", func(t *testing.T) {
		cfg := BaggageConfig{
			Enabled:    true,
			W3CBaggage: true,
			Tags: []BaggageTagDef{
				{Name: "tenant", Source: "header:X-Foo"}, // no Header field
			},
		}
		if err := l.validateBaggageConfig("global", cfg, &Config{}); err != nil {
			t.Fatalf("expected no error (header not required with w3c_baggage), got: %v", err)
		}
	})
}

func TestValidateBaggageConfig_ScopeInErrorMessage(t *testing.T) {
	l := NewLoader()
	cfg := BaggageConfig{Enabled: true}
	err := l.validateBaggageConfig("route my-route", cfg, &Config{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "route my-route") {
		t.Errorf("expected scope in error, got: %v", err)
	}
}

// --- validatePassthroughExclusions: verify route ID in error ---

func TestValidatePassthroughExclusions_RouteIDInError(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:          "my-custom-route",
		Passthrough: true,
		Validation:  ValidationConfig{Enabled: true},
	}
	err := l.validatePassthroughExclusions(route, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "my-custom-route") {
		t.Errorf("expected route ID in error, got: %v", err)
	}
}

// --- validateOutlierDetection: verify route ID in error ---

func TestValidateOutlierDetection_RouteIDInError(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID: "my-route",
		OutlierDetection: OutlierDetectionConfig{
			Enabled:  true,
			Interval: -1,
		},
	}
	err := l.validateOutlierDetection(route, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "my-route") {
		t.Errorf("expected route ID in error, got: %v", err)
	}
}

// --- validateTokenExchangeConfig: scope in error ---

func TestValidateTokenExchangeConfig_ScopeInError(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:            "r1",
		Auth:          RouteAuthConfig{Required: false},
		TokenExchange: TokenExchangeConfig{Enabled: true},
	}
	err := l.validateTokenExchangeConfig("route my-exchange-route", route)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "route my-exchange-route") {
		t.Errorf("expected scope in error, got: %v", err)
	}
}

// --- validateAICrawlConfig: scope in error ---

func TestValidateAICrawlConfig_ScopeInError(t *testing.T) {
	l := NewLoader()
	cfg := AICrawlConfig{
		Enabled:       true,
		DefaultAction: "invalid",
	}
	err := l.validateAICrawlConfig("route my-crawl-route", cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "route my-crawl-route") {
		t.Errorf("expected scope in error, got: %v", err)
	}
}

// --- validateAICrawlConfig: valid policy actions ---

func TestValidateAICrawlConfig_ValidPolicyActions(t *testing.T) {
	l := NewLoader()
	for _, action := range []string{"monitor", "allow", "block"} {
		t.Run("policy_action_"+action, func(t *testing.T) {
			cfg := AICrawlConfig{
				Enabled: true,
				Policies: []AICrawlPolicyConfig{
					{Crawler: "GPTBot", Action: action},
				},
			}
			if err := l.validateAICrawlConfig("global", cfg); err != nil {
				t.Errorf("expected no error for action=%q, got: %v", action, err)
			}
		})
	}
}

// --- validateAICrawlConfig: policies with only allow_paths or disallow_paths ---

func TestValidateAICrawlConfig_PolicyWithOnlyAllowPaths(t *testing.T) {
	l := NewLoader()
	cfg := AICrawlConfig{
		Enabled: true,
		Policies: []AICrawlPolicyConfig{
			{Crawler: "GPTBot", Action: "block", AllowPaths: []string{"/public/**"}},
		},
	}
	if err := l.validateAICrawlConfig("global", cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateAICrawlConfig_PolicyWithOnlyDisallowPaths(t *testing.T) {
	l := NewLoader()
	cfg := AICrawlConfig{
		Enabled: true,
		Policies: []AICrawlPolicyConfig{
			{Crawler: "GPTBot", Action: "block", DisallowPaths: []string{"/private/**"}},
		},
	}
	if err := l.validateAICrawlConfig("global", cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// --- validateAICrawlConfig: multiple custom crawlers with unique names ---

func TestValidateAICrawlConfig_MultipleUniqueCustomCrawlers(t *testing.T) {
	l := NewLoader()
	cfg := AICrawlConfig{
		Enabled: true,
		CustomCrawlers: []CustomCrawlerConfig{
			{Name: "bot-a", Pattern: "BotA/.*"},
			{Name: "bot-b", Pattern: "BotB/.*"},
			{Name: "bot-c", Pattern: "BotC/.*"},
		},
	}
	if err := l.validateAICrawlConfig("global", cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// --- validateBaggageConfig: multiple valid tags ---

func TestValidateBaggageConfig_MultipleValidTags(t *testing.T) {
	l := NewLoader()
	cfg := BaggageConfig{
		Enabled: true,
		Tags: []BaggageTagDef{
			{Name: "tenant", Source: "header:X-Tenant-ID", Header: "X-Baggage-Tenant"},
			{Name: "user", Source: "jwt_claim:sub", Header: "X-Baggage-User"},
			{Name: "region", Source: "query:region", Header: "X-Baggage-Region"},
			{Name: "session", Source: "cookie:sid", Header: "X-Baggage-Session"},
			{Name: "env", Source: "static:production", Header: "X-Baggage-Env"},
		},
	}
	if err := l.validateBaggageConfig("global", cfg, &Config{}); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// --- validateTokenExchangeConfig: empty signing algorithm ---

func TestValidateTokenExchangeConfig_EmptySigningAlgorithm(t *testing.T) {
	l := NewLoader()
	route := RouteConfig{
		ID:   "r1",
		Auth: RouteAuthConfig{Required: true},
		TokenExchange: TokenExchangeConfig{
			Enabled:          true,
			ValidationMode:   "jwt",
			JWKSURL:          "https://example.com/jwks",
			TrustedIssuers:   []string{"https://example.com"},
			Issuer:           "https://my-app.com",
			SigningAlgorithm: "",
			TokenLifetime:    time.Hour,
		},
	}
	err := l.validateTokenExchangeConfig("route r1", route)
	if err == nil {
		t.Fatal("expected error for empty signing_algorithm, got nil")
	}
	if !strings.Contains(err.Error(), "token_exchange.signing_algorithm must be RS256, RS512, HS256, or HS512") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- validateOutlierDetection: first error wins ---

func TestValidateOutlierDetection_FirstErrorReturned(t *testing.T) {
	l := NewLoader()
	// Both interval and window are negative; interval is checked first.
	route := RouteConfig{
		ID: "r1",
		OutlierDetection: OutlierDetectionConfig{
			Enabled:  true,
			Interval: -1,
			Window:   -1,
		},
	}
	err := l.validateOutlierDetection(route, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "interval") {
		t.Errorf("expected first error about interval, got: %v", err)
	}
}
