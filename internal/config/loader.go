package config

import (
	"context"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

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

// validate checks configuration for errors.
func (l *Loader) validate(cfg *Config) error {
	// === Listeners ===
	if len(cfg.Listeners) == 0 {
		return fmt.Errorf("at least one listener is required")
	}
	validTypes := map[string]bool{"consul": true, "etcd": true, "kubernetes": true, "memory": true, "dns": true}
	if cfg.Registry.Type != "" && !validTypes[cfg.Registry.Type] {
		return fmt.Errorf("invalid registry type: %s", cfg.Registry.Type)
	}
	if cfg.Registry.Type == "dns" && cfg.Registry.DNSSRV.Domain == "" {
		return fmt.Errorf("registry.dns.domain is required when registry type is 'dns'")
	}
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
		validProtocols := map[Protocol]bool{ProtocolHTTP: true, ProtocolTCP: true, ProtocolUDP: true}
		if listener.Protocol == "" {
			return fmt.Errorf("listener %s: protocol is required", listener.ID)
		}
		if !validProtocols[listener.Protocol] {
			return fmt.Errorf("listener %s: invalid protocol: %s", listener.ID, listener.Protocol)
		}
		if listener.TLS.Enabled {
			if listener.TLS.CertFile == "" {
				return fmt.Errorf("listener %s: TLS enabled but cert_file not provided", listener.ID)
			}
			if listener.TLS.KeyFile == "" {
				return fmt.Errorf("listener %s: TLS enabled but key_file not provided", listener.ID)
			}
		}
		if listener.HTTP.EnableHTTP3 && !listener.TLS.Enabled {
			return fmt.Errorf("listener %s: enable_http3 requires tls.enabled", listener.ID)
		}
	}

	// === Global simple configs ===
	if cfg.ServiceRateLimit.Enabled && cfg.ServiceRateLimit.Rate <= 0 {
		return fmt.Errorf("service_rate_limit: rate must be > 0 when enabled")
	}
	if cfg.DebugEndpoint.Enabled && cfg.DebugEndpoint.Path != "" && !strings.HasPrefix(cfg.DebugEndpoint.Path, "/") {
		return fmt.Errorf("debug_endpoint: path must start with /")
	}
	if cfg.Logging.Rotation.MaxSize < 0 {
		return fmt.Errorf("logging.rotation.max_size must be >= 0")
	}
	if cfg.Logging.Rotation.MaxBackups < 0 {
		return fmt.Errorf("logging.rotation.max_backups must be >= 0")
	}
	if cfg.Logging.Rotation.MaxAge < 0 {
		return fmt.Errorf("logging.rotation.max_age must be >= 0")
	}

	// === Load shedding ===
	if cfg.LoadShedding.Enabled {
		if cfg.LoadShedding.CPUThreshold < 0 || cfg.LoadShedding.CPUThreshold > 100 {
			return fmt.Errorf("load_shedding: cpu_threshold must be between 0 and 100")
		}
		if cfg.LoadShedding.MemoryThreshold < 0 || cfg.LoadShedding.MemoryThreshold > 100 {
			return fmt.Errorf("load_shedding: memory_threshold must be between 0 and 100")
		}
		if cfg.LoadShedding.GoroutineLimit < 0 {
			return fmt.Errorf("load_shedding: goroutine_limit must be >= 0")
		}
	}

	// === Global audit log ===
	if cfg.AuditLog.Enabled {
		if cfg.AuditLog.WebhookURL == "" {
			return fmt.Errorf("audit_log: webhook_url is required when enabled")
		}
		if cfg.AuditLog.SampleRate < 0 || cfg.AuditLog.SampleRate > 1.0 {
			return fmt.Errorf("audit_log: sample_rate must be between 0.0 and 1.0")
		}
	}

	// === Retry budget pools ===
	for name, pool := range cfg.RetryBudgets {
		if pool.Ratio <= 0 || pool.Ratio > 1.0 {
			return fmt.Errorf("retry_budgets[%s]: ratio must be between 0.0 and 1.0 (exclusive of 0)", name)
		}
		if pool.MinRetries < 0 {
			return fmt.Errorf("retry_budgets[%s]: min_retries must be >= 0", name)
		}
	}

	// === API Key Management ===
	if cfg.Authentication.APIKey.Management.Enabled {
		mgmt := cfg.Authentication.APIKey.Management
		if mgmt.KeyLength != 0 && (mgmt.KeyLength < 16 || mgmt.KeyLength > 128) {
			return fmt.Errorf("authentication.api_key.management.key_length must be between 16 and 128")
		}
		switch mgmt.Store {
		case "", "memory":
			// valid
		case "redis":
			if cfg.Redis.Address == "" {
				return fmt.Errorf("authentication.api_key.management.store \"redis\" requires redis.address")
			}
		default:
			return fmt.Errorf("authentication.api_key.management.store must be \"memory\" or \"redis\"")
		}
		if mgmt.DefaultRateLimit != nil {
			if mgmt.DefaultRateLimit.Rate <= 0 {
				return fmt.Errorf("authentication.api_key.management.default_rate_limit.rate must be > 0")
			}
			if mgmt.DefaultRateLimit.Period <= 0 {
				return fmt.Errorf("authentication.api_key.management.default_rate_limit.period must be > 0")
			}
			if mgmt.DefaultRateLimit.Burst <= 0 {
				return fmt.Errorf("authentication.api_key.management.default_rate_limit.burst must be > 0")
			}
		}
	}

	// === Routes (single loop via validateRoute) ===
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
		if err := l.validateRoute(route, cfg); err != nil {
			return err
		}
	}

	// === Tenants ===
	if cfg.Tenants.Enabled {
		if err := l.validateTenants(cfg.Tenants, routeIDs); err != nil {
			return err
		}
	}
	for _, route := range cfg.Routes {
		if route.Tenant.Required && !cfg.Tenants.Enabled {
			return fmt.Errorf("route %s: tenant.required requires tenants to be enabled", route.ID)
		}
		for _, tid := range route.Tenant.Allowed {
			if cfg.Tenants.Enabled && cfg.Tenants.Tenants[tid].Routes == nil && len(cfg.Tenants.Tenants) > 0 {
				if _, ok := cfg.Tenants.Tenants[tid]; !ok {
					return fmt.Errorf("route %s: tenant.allowed references unknown tenant %q", route.ID, tid)
				}
			}
		}
	}

	// === TCP routes ===
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
		if len(route.Match.SourceCIDR) > 0 {
			if _, err := route.Match.ParsedSourceCIDRs(); err != nil {
				return fmt.Errorf("tcp_route %s: invalid source_cidr: %w", route.ID, err)
			}
		}
	}

	// === UDP routes ===
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

	// === JWT ===
	if cfg.Authentication.JWT.Enabled {
		if cfg.Authentication.JWT.Secret == "" && cfg.Authentication.JWT.PublicKey == "" && cfg.Authentication.JWT.JWKSURL == "" {
			return fmt.Errorf("JWT authentication enabled but no secret, public key, or JWKS URL provided")
		}
	}

	// === DNS resolver ===
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

	// === Upstreams ===
	if err := l.validateUpstreams(cfg); err != nil {
		return err
	}

	// === Global validators ===
	if err := l.validateRules(cfg.Rules.Request, "request"); err != nil {
		return fmt.Errorf("global rules: %w", err)
	}
	if err := l.validateRules(cfg.Rules.Response, "response"); err != nil {
		return fmt.Errorf("global rules: %w", err)
	}
	if err := l.validateTrafficShaping(cfg.TrafficShaping, "global"); err != nil {
		return err
	}
	if cfg.WAF.Enabled {
		if cfg.WAF.Mode != "" && cfg.WAF.Mode != "block" && cfg.WAF.Mode != "detect" {
			return fmt.Errorf("global WAF mode must be 'block' or 'detect'")
		}
	}
	if err := l.validateHealthCheck("global", cfg.HealthCheck); err != nil {
		return err
	}
	if err := l.validateErrorPages("global", cfg.ErrorPages); err != nil {
		return err
	}
	if err := l.validateNonceConfig("global", cfg.Nonce, cfg.Redis.Address); err != nil {
		return err
	}
	if err := l.validateCSRFConfig("global", cfg.CSRF); err != nil {
		return err
	}
	if cfg.Geo.Enabled {
		if cfg.Geo.Database == "" {
			return fmt.Errorf("geo: database path is required when geo is enabled")
		}
		dbLower := strings.ToLower(cfg.Geo.Database)
		if !strings.HasSuffix(dbLower, ".mmdb") && !strings.HasSuffix(dbLower, ".ipdb") {
			return fmt.Errorf("geo: database must be a .mmdb or .ipdb file")
		}
		if _, err := os.Stat(cfg.Geo.Database); os.IsNotExist(err) {
			return fmt.Errorf("geo: database file does not exist: %s", cfg.Geo.Database)
		}
	}
	if err := l.validateGeoConfig("global", cfg.Geo); err != nil {
		return err
	}
	if err := l.validateIdempotencyConfig("global", cfg.Idempotency, cfg.Redis.Address); err != nil {
		return err
	}
	if err := l.validateBackendSigningConfig("global", cfg.BackendSigning); err != nil {
		return err
	}
	if err := l.validateDecompressionConfig("global", cfg.RequestDecompression); err != nil {
		return err
	}
	if err := l.validateResponseLimitConfig("global", cfg.ResponseLimit); err != nil {
		return err
	}
	if err := l.validateSecurityHeadersConfig("global", cfg.SecurityHeaders); err != nil {
		return err
	}
	if err := l.validateMaintenanceConfig("global", cfg.Maintenance); err != nil {
		return err
	}
	if err := l.validateShutdownConfig(cfg.Shutdown); err != nil {
		return err
	}
	if err := l.validateTrustedProxiesConfig(cfg.TrustedProxies); err != nil {
		return err
	}
	if err := l.validateTransportConfig("global", cfg.Transport); err != nil {
		return err
	}
	for name, us := range cfg.Upstreams {
		if err := l.validateTransportConfig(fmt.Sprintf("upstream %s", name), us.Transport); err != nil {
			return err
		}
	}
	if cfg.BotDetection.Enabled {
		if err := l.validateBotDetectionConfig("global", cfg.BotDetection); err != nil {
			return err
		}
	}
	if err := l.validateClientMTLSConfig("global", cfg.ClientMTLS); err != nil {
		return err
	}
	if cfg.HTTPSRedirect.Enabled {
		if cfg.HTTPSRedirect.Port < 0 || cfg.HTTPSRedirect.Port > 65535 {
			return fmt.Errorf("https_redirect: port must be 0-65535")
		}
	}
	if cfg.AllowedHosts.Enabled {
		if len(cfg.AllowedHosts.Hosts) == 0 {
			return fmt.Errorf("allowed_hosts: at least one host is required when enabled")
		}
		for i, h := range cfg.AllowedHosts.Hosts {
			if h == "" {
				return fmt.Errorf("allowed_hosts: host at index %d is empty", i)
			}
		}
	}

	// === Token revocation (global) ===
	if cfg.TokenRevocation.Enabled {
		switch cfg.TokenRevocation.Mode {
		case "", "local":
			// valid
		case "distributed":
			if cfg.Redis.Address == "" {
				return fmt.Errorf("token_revocation: distributed mode requires redis configuration")
			}
		default:
			return fmt.Errorf("token_revocation: mode must be \"local\" or \"distributed\", got %q", cfg.TokenRevocation.Mode)
		}
	}

	// === SSRF protection (global) ===
	if err := l.validateSSRFProtectionConfig(cfg.SSRFProtection); err != nil {
		return err
	}

	// === IP blocklist (global) ===
	if err := l.validateIPBlocklistConfig("global", cfg.IPBlocklist); err != nil {
		return err
	}

	// === Webhooks ===
	if err := l.validateWebhooks(cfg.Webhooks); err != nil {
		return err
	}

	return nil
}

