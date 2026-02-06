package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
)

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

		// Must have either backends or service discovery
		if len(route.Backends) == 0 && route.Service.Name == "" {
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
		if cfg.Authentication.JWT.Secret == "" && cfg.Authentication.JWT.PublicKey == "" {
			return fmt.Errorf("JWT authentication enabled but no secret or public key provided")
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
	return nil
}

// validateRules validates a list of rule configs for a given phase.
func (l *Loader) validateRules(rules []RuleConfig, phase string) error {
	validActions := map[string]bool{
		"block":           true,
		"custom_response": true,
		"redirect":        true,
		"set_headers":     true,
	}

	terminatingActions := map[string]bool{
		"block":           true,
		"custom_response": true,
		"redirect":        true,
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
			return fmt.Errorf("%s rule %s: invalid action %q (must be block, custom_response, redirect, or set_headers)", phase, rule.ID, rule.Action)
		}

		// Response phase: reject terminating actions for now
		if phase == "response" && terminatingActions[rule.Action] {
			return fmt.Errorf("%s rule %s: terminating action %q is not allowed in response phase", phase, rule.ID, rule.Action)
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
