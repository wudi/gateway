package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/getkin/kin-openapi/openapi3"
)

// validHTTPMethods contains all valid HTTP method names.
var validHTTPMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true,
	"DELETE": true, "PATCH": true, "OPTIONS": true,
}

// Loader handles configuration loading and parsing
type Loader struct {
	envPattern *regexp.Regexp
}

// NewLoader creates a new configuration loader
func NewLoader() *Loader {
	return &Loader{
		envPattern: regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`),
	}
}

// Load reads and parses a configuration file
func (l *Loader) Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	return l.Parse(data)
}

// Parse parses configuration from YAML bytes
func (l *Loader) Parse(data []byte) (*Config, error) {
	// Expand environment variables
	expanded := l.expandEnvVars(string(data))

	// Start with defaults
	cfg := DefaultConfig()

	// Unmarshal YAML into config
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Expand OpenAPI spec routes before validation
	if err := expandOpenAPIRoutes(cfg); err != nil {
		return nil, fmt.Errorf("openapi route expansion: %w", err)
	}

	// Validate configuration
	if err := l.validate(cfg); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// expandEnvVars replaces ${VAR_NAME} with environment variable values
func (l *Loader) expandEnvVars(input string) string {
	return l.envPattern.ReplaceAllStringFunc(input, func(match string) string {
		// Extract variable name from ${VAR_NAME}
		varName := strings.TrimPrefix(strings.TrimSuffix(match, "}"), "${")
		if value, exists := os.LookupEnv(varName); exists {
			return value
		}
		return match // Keep original if env var not set
	})
}

// validate checks configuration for errors
func (l *Loader) validate(cfg *Config) error {
	// Validate that at least one listener is configured
	if len(cfg.Listeners) == 0 {
		return fmt.Errorf("at least one listener is required")
	}

	// Validate registry type
	validTypes := map[string]bool{
		"consul":     true,
		"etcd":       true,
		"kubernetes": true,
		"memory":     true,
	}
	if cfg.Registry.Type != "" && !validTypes[cfg.Registry.Type] {
		return fmt.Errorf("invalid registry type: %s", cfg.Registry.Type)
	}

	// Validate listeners
	listenerIDs := make(map[string]bool)
	for i, listener := range cfg.Listeners {
		if listener.ID == "" {
			return fmt.Errorf("listener %d: id is required", i)
		}
		if listenerIDs[listener.ID] {
			return fmt.Errorf("duplicate listener id: %s", listener.ID)
		}
		listenerIDs[listener.ID] = true

		if listener.Address == "" {
			return fmt.Errorf("listener %s: address is required", listener.ID)
		}

		// Validate protocol
		validProtocols := map[Protocol]bool{
			ProtocolHTTP: true,
			ProtocolTCP:  true,
			ProtocolUDP:  true,
		}
		if listener.Protocol == "" {
			return fmt.Errorf("listener %s: protocol is required", listener.ID)
		}
		if !validProtocols[listener.Protocol] {
			return fmt.Errorf("listener %s: invalid protocol: %s", listener.ID, listener.Protocol)
		}

		// Validate TLS config if enabled
		if listener.TLS.Enabled {
			if listener.TLS.CertFile == "" {
				return fmt.Errorf("listener %s: TLS enabled but cert_file not provided", listener.ID)
			}
			if listener.TLS.KeyFile == "" {
				return fmt.Errorf("listener %s: TLS enabled but key_file not provided", listener.ID)
			}
		}
	}

	// Validate routes
	routeIDs := make(map[string]bool)
	for i, route := range cfg.Routes {
		if route.ID == "" {
			return fmt.Errorf("route %d: id is required", i)
		}
		if routeIDs[route.ID] {
			return fmt.Errorf("duplicate route id: %s", route.ID)
		}
		routeIDs[route.ID] = true

		if route.Path == "" {
			return fmt.Errorf("route %s: path is required", route.ID)
		}

		// Must have either backends, service discovery, or versioning
		if len(route.Backends) == 0 && route.Service.Name == "" && !route.Versioning.Enabled {
			return fmt.Errorf("route %s: must have either backends or service name", route.ID)
		}

		// Validate match config
		if err := l.validateMatchConfig(route.ID, route.Match); err != nil {
			return err
		}
	}

	// Validate TCP routes
	tcpRouteIDs := make(map[string]bool)
	for i, route := range cfg.TCPRoutes {
		if route.ID == "" {
			return fmt.Errorf("tcp_route %d: id is required", i)
		}
		if tcpRouteIDs[route.ID] {
			return fmt.Errorf("duplicate tcp_route id: %s", route.ID)
		}
		tcpRouteIDs[route.ID] = true

		if route.Listener == "" {
			return fmt.Errorf("tcp_route %s: listener is required", route.ID)
		}
		if !listenerIDs[route.Listener] {
			return fmt.Errorf("tcp_route %s: references unknown listener: %s", route.ID, route.Listener)
		}

		if len(route.Backends) == 0 {
			return fmt.Errorf("tcp_route %s: at least one backend is required", route.ID)
		}

		// Validate source CIDR format if specified
		if len(route.Match.SourceCIDR) > 0 {
			if _, err := route.Match.ParsedSourceCIDRs(); err != nil {
				return fmt.Errorf("tcp_route %s: invalid source_cidr: %w", route.ID, err)
			}
		}
	}

	// Validate UDP routes
	udpRouteIDs := make(map[string]bool)
	for i, route := range cfg.UDPRoutes {
		if route.ID == "" {
			return fmt.Errorf("udp_route %d: id is required", i)
		}
		if udpRouteIDs[route.ID] {
			return fmt.Errorf("duplicate udp_route id: %s", route.ID)
		}
		udpRouteIDs[route.ID] = true

		if route.Listener == "" {
			return fmt.Errorf("udp_route %s: listener is required", route.ID)
		}
		if !listenerIDs[route.Listener] {
			return fmt.Errorf("udp_route %s: references unknown listener: %s", route.ID, route.Listener)
		}

		if len(route.Backends) == 0 {
			return fmt.Errorf("udp_route %s: at least one backend is required", route.ID)
		}
	}

	// Validate JWT config if enabled
	if cfg.Authentication.JWT.Enabled {
		if cfg.Authentication.JWT.Secret == "" && cfg.Authentication.JWT.PublicKey == "" && cfg.Authentication.JWT.JWKSURL == "" {
			return fmt.Errorf("JWT authentication enabled but no secret, public key, or JWKS URL provided")
		}
	}

	// Validate distributed rate limiting requires Redis
	for _, route := range cfg.Routes {
		if route.RateLimit.Mode == "distributed" && cfg.Redis.Address == "" {
			return fmt.Errorf("route %s: distributed rate limiting requires redis.address to be configured", route.ID)
		}
	}

	// Validate rate limit algorithm
	for _, route := range cfg.Routes {
		switch route.RateLimit.Algorithm {
		case "", "token_bucket", "sliding_window":
			// valid
		default:
			return fmt.Errorf("route %s: invalid rate_limit.algorithm %q (must be \"token_bucket\" or \"sliding_window\")", route.ID, route.RateLimit.Algorithm)
		}
		if route.RateLimit.Algorithm == "sliding_window" && route.RateLimit.Mode == "distributed" {
			return fmt.Errorf("route %s: algorithm \"sliding_window\" is incompatible with mode \"distributed\" (distributed already uses a sliding window)", route.ID)
		}
	}

	// Validate global rules
	if err := l.validateRules(cfg.Rules.Request, "request"); err != nil {
		return fmt.Errorf("global rules: %w", err)
	}
	if err := l.validateRules(cfg.Rules.Response, "response"); err != nil {
		return fmt.Errorf("global rules: %w", err)
	}

	// Validate per-route rules
	for _, route := range cfg.Routes {
		if err := l.validateRules(route.Rules.Request, "request"); err != nil {
			return fmt.Errorf("route %s rules: %w", route.ID, err)
		}
		if err := l.validateRules(route.Rules.Response, "response"); err != nil {
			return fmt.Errorf("route %s rules: %w", route.ID, err)
		}
	}

	// Validate global traffic shaping
	if err := l.validateTrafficShaping(cfg.TrafficShaping, "global"); err != nil {
		return err
	}

	// Validate per-route traffic shaping
	for _, route := range cfg.Routes {
		if err := l.validateTrafficShaping(route.TrafficShaping, fmt.Sprintf("route %s", route.ID)); err != nil {
			return err
		}
		if route.TrafficShaping.Priority.Enabled && !cfg.TrafficShaping.Priority.Enabled {
			return fmt.Errorf("route %s: per-route priority requires global priority to be enabled", route.ID)
		}
	}

	// Validate sticky + traffic_split + mirror conditions per route
	for _, route := range cfg.Routes {
		// Validate sticky config
		if route.Sticky.Enabled {
			validModes := map[string]bool{"cookie": true, "header": true, "hash": true}
			if route.Sticky.Mode == "" {
				return fmt.Errorf("route %s: sticky.mode is required when enabled", route.ID)
			}
			if !validModes[route.Sticky.Mode] {
				return fmt.Errorf("route %s: sticky.mode must be cookie, header, or hash", route.ID)
			}
			if len(route.TrafficSplit) == 0 {
				return fmt.Errorf("route %s: sticky requires traffic_split to be configured", route.ID)
			}
			if (route.Sticky.Mode == "header" || route.Sticky.Mode == "hash") && route.Sticky.HashKey == "" {
				return fmt.Errorf("route %s: sticky.hash_key is required for header/hash mode", route.ID)
			}
		}

		// Validate traffic_split weights sum to 100
		if len(route.TrafficSplit) > 0 {
			totalWeight := 0
			for _, split := range route.TrafficSplit {
				totalWeight += split.Weight
			}
			if totalWeight != 100 {
				return fmt.Errorf("route %s: traffic_split weights must sum to 100, got %d", route.ID, totalWeight)
			}
		}

		// Validate mirror conditions
		if route.Mirror.Enabled && route.Mirror.Conditions.PathRegex != "" {
			if _, err := regexp.Compile(route.Mirror.Conditions.PathRegex); err != nil {
				return fmt.Errorf("route %s: mirror conditions path_regex is invalid: %w", route.ID, err)
			}
		}
		if route.Mirror.Enabled && route.Mirror.Percentage != 0 {
			if route.Mirror.Percentage < 0 || route.Mirror.Percentage > 100 {
				return fmt.Errorf("route %s: mirror percentage must be between 0 and 100", route.ID)
			}
		}
	}

	// Validate CORS regex patterns
	for _, route := range cfg.Routes {
		for _, pattern := range route.CORS.AllowOriginPatterns {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("route %s: cors allow_origin_patterns: invalid regex %q: %w", route.ID, pattern, err)
			}
		}
	}

	// Validate WAF config
	if cfg.WAF.Enabled {
		if cfg.WAF.Mode != "" && cfg.WAF.Mode != "block" && cfg.WAF.Mode != "detect" {
			return fmt.Errorf("global WAF mode must be 'block' or 'detect'")
		}
	}
	for _, route := range cfg.Routes {
		if route.WAF.Enabled {
			if route.WAF.Mode != "" && route.WAF.Mode != "block" && route.WAF.Mode != "detect" {
				return fmt.Errorf("route %s: WAF mode must be 'block' or 'detect'", route.ID)
			}
		}
	}

	// Validate GraphQL config
	for _, route := range cfg.Routes {
		if route.GraphQL.Enabled {
			if route.GraphQL.MaxDepth < 0 {
				return fmt.Errorf("route %s: graphql max_depth must be >= 0", route.ID)
			}
			if route.GraphQL.MaxComplexity < 0 {
				return fmt.Errorf("route %s: graphql max_complexity must be >= 0", route.ID)
			}
			validOpTypes := map[string]bool{"query": true, "mutation": true, "subscription": true}
			for opType, limit := range route.GraphQL.OperationLimits {
				if !validOpTypes[opType] {
					return fmt.Errorf("route %s: graphql operation_limits key %q must be query, mutation, or subscription", route.ID, opType)
				}
				if limit <= 0 {
					return fmt.Errorf("route %s: graphql operation_limits value for %q must be > 0", route.ID, opType)
				}
			}
		}
	}

	// Validate coalesce config
	for _, route := range cfg.Routes {
		if route.Coalesce.Enabled {
			if route.Coalesce.Timeout < 0 {
				return fmt.Errorf("route %s: coalesce timeout must be >= 0", route.ID)
			}
			for _, m := range route.Coalesce.Methods {
				if !validHTTPMethods[m] {
					return fmt.Errorf("route %s: coalesce methods contains invalid HTTP method: %s", route.ID, m)
				}
			}
		}
	}

	// Validate canary config
	for _, route := range cfg.Routes {
		if route.Canary.Enabled {
			if len(route.TrafficSplit) == 0 {
				return fmt.Errorf("route %s: canary requires traffic_split to be configured", route.ID)
			}
			if route.Canary.CanaryGroup == "" {
				return fmt.Errorf("route %s: canary.canary_group is required", route.ID)
			}
			// Validate canary_group references an existing traffic split group
			groupFound := false
			for _, split := range route.TrafficSplit {
				if split.Name == route.Canary.CanaryGroup {
					groupFound = true
					break
				}
			}
			if !groupFound {
				return fmt.Errorf("route %s: canary.canary_group %q not found in traffic_split groups", route.ID, route.Canary.CanaryGroup)
			}
			if len(route.Canary.Steps) == 0 {
				return fmt.Errorf("route %s: canary requires at least one step", route.ID)
			}
			for i, step := range route.Canary.Steps {
				if step.Weight < 0 || step.Weight > 100 {
					return fmt.Errorf("route %s: canary step %d weight must be 0-100", route.ID, i)
				}
				if i > 0 && step.Weight < route.Canary.Steps[i-1].Weight {
					return fmt.Errorf("route %s: canary step weights must be monotonically non-decreasing", route.ID)
				}
			}
			if route.Canary.Analysis.ErrorThreshold < 0 || route.Canary.Analysis.ErrorThreshold > 1.0 {
				return fmt.Errorf("route %s: canary analysis error_threshold must be 0.0-1.0", route.ID)
			}
			if route.Canary.Analysis.Interval < 0 {
				return fmt.Errorf("route %s: canary analysis interval must be >= 0", route.ID)
			}
		}
	}

	// Validate DNS resolver config
	if len(cfg.DNSResolver.Nameservers) > 0 {
		for i, ns := range cfg.DNSResolver.Nameservers {
			if _, _, err := net.SplitHostPort(ns); err != nil {
				return fmt.Errorf("dns_resolver: nameserver %d (%q): must be host:port format: %w", i, ns, err)
			}
		}
	}
	if cfg.DNSResolver.Timeout < 0 {
		return fmt.Errorf("dns_resolver: timeout must be positive")
	}

	// Validate new route-level configs
	for _, route := range cfg.Routes {
		// Validate retry policy
		if route.RetryPolicy.MaxRetries > 0 {
			if route.RetryPolicy.BackoffMultiplier != 0 && route.RetryPolicy.BackoffMultiplier < 1.0 {
				return fmt.Errorf("route %s: retry_policy backoff_multiplier must be >= 1.0", route.ID)
			}
			for _, status := range route.RetryPolicy.RetryableStatuses {
				if status < 100 || status > 599 {
					return fmt.Errorf("route %s: retry_policy contains invalid HTTP status code: %d", route.ID, status)
				}
			}
		}

		// Validate retry budget
		if route.RetryPolicy.Budget.Ratio > 0 {
			if route.RetryPolicy.Budget.Ratio > 1.0 {
				return fmt.Errorf("route %s: retry_policy budget ratio must be between 0.0 and 1.0", route.ID)
			}
			if route.RetryPolicy.Budget.MinRetries < 0 {
				return fmt.Errorf("route %s: retry_policy budget min_retries must be >= 0", route.ID)
			}
			if route.RetryPolicy.Budget.Window < 0 {
				return fmt.Errorf("route %s: retry_policy budget window must be > 0", route.ID)
			}
		}

		// Validate hedging config
		if route.RetryPolicy.Hedging.Enabled {
			if route.RetryPolicy.Hedging.MaxRequests < 2 {
				return fmt.Errorf("route %s: retry_policy hedging max_requests must be >= 2", route.ID)
			}
			// Hedging and retries are mutually exclusive
			if route.RetryPolicy.MaxRetries > 0 {
				return fmt.Errorf("route %s: retry_policy cannot use both hedging and max_retries", route.ID)
			}
		}

		// Validate circuit breaker
		if route.CircuitBreaker.Enabled {
			if route.CircuitBreaker.FailureThreshold != 0 && route.CircuitBreaker.FailureThreshold < 1 {
				return fmt.Errorf("route %s: circuit_breaker failure_threshold must be > 0", route.ID)
			}
			if route.CircuitBreaker.MaxRequests != 0 && route.CircuitBreaker.MaxRequests < 1 {
				return fmt.Errorf("route %s: circuit_breaker max_requests must be > 0", route.ID)
			}
			if route.CircuitBreaker.Timeout != 0 && route.CircuitBreaker.Timeout < 0 {
				return fmt.Errorf("route %s: circuit_breaker timeout must be > 0", route.ID)
			}
		}

		// Validate cache
		if route.Cache.Enabled {
			if route.Cache.TTL != 0 && route.Cache.TTL < 0 {
				return fmt.Errorf("route %s: cache ttl must be > 0", route.ID)
			}
			if route.Cache.MaxSize != 0 && route.Cache.MaxSize < 1 {
				return fmt.Errorf("route %s: cache max_size must be > 0", route.ID)
			}
			if route.Cache.Mode != "" && route.Cache.Mode != "local" && route.Cache.Mode != "distributed" {
				return fmt.Errorf("route %s: cache mode must be \"local\" or \"distributed\"", route.ID)
			}
			if route.Cache.Mode == "distributed" && cfg.Redis.Address == "" {
				return fmt.Errorf("route %s: distributed cache requires redis.address to be configured", route.ID)
			}
		}

		// Validate websocket
		if route.WebSocket.Enabled {
			if route.WebSocket.ReadBufferSize != 0 && route.WebSocket.ReadBufferSize < 1 {
				return fmt.Errorf("route %s: websocket read_buffer_size must be > 0", route.ID)
			}
			if route.WebSocket.WriteBufferSize != 0 && route.WebSocket.WriteBufferSize < 1 {
				return fmt.Errorf("route %s: websocket write_buffer_size must be > 0", route.ID)
			}
		}

		// Validate load balancer algorithm
		if route.LoadBalancer != "" {
			validLBs := map[string]bool{
				"round_robin":         true,
				"least_conn":          true,
				"consistent_hash":     true,
				"least_response_time": true,
			}
			if !validLBs[route.LoadBalancer] {
				return fmt.Errorf("route %s: load_balancer must be round_robin, least_conn, consistent_hash, or least_response_time", route.ID)
			}
			// consistent_hash requires key config
			if route.LoadBalancer == "consistent_hash" {
				validKeys := map[string]bool{"header": true, "cookie": true, "path": true, "ip": true}
				if !validKeys[route.ConsistentHash.Key] {
					return fmt.Errorf("route %s: consistent_hash.key must be header, cookie, path, or ip", route.ID)
				}
				if (route.ConsistentHash.Key == "header" || route.ConsistentHash.Key == "cookie") && route.ConsistentHash.HeaderName == "" {
					return fmt.Errorf("route %s: consistent_hash.header_name is required for header/cookie key mode", route.ID)
				}
			}
			// least_conn, consistent_hash, least_response_time are incompatible with traffic_split
			if route.LoadBalancer != "round_robin" && len(route.TrafficSplit) > 0 {
				return fmt.Errorf("route %s: load_balancer %s is incompatible with traffic_split", route.ID, route.LoadBalancer)
			}
		}

		// Validate protocol translation config
		if route.Protocol.Type != "" {
			validProtocolTypes := map[string]bool{"http_to_grpc": true}
			if !validProtocolTypes[route.Protocol.Type] {
				return fmt.Errorf("route %s: unknown protocol type: %s", route.ID, route.Protocol.Type)
			}
			// Protocol translation and gRPC passthrough are mutually exclusive
			if route.GRPC.Enabled {
				return fmt.Errorf("route %s: cannot enable both grpc.enabled and protocol translation", route.ID)
			}
			// Validate TLS config if enabled
			if route.Protocol.GRPC.TLS.Enabled {
				if route.Protocol.GRPC.TLS.CAFile == "" {
					return fmt.Errorf("route %s: protocol grpc tls enabled but ca_file not provided", route.ID)
				}
			}
			// Validate REST-to-gRPC mappings
			if err := l.validateGRPCMappings(route.ID, route.Protocol.GRPC); err != nil {
				return err
			}
		}

		// Validate ext_auth config
		if route.ExtAuth.Enabled {
			if route.ExtAuth.URL == "" {
				return fmt.Errorf("route %s: ext_auth.url is required when enabled", route.ID)
			}
			if !strings.HasPrefix(route.ExtAuth.URL, "http://") &&
				!strings.HasPrefix(route.ExtAuth.URL, "https://") &&
				!strings.HasPrefix(route.ExtAuth.URL, "grpc://") {
				return fmt.Errorf("route %s: ext_auth.url must start with http://, https://, or grpc://", route.ID)
			}
			if route.ExtAuth.Timeout < 0 {
				return fmt.Errorf("route %s: ext_auth.timeout must be >= 0", route.ID)
			}
			if route.ExtAuth.CacheTTL < 0 {
				return fmt.Errorf("route %s: ext_auth.cache_ttl must be >= 0", route.ID)
			}
			if route.ExtAuth.TLS.Enabled && strings.HasPrefix(route.ExtAuth.URL, "http://") {
				return fmt.Errorf("route %s: ext_auth.tls cannot be used with http:// URL", route.ID)
			}
		}

		// Validate versioning config
		if route.Versioning.Enabled {
			validSources := map[string]bool{"path": true, "header": true, "accept": true, "query": true}
			if !validSources[route.Versioning.Source] {
				return fmt.Errorf("route %s: versioning.source must be path, header, accept, or query", route.ID)
			}
			if len(route.Versioning.Versions) == 0 {
				return fmt.Errorf("route %s: versioning.versions must not be empty", route.ID)
			}
			if route.Versioning.DefaultVersion == "" {
				return fmt.Errorf("route %s: versioning.default_version is required", route.ID)
			}
			if _, ok := route.Versioning.Versions[route.Versioning.DefaultVersion]; !ok {
				return fmt.Errorf("route %s: versioning.default_version %q must exist in versions", route.ID, route.Versioning.DefaultVersion)
			}
			for ver, vcfg := range route.Versioning.Versions {
				if len(vcfg.Backends) == 0 {
					return fmt.Errorf("route %s: versioning.versions[%s] must have at least one backend", route.ID, ver)
				}
				if vcfg.Sunset != "" {
					if _, err := time.Parse("2006-01-02", vcfg.Sunset); err != nil {
						return fmt.Errorf("route %s: versioning.versions[%s].sunset must be YYYY-MM-DD format", route.ID, ver)
					}
				}
			}
			if len(route.TrafficSplit) > 0 {
				return fmt.Errorf("route %s: versioning and traffic_split are mutually exclusive", route.ID)
			}
			if len(route.Backends) > 0 {
				return fmt.Errorf("route %s: versioning and top-level backends are mutually exclusive", route.ID)
			}
		}

		// Validate body transforms
		if err := l.validateBodyTransform(route.ID, "request", route.Transform.Request.Body); err != nil {
			return err
		}
		if err := l.validateBodyTransform(route.ID, "response", route.Transform.Response.Body); err != nil {
			return err
		}

		// Validate access log config
		if err := l.validateAccessLog(route.ID, route.AccessLog); err != nil {
			return err
		}

		// Validate per-route OpenAPI config
		if route.OpenAPI.SpecFile != "" && route.OpenAPI.SpecID != "" {
			return fmt.Errorf("route %s: openapi spec_file and spec_id are mutually exclusive", route.ID)
		}
		if route.OpenAPI.SpecID != "" {
			// Validate spec_id references an existing spec
			found := false
			for _, s := range cfg.OpenAPI.Specs {
				if s.ID == route.OpenAPI.SpecID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("route %s: openapi.spec_id %q not found in openapi.specs", route.ID, route.OpenAPI.SpecID)
			}
		}

		// Validate enhanced validation: response_schema and response_schema_file mutually exclusive
		if route.Validation.ResponseSchema != "" && route.Validation.ResponseSchemaFile != "" {
			return fmt.Errorf("route %s: validation response_schema and response_schema_file are mutually exclusive", route.ID)
		}

		// Validate timeout policy
		if route.TimeoutPolicy.IsActive() {
			if route.TimeoutPolicy.Request < 0 {
				return fmt.Errorf("route %s: timeout_policy.request must be >= 0", route.ID)
			}
			if route.TimeoutPolicy.Idle < 0 {
				return fmt.Errorf("route %s: timeout_policy.idle must be >= 0", route.ID)
			}
			if route.TimeoutPolicy.Backend < 0 {
				return fmt.Errorf("route %s: timeout_policy.backend must be >= 0", route.ID)
			}
			if route.TimeoutPolicy.HeaderTimeout < 0 {
				return fmt.Errorf("route %s: timeout_policy.header_timeout must be >= 0", route.ID)
			}
			if route.TimeoutPolicy.Backend > 0 && route.TimeoutPolicy.Request > 0 && route.TimeoutPolicy.Backend > route.TimeoutPolicy.Request {
				return fmt.Errorf("route %s: timeout_policy.backend must be <= timeout_policy.request", route.ID)
			}
			if route.TimeoutPolicy.HeaderTimeout > 0 {
				limit := route.TimeoutPolicy.Backend
				if limit <= 0 {
					limit = route.TimeoutPolicy.Request
				}
				if limit > 0 && route.TimeoutPolicy.HeaderTimeout > limit {
					return fmt.Errorf("route %s: timeout_policy.header_timeout must be <= backend (or request) timeout", route.ID)
				}
			}
		}
	}

	return nil
}

// validateAccessLog validates access log config for a given route.
func (l *Loader) validateAccessLog(routeID string, cfg AccessLogConfig) error {
	if len(cfg.HeadersInclude) > 0 && len(cfg.HeadersExclude) > 0 {
		return fmt.Errorf("route %s: access_log headers_include and headers_exclude are mutually exclusive", routeID)
	}
	if cfg.Conditions.SampleRate < 0 || cfg.Conditions.SampleRate > 1.0 {
		return fmt.Errorf("route %s: access_log conditions.sample_rate must be between 0.0 and 1.0", routeID)
	}
	for _, sc := range cfg.Conditions.StatusCodes {
		if _, err := parseStatusRange(sc); err != nil {
			return fmt.Errorf("route %s: access_log conditions.status_codes: %w", routeID, err)
		}
	}
	for _, m := range cfg.Conditions.Methods {
		if !validHTTPMethods[m] {
			return fmt.Errorf("route %s: access_log conditions.methods contains invalid HTTP method: %s", routeID, m)
		}
	}
	if cfg.Body.Enabled && cfg.Body.MaxSize < 0 {
		return fmt.Errorf("route %s: access_log body.max_size must be >= 0", routeID)
	}
	return nil
}

// parseStatusRange validates a status range string like "4xx", "200", "200-299".
func parseStatusRange(s string) ([2]int, error) {
	s = strings.TrimSpace(s)
	// Pattern: Nxx (e.g. "4xx", "5xx")
	if len(s) == 3 && s[1] == 'x' && s[2] == 'x' {
		base := int(s[0]-'0') * 100
		if base < 100 || base > 500 {
			return [2]int{}, fmt.Errorf("invalid status range %q", s)
		}
		return [2]int{base, base + 99}, nil
	}
	// Pattern: N-M (e.g. "200-299")
	if parts := strings.SplitN(s, "-", 2); len(parts) == 2 {
		lo, err1 := strconv.Atoi(parts[0])
		hi, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || lo < 100 || hi > 599 || lo > hi {
			return [2]int{}, fmt.Errorf("invalid status range %q", s)
		}
		return [2]int{lo, hi}, nil
	}
	// Pattern: single code (e.g. "200")
	code, err := strconv.Atoi(s)
	if err != nil || code < 100 || code > 599 {
		return [2]int{}, fmt.Errorf("invalid status code %q", s)
	}
	return [2]int{code, code}, nil
}

// validateBodyTransform validates body transform config for a given route and phase.
func (l *Loader) validateBodyTransform(routeID, phase string, cfg BodyTransformConfig) error {
	if len(cfg.AllowFields) > 0 && len(cfg.DenyFields) > 0 {
		return fmt.Errorf("route %s: %s body transform cannot use both allow_fields and deny_fields", routeID, phase)
	}
	if cfg.Template != "" {
		funcMap := template.FuncMap{
			"json": func(v interface{}) (string, error) {
				b, err := json.Marshal(v)
				return string(b), err
			},
		}
		if _, err := template.New("body").Funcs(funcMap).Parse(cfg.Template); err != nil {
			return fmt.Errorf("route %s: %s body transform template is invalid: %w", routeID, phase, err)
		}
	}
	return nil
}

// validateTrafficShaping validates traffic shaping config for a given scope (global or route).
func (l *Loader) validateTrafficShaping(cfg TrafficShapingConfig, scope string) error {
	if cfg.Throttle.Enabled {
		if cfg.Throttle.Rate <= 0 {
			return fmt.Errorf("%s: throttle rate must be > 0 when enabled", scope)
		}
		if cfg.Throttle.Burst < 0 {
			return fmt.Errorf("%s: throttle burst must be >= 0", scope)
		}
	}
	if cfg.Bandwidth.Enabled {
		if cfg.Bandwidth.RequestRate < 0 {
			return fmt.Errorf("%s: bandwidth request_rate must be >= 0", scope)
		}
		if cfg.Bandwidth.ResponseRate < 0 {
			return fmt.Errorf("%s: bandwidth response_rate must be >= 0", scope)
		}
	}
	if cfg.Priority.Enabled {
		if cfg.Priority.MaxConcurrent <= 0 {
			return fmt.Errorf("%s: priority max_concurrent must be > 0 when enabled", scope)
		}
		if cfg.Priority.DefaultLevel != 0 && (cfg.Priority.DefaultLevel < 1 || cfg.Priority.DefaultLevel > 10) {
			return fmt.Errorf("%s: priority default_level must be between 1 and 10", scope)
		}
		for i, lvl := range cfg.Priority.Levels {
			if lvl.Level < 1 || lvl.Level > 10 {
				return fmt.Errorf("%s: priority level %d: level must be between 1 and 10", scope, i)
			}
		}
	}
	if cfg.FaultInjection.Enabled {
		if cfg.FaultInjection.Delay.Percentage < 0 || cfg.FaultInjection.Delay.Percentage > 100 {
			return fmt.Errorf("%s: fault_injection delay percentage must be between 0 and 100", scope)
		}
		if cfg.FaultInjection.Delay.Percentage > 0 && cfg.FaultInjection.Delay.Duration <= 0 {
			return fmt.Errorf("%s: fault_injection delay duration must be > 0 when percentage is set", scope)
		}
		if cfg.FaultInjection.Abort.Percentage < 0 || cfg.FaultInjection.Abort.Percentage > 100 {
			return fmt.Errorf("%s: fault_injection abort percentage must be between 0 and 100", scope)
		}
		if cfg.FaultInjection.Abort.Percentage > 0 && (cfg.FaultInjection.Abort.StatusCode < 100 || cfg.FaultInjection.Abort.StatusCode > 599) {
			return fmt.Errorf("%s: fault_injection abort status_code must be between 100 and 599", scope)
		}
	}
	if cfg.AdaptiveConcurrency.Enabled {
		if cfg.AdaptiveConcurrency.MinConcurrency < 0 {
			return fmt.Errorf("%s: adaptive_concurrency min_concurrency must be >= 0", scope)
		}
		if cfg.AdaptiveConcurrency.MaxConcurrency < 0 {
			return fmt.Errorf("%s: adaptive_concurrency max_concurrency must be >= 0", scope)
		}
		if cfg.AdaptiveConcurrency.MinConcurrency > 0 && cfg.AdaptiveConcurrency.MaxConcurrency > 0 &&
			cfg.AdaptiveConcurrency.MinConcurrency > cfg.AdaptiveConcurrency.MaxConcurrency {
			return fmt.Errorf("%s: adaptive_concurrency min_concurrency must be <= max_concurrency", scope)
		}
		if cfg.AdaptiveConcurrency.LatencyTolerance != 0 && cfg.AdaptiveConcurrency.LatencyTolerance < 1.0 {
			return fmt.Errorf("%s: adaptive_concurrency latency_tolerance must be >= 1.0", scope)
		}
		if cfg.AdaptiveConcurrency.SmoothingFactor != 0 && (cfg.AdaptiveConcurrency.SmoothingFactor <= 0 || cfg.AdaptiveConcurrency.SmoothingFactor >= 1) {
			return fmt.Errorf("%s: adaptive_concurrency smoothing_factor must be between 0 and 1 (exclusive)", scope)
		}
	}
	return nil
}

// validateRules validates a list of rule configs for a given phase.
func (l *Loader) validateRules(rules []RuleConfig, phase string) error {
	validActions := map[string]bool{
		"block":           true,
		"custom_response": true,
		"redirect":        true,
		"set_headers":     true,
		"rewrite":         true,
		"group":           true,
		"log":             true,
	}

	terminatingActions := map[string]bool{
		"block":           true,
		"custom_response": true,
		"redirect":        true,
	}

	requestOnlyActions := map[string]bool{
		"rewrite": true,
		"group":   true,
	}

	ids := make(map[string]bool)

	for i, rule := range rules {
		if rule.ID == "" {
			return fmt.Errorf("%s rule %d: id is required", phase, i)
		}
		if ids[rule.ID] {
			return fmt.Errorf("%s rule %s: duplicate id", phase, rule.ID)
		}
		ids[rule.ID] = true

		if rule.Expression == "" {
			return fmt.Errorf("%s rule %s: expression is required", phase, rule.ID)
		}

		if !validActions[rule.Action] {
			return fmt.Errorf("%s rule %s: invalid action %q (must be block, custom_response, redirect, set_headers, rewrite, group, or log)", phase, rule.ID, rule.Action)
		}

		// Response phase: reject terminating actions for now
		if phase == "response" && terminatingActions[rule.Action] {
			return fmt.Errorf("%s rule %s: terminating action %q is not allowed in response phase", phase, rule.ID, rule.Action)
		}

		// Response phase: reject request-only actions
		if phase == "response" && requestOnlyActions[rule.Action] {
			return fmt.Errorf("%s rule %s: action %q is only allowed in request phase", phase, rule.ID, rule.Action)
		}

		if rule.Action == "redirect" && rule.RedirectURL == "" {
			return fmt.Errorf("%s rule %s: redirect action requires redirect_url", phase, rule.ID)
		}

		if rule.StatusCode != 0 && (rule.StatusCode < 100 || rule.StatusCode > 599) {
			return fmt.Errorf("%s rule %s: invalid status_code %d", phase, rule.ID, rule.StatusCode)
		}

		if rule.Action == "set_headers" {
			if len(rule.Headers.Add) == 0 && len(rule.Headers.Set) == 0 && len(rule.Headers.Remove) == 0 {
				return fmt.Errorf("%s rule %s: set_headers action requires at least one header operation", phase, rule.ID)
			}
		}

		if rule.Action == "rewrite" {
			if rule.Rewrite == nil {
				return fmt.Errorf("%s rule %s: rewrite action requires rewrite config", phase, rule.ID)
			}
			if rule.Rewrite.Path == "" && rule.Rewrite.Query == "" &&
				len(rule.Rewrite.Headers.Add) == 0 && len(rule.Rewrite.Headers.Set) == 0 && len(rule.Rewrite.Headers.Remove) == 0 {
				return fmt.Errorf("%s rule %s: rewrite action requires at least one of path, query, or headers", phase, rule.ID)
			}
		}

		if rule.Action == "group" {
			if rule.Group == "" {
				return fmt.Errorf("%s rule %s: group action requires group field", phase, rule.ID)
			}
		}
	}

	return nil
}

// validateGRPCMappings validates REST-to-gRPC method mappings
func (l *Loader) validateGRPCMappings(routeID string, cfg GRPCTranslateConfig) error {
	// If method is set, service must also be set
	if cfg.Method != "" && cfg.Service == "" {
		return fmt.Errorf("route %s: grpc.service is required when grpc.method is set", routeID)
	}

	// Method and mappings are mutually exclusive
	if cfg.Method != "" && len(cfg.Mappings) > 0 {
		return fmt.Errorf("route %s: cannot use both grpc.method and grpc.mappings", routeID)
	}

	if len(cfg.Mappings) == 0 {
		return nil
	}

	// If mappings are used, service must be specified
	if cfg.Service == "" {
		return fmt.Errorf("route %s: grpc.service is required when using mappings", routeID)
	}

	validMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true,
	}

	seen := make(map[string]bool)
	for i, m := range cfg.Mappings {
		if m.HTTPMethod == "" {
			return fmt.Errorf("route %s: mapping %d: http_method is required", routeID, i)
		}
		if !validMethods[m.HTTPMethod] {
			return fmt.Errorf("route %s: mapping %d: invalid http_method: %s", routeID, i, m.HTTPMethod)
		}
		if m.HTTPPath == "" {
			return fmt.Errorf("route %s: mapping %d: http_path is required", routeID, i)
		}
		if m.GRPCMethod == "" {
			return fmt.Errorf("route %s: mapping %d: grpc_method is required", routeID, i)
		}

		// Check for duplicate method+path combinations
		key := m.HTTPMethod + " " + m.HTTPPath
		if seen[key] {
			return fmt.Errorf("route %s: mapping %d: duplicate mapping for %s", routeID, i, key)
		}
		seen[key] = true
	}

	return nil
}

// validateMatchConfig validates the match configuration for a route
func (l *Loader) validateMatchConfig(routeID string, mc MatchConfig) error {
	// Validate domains
	for _, domain := range mc.Domains {
		if domain == "" {
			return fmt.Errorf("route %s: match domain must not be empty", routeID)
		}
		if strings.Contains(domain, "*") && !strings.HasPrefix(domain, "*.") {
			return fmt.Errorf("route %s: match domain wildcard must be a prefix '*.', got: %s", routeID, domain)
		}
	}

	// Validate header matchers
	for i, h := range mc.Headers {
		if h.Name == "" {
			return fmt.Errorf("route %s: match header %d: name is required", routeID, i)
		}
		count := 0
		if h.Value != "" {
			count++
		}
		if h.Present != nil {
			count++
		}
		if h.Regex != "" {
			count++
		}
		if count != 1 {
			return fmt.Errorf("route %s: match header %q: must set exactly one of value, present, or regex", routeID, h.Name)
		}
		if h.Regex != "" {
			if _, err := regexp.Compile(h.Regex); err != nil {
				return fmt.Errorf("route %s: match header %q: invalid regex: %w", routeID, h.Name, err)
			}
		}
	}

	// Validate query matchers
	for i, q := range mc.Query {
		if q.Name == "" {
			return fmt.Errorf("route %s: match query %d: name is required", routeID, i)
		}
		count := 0
		if q.Value != "" {
			count++
		}
		if q.Present != nil {
			count++
		}
		if q.Regex != "" {
			count++
		}
		if count != 1 {
			return fmt.Errorf("route %s: match query %q: must set exactly one of value, present, or regex", routeID, q.Name)
		}
		if q.Regex != "" {
			if _, err := regexp.Compile(q.Regex); err != nil {
				return fmt.Errorf("route %s: match query %q: invalid regex: %w", routeID, q.Name, err)
			}
		}
	}

	return nil
}

// expandOpenAPIRoutes generates routes from OpenAPI specs and appends them to cfg.Routes.
func expandOpenAPIRoutes(cfg *Config) error {
	if len(cfg.OpenAPI.Specs) == 0 {
		return nil
	}

	existingIDs := make(map[string]bool, len(cfg.Routes))
	for _, r := range cfg.Routes {
		existingIDs[r.ID] = true
	}

	specIDs := make(map[string]bool)
	for _, specCfg := range cfg.OpenAPI.Specs {
		if specCfg.ID != "" {
			if specIDs[specCfg.ID] {
				return fmt.Errorf("duplicate openapi spec id: %s", specCfg.ID)
			}
			specIDs[specCfg.ID] = true
		}
		if specCfg.File == "" {
			return fmt.Errorf("openapi spec %s: file is required", specCfg.ID)
		}
		if len(specCfg.DefaultBackends) == 0 {
			return fmt.Errorf("openapi spec %s: default_backends is required", specCfg.ID)
		}

		routes, err := generateRoutesFromSpec(specCfg)
		if err != nil {
			return fmt.Errorf("spec %s: %w", specCfg.ID, err)
		}

		for _, r := range routes {
			if existingIDs[r.ID] {
				return fmt.Errorf("openapi spec %s: generated route %s conflicts with existing route", specCfg.ID, r.ID)
			}
			existingIDs[r.ID] = true
			cfg.Routes = append(cfg.Routes, r)
		}
	}

	return nil
}

// pathParamRegex matches OpenAPI path parameters like {user_id}.
var pathParamRegex = regexp.MustCompile(`\{([^}]+)\}`)

// generateRoutesFromSpec auto-generates route configs from an OpenAPI spec file.
func generateRoutesFromSpec(specCfg OpenAPISpecConfig) ([]RouteConfig, error) {
	ctx := context.Background()
	loader := &openapi3.Loader{Context: ctx, IsExternalRefsAllowed: true}
	doc, err := loader.LoadFromFile(specCfg.File)
	if err != nil {
		return nil, fmt.Errorf("failed to load OpenAPI spec: %w", err)
	}
	if err := doc.Validate(ctx); err != nil {
		return nil, fmt.Errorf("invalid OpenAPI spec: %w", err)
	}

	if doc.Paths == nil {
		return nil, nil
	}

	validateReq := true
	if specCfg.Validation.Request != nil {
		validateReq = *specCfg.Validation.Request
	}

	var routes []RouteConfig

	for path, pathItem := range doc.Paths.Map() {
		for method, op := range pathItem.Operations() {
			routeID := openAPIRouteID(method, path, op.OperationID)

			gwPath := specCfg.RoutePrefix + openAPIConvertPath(path)

			valReqPtr := &validateReq
			routeCfg := RouteConfig{
				ID:          routeID,
				Path:        gwPath,
				PathPrefix:  strings.Contains(path, "{"),
				Methods:     []string{strings.ToUpper(method)},
				Backends:    specCfg.DefaultBackends,
				StripPrefix: specCfg.StripPrefix,
				OpenAPI: OpenAPIRouteConfig{
					SpecFile:         specCfg.File,
					SpecID:           specCfg.ID,
					OperationID:      op.OperationID,
					ValidateRequest:  valReqPtr,
					ValidateResponse: specCfg.Validation.Response,
					LogOnly:          specCfg.Validation.LogOnly,
				},
			}

			routes = append(routes, routeCfg)
		}
	}

	return routes, nil
}

// openAPIRouteID creates a route ID from the operation.
func openAPIRouteID(method, path, operationID string) string {
	if operationID != "" {
		return "openapi-" + operationID
	}
	sanitized := strings.NewReplacer("/", "-", "{", "", "}", "").Replace(path)
	sanitized = strings.Trim(sanitized, "-")
	return fmt.Sprintf("openapi-%s-%s", strings.ToLower(method), sanitized)
}

// openAPIConvertPath converts OpenAPI path params {id} to gateway path params :id.
func openAPIConvertPath(path string) string {
	return pathParamRegex.ReplaceAllString(path, ":$1")
}

// LoadFromEnv loads configuration from environment variables
func (l *Loader) LoadFromEnv() (*Config, error) {
	cfg := DefaultConfig()

	// Override with environment variables
	if registryType := os.Getenv("REGISTRY_TYPE"); registryType != "" {
		cfg.Registry.Type = registryType
	}

	if consulAddr := os.Getenv("CONSUL_ADDRESS"); consulAddr != "" {
		cfg.Registry.Consul.Address = consulAddr
	}

	if jwtSecret := os.Getenv("JWT_SECRET"); jwtSecret != "" {
		cfg.Authentication.JWT.Secret = jwtSecret
		cfg.Authentication.JWT.Enabled = true
	}

	return cfg, nil
}

// Merge combines two configurations, with overlay taking precedence
func Merge(base, overlay *Config) *Config {
	result := *base

	// Overlay listeners
	if len(overlay.Listeners) > 0 {
		result.Listeners = overlay.Listeners
	}

	// Overlay registry settings
	if overlay.Registry.Type != "" {
		result.Registry.Type = overlay.Registry.Type
	}

	// Append routes (don't replace)
	if len(overlay.Routes) > 0 {
		result.Routes = append(result.Routes, overlay.Routes...)
	}

	return &result
}
