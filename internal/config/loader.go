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