// validateUpstreams validates upstream definitions and references.
func (l *Loader) validateUpstreams(cfg *Config) error {
	validLBs := map[string]bool{
		"": true, "round_robin": true, "least_conn": true,
		"consistent_hash": true, "least_response_time": true,
	}
	for name, us := range cfg.Upstreams {
		if len(us.Backends) == 0 && us.Service.Name == "" {
			return fmt.Errorf("upstream %s: must have either backends or service name", name)
		}
		if len(us.Backends) > 0 && us.Service.Name != "" {
			return fmt.Errorf("upstream %s: backends and service are mutually exclusive", name)
		}
		if !validLBs[us.LoadBalancer] {
			return fmt.Errorf("upstream %s: load_balancer must be round_robin, least_conn, consistent_hash, or least_response_time", name)
		}
		if us.LoadBalancer == "consistent_hash" {
			validKeys := map[string]bool{"header": true, "cookie": true, "path": true, "ip": true}
			if !validKeys[us.ConsistentHash.Key] {
				return fmt.Errorf("upstream %s: consistent_hash.key must be header, cookie, path, or ip", name)
			}
			if (us.ConsistentHash.Key == "header" || us.ConsistentHash.Key == "cookie") && us.ConsistentHash.HeaderName == "" {
				return fmt.Errorf("upstream %s: consistent_hash.header_name is required for header/cookie key mode", name)
			}
		}
		if us.HealthCheck != nil {
			if err := l.validateHealthCheck(fmt.Sprintf("upstream %s", name), *us.HealthCheck); err != nil {
				return err
			}
		}
		for i, b := range us.Backends {
			if b.HealthCheck != nil {
				if err := l.validateHealthCheck(fmt.Sprintf("upstream %s backend %d", name, i), *b.HealthCheck); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validateWebhooks validates webhook configuration.
func (l *Loader) validateWebhooks(cfg WebhooksConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if len(cfg.Endpoints) == 0 {
		return fmt.Errorf("webhooks: enabled requires at least one endpoint")
	}
	webhookIDs := make(map[string]bool)
	validEventPrefixes := map[string]bool{
		"backend.": true, "circuit_breaker.": true, "canary.": true,
		"config.": true, "outlier.": true,
	}
	for i, ep := range cfg.Endpoints {
		if ep.ID == "" {
			return fmt.Errorf("webhooks: endpoint %d: id is required", i)
		}
		if webhookIDs[ep.ID] {
			return fmt.Errorf("webhooks: duplicate endpoint id: %s", ep.ID)
		}
		webhookIDs[ep.ID] = true
		if ep.URL == "" {
			return fmt.Errorf("webhooks: endpoint %s: url is required", ep.ID)
		}
		if !strings.HasPrefix(ep.URL, "http://") && !strings.HasPrefix(ep.URL, "https://") {
			return fmt.Errorf("webhooks: endpoint %s: url must start with http:// or https://", ep.ID)
		}
		if len(ep.Events) == 0 {
			return fmt.Errorf("webhooks: endpoint %s: events is required", ep.ID)
		}
		for _, evt := range ep.Events {
			if evt == "*" {
				continue
			}
			valid := false
			for prefix := range validEventPrefixes {
				if strings.HasPrefix(evt, prefix) {
					valid = true
					break
				}
			}
			if !valid {
				return fmt.Errorf("webhooks: endpoint %s: invalid event pattern %q (must start with backend., circuit_breaker., canary., config., or be *)", ep.ID, evt)
			}
		}
	}
	if cfg.Timeout < 0 {
		return fmt.Errorf("webhooks: timeout must be >= 0")
	}
	if cfg.Workers < 0 {
		return fmt.Errorf("webhooks: workers must be >= 0")
	}
	if cfg.QueueSize < 0 {
		return fmt.Errorf("webhooks: queue_size must be >= 0")
	}
	if cfg.Retry.MaxRetries < 0 {
		return fmt.Errorf("webhooks: retry.max_retries must be >= 0")
	}
	if cfg.Retry.Backoff < 0 {
		return fmt.Errorf("webhooks: retry.backoff must be >= 0")
	}
	if cfg.Retry.MaxBackoff < 0 {
		return fmt.Errorf("webhooks: retry.max_backoff must be >= 0")
	}
	if cfg.Retry.MaxBackoff > 0 && cfg.Retry.Backoff > 0 && cfg.Retry.MaxBackoff < cfg.Retry.Backoff {
		return fmt.Errorf("webhooks: retry.max_backoff must be >= retry.backoff")
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
