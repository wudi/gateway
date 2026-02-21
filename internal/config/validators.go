package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// validateRoute validates a single route configuration by running all
// per-feature validators in sequence.
func (l *Loader) validateRoute(route RouteConfig, cfg *Config) error {
	validators := []func(RouteConfig, *Config) error{
		l.validateRouteBasics,
		l.validateEchoExclusions,
		l.validatePassthroughExclusions,
		l.validateMockAndStaticFiles,
		l.validateFastCGI,
		l.validateBackendAuthAndStatusMapping,
		l.validateSequentialProxy,
		l.validateAggregateProxy,
		l.validateSmallRouteFeatures,
		l.validateRateLimiting,
		l.validateTrafficControls,
		l.validateMirrorAndCORS,
		l.validateResilienceFeatures,
		l.validateNetworkFeatures,
		l.validateTransformsAndValidation,
		l.validateTimeoutPolicy,
		l.validateHealthCheckRefs,
		l.validateOutlierDetection,
		l.validateDelegatedSecurity,
		l.validateDelegatedMiddleware,
		l.validateTenantBackends,
	}
	for _, v := range validators {
		if err := v(route, cfg); err != nil {
			return err
		}
	}
	return nil
}

// --- Route validator helpers ---

func (l *Loader) validateRouteBasics(route RouteConfig, _ *Config) error {
	routeID := route.ID
	if len(route.Backends) == 0 && route.Service.Name == "" && !route.Versioning.Enabled && route.Upstream == "" && !route.Echo && !route.Static.Enabled && !route.Sequential.Enabled && !route.Aggregate.Enabled && !route.FastCGI.Enabled {
		return fmt.Errorf("route %s: must have either backends, service name, or upstream", routeID)
	}
	if route.Upstream != "" {
		if len(route.Backends) > 0 {
			return fmt.Errorf("route %s: upstream and backends are mutually exclusive", routeID)
		}
		if route.Service.Name != "" {
			return fmt.Errorf("route %s: upstream and service are mutually exclusive", routeID)
		}
	}
	return l.validateMatchConfig(routeID, route.Match)
}

func (l *Loader) validateEchoExclusions(route RouteConfig, _ *Config) error {
	if !route.Echo {
		return nil
	}
	routeID := route.ID
	if len(route.Backends) > 0 || route.Service.Name != "" || route.Upstream != "" {
		return fmt.Errorf("route %s: echo is mutually exclusive with backends, service, and upstream", routeID)
	}
	if route.Versioning.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with versioning", routeID)
	}
	if route.Protocol.Type != "" {
		return fmt.Errorf("route %s: echo is mutually exclusive with protocol", routeID)
	}
	if route.WebSocket.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with websocket", routeID)
	}
	if route.CircuitBreaker.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with circuit_breaker", routeID)
	}
	if route.Cache.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with cache", routeID)
	}
	if route.Coalesce.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with coalesce", routeID)
	}
	if route.OutlierDetection.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with outlier_detection", routeID)
	}
	if route.Canary.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with canary", routeID)
	}
	if route.RetryPolicy.MaxRetries > 0 || route.RetryPolicy.Hedging.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with retry_policy", routeID)
	}
	if len(route.TrafficSplit) > 0 {
		return fmt.Errorf("route %s: echo is mutually exclusive with traffic_split", routeID)
	}
	if route.Mirror.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with mirror", routeID)
	}
	if route.MockResponse.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with mock_response", routeID)
	}
	if route.FastCGI.Enabled {
		return fmt.Errorf("route %s: echo is mutually exclusive with fastcgi", routeID)
	}
	return nil
}

func (l *Loader) validatePassthroughExclusions(route RouteConfig, _ *Config) error {
	if !route.Passthrough {
		return nil
	}
	routeID := route.ID
	type passthroughCheck struct {
		active bool
		name   string
	}
	checks := []passthroughCheck{
		{route.Transform.Request.Body.IsActive() || route.Transform.Response.Body.IsActive(), "body transforms"},
		{route.Validation.Enabled, "validation"},
		{route.Compression.Enabled, "compression"},
		{route.Cache.Enabled, "cache"},
		{route.GraphQL.Enabled, "graphql"},
		{route.OpenAPI.SpecFile != "" || route.OpenAPI.SpecID != "", "openapi"},
		{route.RequestDecompression.Enabled, "request_decompression"},
		{route.ResponseLimit.Enabled, "response_limit"},
		{route.ContentReplacer.Enabled, "content_replacer"},
		{route.BodyGenerator.Enabled, "body_generator"},
		{route.Sequential.Enabled, "sequential"},
		{route.Aggregate.Enabled, "aggregate"},
		{route.ResponseBodyGenerator.Enabled, "response_body_generator"},
		{route.ContentNegotiation.Enabled, "content_negotiation"},
		{route.BackendEncoding.Encoding != "", "backend_encoding"},
		{route.PIIRedaction.Enabled, "pii_redaction"},
		{route.FieldEncryption.Enabled, "field_encryption"},
		{route.FastCGI.Enabled, "fastcgi"},
	}
	for _, c := range checks {
		if c.active {
			return fmt.Errorf("route %s: passthrough is mutually exclusive with %s", routeID, c.name)
		}
	}
	return nil
}

func (l *Loader) validateMockAndStaticFiles(route RouteConfig, _ *Config) error {
	routeID := route.ID
	if route.MockResponse.Enabled {
		if route.MockResponse.StatusCode != 0 && (route.MockResponse.StatusCode < 100 || route.MockResponse.StatusCode > 599) {
			return fmt.Errorf("route %s: mock_response.status_code must be 100-599", routeID)
		}
	}
	if route.Static.Enabled {
		if route.Static.Root == "" {
			return fmt.Errorf("route %s: static.root is required", routeID)
		}
		if route.Echo {
			return fmt.Errorf("route %s: static is mutually exclusive with echo", routeID)
		}
		if len(route.Backends) > 0 || route.Service.Name != "" || route.Upstream != "" {
			return fmt.Errorf("route %s: static is mutually exclusive with backends, service, and upstream", routeID)
		}
		if route.FastCGI.Enabled {
			return fmt.Errorf("route %s: static is mutually exclusive with fastcgi", routeID)
		}
	}
	return nil
}

func (l *Loader) validateFastCGI(route RouteConfig, _ *Config) error {
	if !route.FastCGI.Enabled {
		return nil
	}
	routeID := route.ID
	if route.FastCGI.Address == "" {
		return fmt.Errorf("route %s: fastcgi.address is required", routeID)
	}
	if route.FastCGI.DocumentRoot == "" {
		return fmt.Errorf("route %s: fastcgi.document_root is required", routeID)
	}
	if route.FastCGI.Network != "" && route.FastCGI.Network != "tcp" && route.FastCGI.Network != "unix" {
		return fmt.Errorf("route %s: fastcgi.network must be 'tcp' or 'unix'", routeID)
	}
	if route.FastCGI.PoolSize < 0 {
		return fmt.Errorf("route %s: fastcgi.pool_size must be >= 0", routeID)
	}
	if len(route.Backends) > 0 || route.Service.Name != "" || route.Upstream != "" {
		return fmt.Errorf("route %s: fastcgi is mutually exclusive with backends, service, and upstream", routeID)
	}
	if route.Echo {
		return fmt.Errorf("route %s: fastcgi is mutually exclusive with echo", routeID)
	}
	if route.Sequential.Enabled {
		return fmt.Errorf("route %s: fastcgi is mutually exclusive with sequential", routeID)
	}
	if route.Aggregate.Enabled {
		return fmt.Errorf("route %s: fastcgi is mutually exclusive with aggregate", routeID)
	}
	if route.Passthrough {
		return fmt.Errorf("route %s: fastcgi is mutually exclusive with passthrough", routeID)
	}
	return nil
}

func (l *Loader) validateBackendAuthAndStatusMapping(route RouteConfig, _ *Config) error {
	routeID := route.ID
	if route.BackendAuth.Enabled {
		if route.BackendAuth.Type != "oauth2_client_credentials" {
			return fmt.Errorf("route %s: backend_auth.type must be 'oauth2_client_credentials'", routeID)
		}
		if route.BackendAuth.TokenURL == "" {
			return fmt.Errorf("route %s: backend_auth.token_url is required", routeID)
		}
		if route.BackendAuth.ClientID == "" {
			return fmt.Errorf("route %s: backend_auth.client_id is required", routeID)
		}
		if route.BackendAuth.ClientSecret == "" {
			return fmt.Errorf("route %s: backend_auth.client_secret is required", routeID)
		}
	}
	if route.StatusMapping.Enabled {
		for from, to := range route.StatusMapping.Mappings {
			if from < 100 || from > 599 {
				return fmt.Errorf("route %s: status_mapping.mappings key %d is not a valid HTTP status code (100-599)", routeID, from)
			}
			if to < 100 || to > 599 {
				return fmt.Errorf("route %s: status_mapping.mappings value %d is not a valid HTTP status code (100-599)", routeID, to)
			}
		}
	}
	return nil
}

func (l *Loader) validateSequentialProxy(route RouteConfig, _ *Config) error {
	if !route.Sequential.Enabled {
		return nil
	}
	routeID := route.ID
	if len(route.Sequential.Steps) < 2 {
		return fmt.Errorf("route %s: sequential requires at least 2 steps", routeID)
	}
	for j, step := range route.Sequential.Steps {
		if step.URL == "" {
			return fmt.Errorf("route %s: sequential step %d requires a URL", routeID, j)
		}
	}
	if route.Echo {
		return fmt.Errorf("route %s: sequential is mutually exclusive with echo", routeID)
	}
	if route.Static.Enabled {
		return fmt.Errorf("route %s: sequential is mutually exclusive with static", routeID)
	}
	if route.FastCGI.Enabled {
		return fmt.Errorf("route %s: sequential is mutually exclusive with fastcgi", routeID)
	}
	return nil
}

func (l *Loader) validateAggregateProxy(route RouteConfig, _ *Config) error {
	if !route.Aggregate.Enabled {
		return nil
	}
	routeID := route.ID
	if len(route.Aggregate.Backends) < 2 {
		return fmt.Errorf("route %s: aggregate requires at least 2 backends", routeID)
	}
	names := make(map[string]bool)
	for j, ab := range route.Aggregate.Backends {
		if ab.Name == "" {
			return fmt.Errorf("route %s: aggregate backend %d requires a name", routeID, j)
		}
		if names[ab.Name] {
			return fmt.Errorf("route %s: duplicate aggregate backend name: %s", routeID, ab.Name)
		}
		names[ab.Name] = true
		if ab.URL == "" {
			return fmt.Errorf("route %s: aggregate backend %s requires a URL", routeID, ab.Name)
		}
	}
	fs := route.Aggregate.FailStrategy
	if fs != "" && fs != "abort" && fs != "partial" {
		return fmt.Errorf("route %s: aggregate fail_strategy must be 'abort' or 'partial'", routeID)
	}
	if route.Echo {
		return fmt.Errorf("route %s: aggregate is mutually exclusive with echo", routeID)
	}
	if route.Sequential.Enabled {
		return fmt.Errorf("route %s: aggregate is mutually exclusive with sequential", routeID)
	}
	if route.Static.Enabled {
		return fmt.Errorf("route %s: aggregate is mutually exclusive with static", routeID)
	}
	if route.FastCGI.Enabled {
		return fmt.Errorf("route %s: aggregate is mutually exclusive with fastcgi", routeID)
	}
	return nil
}

func (l *Loader) validateSmallRouteFeatures(route RouteConfig, _ *Config) error {
	routeID := route.ID

	// Spike arrest
	if route.SpikeArrest.Enabled && route.SpikeArrest.Rate <= 0 {
		return fmt.Errorf("route %s: spike_arrest rate must be > 0 when enabled", routeID)
	}

	// Content replacer
	if route.ContentReplacer.Enabled {
		if len(route.ContentReplacer.Replacements) == 0 {
			return fmt.Errorf("route %s: content_replacer requires at least one replacement", routeID)
		}
		for j, rule := range route.ContentReplacer.Replacements {
			if _, err := regexp.Compile(rule.Pattern); err != nil {
				return fmt.Errorf("route %s: content_replacer replacement %d: invalid pattern: %w", routeID, j, err)
			}
		}
	}

	// Follow redirects
	if route.FollowRedirects.Enabled && route.FollowRedirects.MaxRedirects < 0 {
		return fmt.Errorf("route %s: follow_redirects max_redirects must be >= 0", routeID)
	}

	// Body generator
	if route.BodyGenerator.Enabled {
		if route.BodyGenerator.Template == "" {
			return fmt.Errorf("route %s: body_generator requires a template", routeID)
		}
	}

	// Response body generator
	if route.ResponseBodyGenerator.Enabled {
		if route.ResponseBodyGenerator.Template == "" {
			return fmt.Errorf("route %s: response_body_generator requires a template", routeID)
		}
	}

	// Param forwarding
	if route.ParamForwarding.Enabled {
		if len(route.ParamForwarding.Headers) == 0 && len(route.ParamForwarding.QueryParams) == 0 && len(route.ParamForwarding.Cookies) == 0 {
			return fmt.Errorf("route %s: param_forwarding requires at least one of headers, query_params, or cookies", routeID)
		}
	}

	// SSE proxy
	if route.SSE.Enabled {
		if route.SSE.HeartbeatInterval < 0 {
			return fmt.Errorf("route %s: sse.heartbeat_interval must be >= 0", routeID)
		}
		if route.SSE.RetryMS < 0 {
			return fmt.Errorf("route %s: sse.retry_ms must be >= 0", routeID)
		}
		if route.SSE.MaxIdle < 0 {
			return fmt.Errorf("route %s: sse.max_idle must be >= 0", routeID)
		}
		if route.Passthrough {
			return fmt.Errorf("route %s: sse is mutually exclusive with passthrough", routeID)
		}
		if route.ResponseBodyGenerator.Enabled {
			return fmt.Errorf("route %s: sse is mutually exclusive with response_body_generator", routeID)
		}
		if route.SSE.Fanout.Enabled {
			if route.SSE.Fanout.BufferSize < 0 {
				return fmt.Errorf("route %s: sse.fanout.buffer_size must be >= 0", routeID)
			}
			if route.SSE.Fanout.ClientBufferSize < 0 {
				return fmt.Errorf("route %s: sse.fanout.client_buffer_size must be >= 0", routeID)
			}
			if route.SSE.Fanout.ReconnectDelay < 0 {
				return fmt.Errorf("route %s: sse.fanout.reconnect_delay must be >= 0", routeID)
			}
			if route.SSE.Fanout.MaxReconnects < 0 {
				return fmt.Errorf("route %s: sse.fanout.max_reconnects must be >= 0", routeID)
			}
		}
	}

	// Content negotiation
	if route.ContentNegotiation.Enabled {
		validFormats := map[string]bool{"json": true, "xml": true, "yaml": true}
		for _, f := range route.ContentNegotiation.Supported {
			if !validFormats[f] {
				return fmt.Errorf("route %s: content_negotiation supported format %q must be json, xml, or yaml", routeID, f)
			}
		}
		if route.ContentNegotiation.Default != "" && !validFormats[route.ContentNegotiation.Default] {
			return fmt.Errorf("route %s: content_negotiation default %q must be json, xml, or yaml", routeID, route.ContentNegotiation.Default)
		}
	}

	// CDN cache headers
	if route.CDNCacheHeaders.Enabled {
		if route.CDNCacheHeaders.CacheControl == "" && route.CDNCacheHeaders.SurrogateControl == "" && len(route.CDNCacheHeaders.Vary) == 0 {
			return fmt.Errorf("route %s: cdn_cache_headers requires at least one of cache_control, surrogate_control, or vary", routeID)
		}
	}

	// Cache bucket name
	if route.Cache.Bucket != "" {
		for _, c := range route.Cache.Bucket {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
				return fmt.Errorf("route %s: cache bucket name must be alphanumeric with hyphens/underscores", routeID)
			}
		}
	}

	// Backend encoding
	if route.BackendEncoding.Encoding != "" {
		if route.BackendEncoding.Encoding != "xml" && route.BackendEncoding.Encoding != "yaml" {
			return fmt.Errorf("route %s: backend_encoding encoding must be 'xml' or 'yaml', got %q", routeID, route.BackendEncoding.Encoding)
		}
	}

	// Quota
	if route.Quota.Enabled {
		if route.Quota.Limit <= 0 {
			return fmt.Errorf("route %s: quota limit must be > 0", routeID)
		}
		validPeriods := map[string]bool{"hourly": true, "daily": true, "monthly": true, "yearly": true}
		if !validPeriods[route.Quota.Period] {
			return fmt.Errorf("route %s: quota period must be hourly, daily, monthly, or yearly", routeID)
		}
		if route.Quota.Key == "" {
			return fmt.Errorf("route %s: quota key is required", routeID)
		}
	}

	// Proxy rate limit
	if route.ProxyRateLimit.Enabled {
		if route.ProxyRateLimit.Rate <= 0 {
			return fmt.Errorf("route %s: proxy_rate_limit.rate must be > 0", routeID)
		}
	}

	// Claims propagation
	if route.ClaimsPropagation.Enabled {
		if len(route.ClaimsPropagation.Claims) == 0 {
			return fmt.Errorf("route %s: claims_propagation: at least one claim mapping is required when enabled", routeID)
		}
		for claimName, headerName := range route.ClaimsPropagation.Claims {
			if claimName == "" {
				return fmt.Errorf("route %s: claims_propagation: empty claim name", routeID)
			}
			if headerName == "" {
				return fmt.Errorf("route %s: claims_propagation: empty header name for claim %q", routeID, claimName)
			}
		}
	}

	return nil
}

func (l *Loader) validateRateLimiting(route RouteConfig, cfg *Config) error {
	routeID := route.ID

	if route.RateLimit.Mode == "distributed" && cfg.Redis.Address == "" {
		return fmt.Errorf("route %s: distributed rate limiting requires redis.address to be configured", routeID)
	}
	switch route.RateLimit.Algorithm {
	case "", "token_bucket", "sliding_window":
		// valid
	default:
		return fmt.Errorf("route %s: invalid rate_limit.algorithm %q (must be \"token_bucket\" or \"sliding_window\")", routeID, route.RateLimit.Algorithm)
	}
	if route.RateLimit.Algorithm == "sliding_window" && route.RateLimit.Mode == "distributed" {
		return fmt.Errorf("route %s: algorithm \"sliding_window\" is incompatible with mode \"distributed\" (distributed already uses a sliding window)", routeID)
	}
	if route.RateLimit.Key != "" && route.RateLimit.PerIP {
		return fmt.Errorf("route %s: rate_limit.key and rate_limit.per_ip are mutually exclusive", routeID)
	}
	if route.RateLimit.Key != "" {
		key := route.RateLimit.Key
		switch {
		case key == "ip", key == "client_id":
			// valid
		case strings.HasPrefix(key, "header:"):
			if key[len("header:"):] == "" {
				return fmt.Errorf("route %s: rate_limit.key \"header:\" requires a non-empty header name", routeID)
			}
		case strings.HasPrefix(key, "cookie:"):
			if key[len("cookie:"):] == "" {
				return fmt.Errorf("route %s: rate_limit.key \"cookie:\" requires a non-empty cookie name", routeID)
			}
		case strings.HasPrefix(key, "jwt_claim:"):
			if key[len("jwt_claim:"):] == "" {
				return fmt.Errorf("route %s: rate_limit.key \"jwt_claim:\" requires a non-empty claim name", routeID)
			}
		default:
			return fmt.Errorf("route %s: invalid rate_limit.key %q (must be \"ip\", \"client_id\", \"header:<name>\", \"cookie:<name>\", or \"jwt_claim:<name>\")", routeID, key)
		}
	}

	// Tiered rate limits
	if len(route.RateLimit.Tiers) > 0 {
		if route.RateLimit.Rate > 0 {
			return fmt.Errorf("route %s: rate_limit.tiers and rate_limit.rate are mutually exclusive", routeID)
		}
		if route.RateLimit.DefaultTier == "" {
			return fmt.Errorf("route %s: rate_limit.default_tier is required when tiers are set", routeID)
		}
		if _, ok := route.RateLimit.Tiers[route.RateLimit.DefaultTier]; !ok {
			return fmt.Errorf("route %s: rate_limit.default_tier %q must exist in tiers", routeID, route.RateLimit.DefaultTier)
		}
		if route.RateLimit.TierKey == "" {
			return fmt.Errorf("route %s: rate_limit.tier_key is required when tiers are set", routeID)
		}
		if !strings.HasPrefix(route.RateLimit.TierKey, "header:") && !strings.HasPrefix(route.RateLimit.TierKey, "jwt_claim:") {
			return fmt.Errorf("route %s: rate_limit.tier_key must be \"header:<name>\" or \"jwt_claim:<name>\"", routeID)
		}
		for name, tier := range route.RateLimit.Tiers {
			if tier.Rate <= 0 {
				return fmt.Errorf("route %s: rate_limit.tiers[%s].rate must be > 0", routeID, name)
			}
		}
	}

	return nil
}

func (l *Loader) validateTrafficControls(route RouteConfig, cfg *Config) error {
	routeID := route.ID
	scope := fmt.Sprintf("route %s", routeID)

	// Per-route rules
	if err := l.validateRules(route.Rules.Request, "request"); err != nil {
		return fmt.Errorf("route %s rules: %w", routeID, err)
	}
	if err := l.validateRules(route.Rules.Response, "response"); err != nil {
		return fmt.Errorf("route %s rules: %w", routeID, err)
	}

	// Per-route traffic shaping
	if err := l.validateTrafficShaping(route.TrafficShaping, scope); err != nil {
		return err
	}
	if route.TrafficShaping.Priority.Enabled && !cfg.TrafficShaping.Priority.Enabled {
		return fmt.Errorf("route %s: per-route priority requires global priority to be enabled", routeID)
	}

	// Sticky sessions
	if route.Sticky.Enabled {
		validModes := map[string]bool{"cookie": true, "header": true, "hash": true}
		if route.Sticky.Mode == "" {
			return fmt.Errorf("route %s: sticky.mode is required when enabled", routeID)
		}
		if !validModes[route.Sticky.Mode] {
			return fmt.Errorf("route %s: sticky.mode must be cookie, header, or hash", routeID)
		}
		if len(route.TrafficSplit) == 0 {
			return fmt.Errorf("route %s: sticky requires traffic_split to be configured", routeID)
		}
		if (route.Sticky.Mode == "header" || route.Sticky.Mode == "hash") && route.Sticky.HashKey == "" {
			return fmt.Errorf("route %s: sticky.hash_key is required for header/hash mode", routeID)
		}
	}

	// Traffic split
	if len(route.TrafficSplit) > 0 {
		totalWeight := 0
		for _, split := range route.TrafficSplit {
			totalWeight += split.Weight
		}
		if totalWeight != 100 {
			return fmt.Errorf("route %s: traffic_split weights must sum to 100, got %d", routeID, totalWeight)
		}
	}

	return nil
}

func (l *Loader) validateMirrorAndCORS(route RouteConfig, _ *Config) error {
	routeID := route.ID

	// Mirror
	if route.Mirror.Enabled && route.Mirror.Conditions.PathRegex != "" {
		if _, err := regexp.Compile(route.Mirror.Conditions.PathRegex); err != nil {
			return fmt.Errorf("route %s: mirror conditions path_regex is invalid: %w", routeID, err)
		}
	}
	if route.Mirror.Enabled && route.Mirror.Percentage != 0 {
		if route.Mirror.Percentage < 0 || route.Mirror.Percentage > 100 {
			return fmt.Errorf("route %s: mirror percentage must be between 0 and 100", routeID)
		}
	}
	if route.Mirror.Compare.DetailedDiff && !route.Mirror.Compare.Enabled {
		return fmt.Errorf("route %s: mirror compare.detailed_diff requires compare.enabled", routeID)
	}
	if route.Mirror.Compare.MaxBodyCapture < 0 {
		return fmt.Errorf("route %s: mirror compare.max_body_capture must be >= 0", routeID)
	}
	if route.Mirror.Compare.MaxMismatches < 0 {
		return fmt.Errorf("route %s: mirror compare.max_mismatches must be >= 0", routeID)
	}
	for i, field := range route.Mirror.Compare.IgnoreJSONFields {
		if field == "" {
			return fmt.Errorf("route %s: mirror compare.ignore_json_fields[%d] must be non-empty", routeID, i)
		}
	}

	// CORS regex
	for _, pattern := range route.CORS.AllowOriginPatterns {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("route %s: cors allow_origin_patterns: invalid regex %q: %w", routeID, pattern, err)
		}
	}

	return nil
}

func (l *Loader) validateResilienceFeatures(route RouteConfig, cfg *Config) error {
	routeID := route.ID

	// Retry policy
	if route.RetryPolicy.MaxRetries > 0 {
		if route.RetryPolicy.BackoffMultiplier != 0 && route.RetryPolicy.BackoffMultiplier < 1.0 {
			return fmt.Errorf("route %s: retry_policy backoff_multiplier must be >= 1.0", routeID)
		}
		for _, status := range route.RetryPolicy.RetryableStatuses {
			if status < 100 || status > 599 {
				return fmt.Errorf("route %s: retry_policy contains invalid HTTP status code: %d", routeID, status)
			}
		}
	}
	if route.RetryPolicy.Budget.Ratio > 0 {
		if route.RetryPolicy.Budget.Ratio > 1.0 {
			return fmt.Errorf("route %s: retry_policy budget ratio must be between 0.0 and 1.0", routeID)
		}
		if route.RetryPolicy.Budget.MinRetries < 0 {
			return fmt.Errorf("route %s: retry_policy budget min_retries must be >= 0", routeID)
		}
		if route.RetryPolicy.Budget.Window < 0 {
			return fmt.Errorf("route %s: retry_policy budget window must be > 0", routeID)
		}
	}
	if route.RetryPolicy.BudgetPool != "" {
		if route.RetryPolicy.Budget.Ratio > 0 {
			return fmt.Errorf("route %s: retry_policy budget_pool and inline budget.ratio are mutually exclusive", routeID)
		}
		if _, ok := cfg.RetryBudgets[route.RetryPolicy.BudgetPool]; !ok {
			return fmt.Errorf("route %s: retry_policy budget_pool %q not found in retry_budgets", routeID, route.RetryPolicy.BudgetPool)
		}
	}
	if route.RetryPolicy.Hedging.Enabled {
		if route.RetryPolicy.Hedging.MaxRequests < 2 {
			return fmt.Errorf("route %s: retry_policy hedging max_requests must be >= 2", routeID)
		}
		if route.RetryPolicy.MaxRetries > 0 {
			return fmt.Errorf("route %s: retry_policy cannot use both hedging and max_retries", routeID)
		}
	}

	// Circuit breaker
	if route.CircuitBreaker.Enabled {
		if route.CircuitBreaker.FailureThreshold != 0 && route.CircuitBreaker.FailureThreshold < 1 {
			return fmt.Errorf("route %s: circuit_breaker failure_threshold must be > 0", routeID)
		}
		if route.CircuitBreaker.MaxRequests != 0 && route.CircuitBreaker.MaxRequests < 1 {
			return fmt.Errorf("route %s: circuit_breaker max_requests must be > 0", routeID)
		}
		if route.CircuitBreaker.Timeout != 0 && route.CircuitBreaker.Timeout < 0 {
			return fmt.Errorf("route %s: circuit_breaker timeout must be > 0", routeID)
		}
		if route.CircuitBreaker.Mode != "" && route.CircuitBreaker.Mode != "local" && route.CircuitBreaker.Mode != "distributed" {
			return fmt.Errorf("route %s: circuit_breaker mode must be 'local' or 'distributed'", routeID)
		}
		if route.CircuitBreaker.Mode == "distributed" && cfg.Redis.Address == "" {
			return fmt.Errorf("route %s: circuit_breaker mode 'distributed' requires redis configuration", routeID)
		}
	}

	// Cache
	if route.Cache.Enabled {
		if route.Cache.TTL != 0 && route.Cache.TTL < 0 {
			return fmt.Errorf("route %s: cache ttl must be > 0", routeID)
		}
		if route.Cache.MaxSize != 0 && route.Cache.MaxSize < 1 {
			return fmt.Errorf("route %s: cache max_size must be > 0", routeID)
		}
		if route.Cache.Mode != "" && route.Cache.Mode != "local" && route.Cache.Mode != "distributed" {
			return fmt.Errorf("route %s: cache mode must be \"local\" or \"distributed\"", routeID)
		}
		if route.Cache.Mode == "distributed" && cfg.Redis.Address == "" {
			return fmt.Errorf("route %s: distributed cache requires redis.address to be configured", routeID)
		}
	}

	// Coalesce
	if route.Coalesce.Enabled {
		if route.Coalesce.Timeout < 0 {
			return fmt.Errorf("route %s: coalesce timeout must be >= 0", routeID)
		}
		for _, m := range route.Coalesce.Methods {
			if !validHTTPMethods[m] {
				return fmt.Errorf("route %s: coalesce methods contains invalid HTTP method: %s", routeID, m)
			}
		}
	}

	// Canary
	if route.Canary.Enabled {
		if len(route.TrafficSplit) == 0 {
			return fmt.Errorf("route %s: canary requires traffic_split to be configured", routeID)
		}
		if route.Canary.CanaryGroup == "" {
			return fmt.Errorf("route %s: canary.canary_group is required", routeID)
		}
		groupFound := false
		for _, split := range route.TrafficSplit {
			if split.Name == route.Canary.CanaryGroup {
				groupFound = true
				break
			}
		}
		if !groupFound {
			return fmt.Errorf("route %s: canary.canary_group %q not found in traffic_split groups", routeID, route.Canary.CanaryGroup)
		}
		if len(route.Canary.Steps) == 0 {
			return fmt.Errorf("route %s: canary requires at least one step", routeID)
		}
		for i, step := range route.Canary.Steps {
			if step.Weight < 0 || step.Weight > 100 {
				return fmt.Errorf("route %s: canary step %d weight must be 0-100", routeID, i)
			}
			if i > 0 && step.Weight < route.Canary.Steps[i-1].Weight {
				return fmt.Errorf("route %s: canary step weights must be monotonically non-decreasing", routeID)
			}
		}
		if route.Canary.Analysis.ErrorThreshold < 0 || route.Canary.Analysis.ErrorThreshold > 1.0 {
			return fmt.Errorf("route %s: canary analysis error_threshold must be 0.0-1.0", routeID)
		}
		if route.Canary.Analysis.Interval < 0 {
			return fmt.Errorf("route %s: canary analysis interval must be >= 0", routeID)
		}
		if route.Canary.Analysis.MaxErrorRateIncrease < 0 {
			return fmt.Errorf("route %s: canary analysis max_error_rate_increase must be >= 0", routeID)
		}
		if route.Canary.Analysis.MaxLatencyIncrease < 0 {
			return fmt.Errorf("route %s: canary analysis max_latency_increase must be >= 0", routeID)
		}
		if route.Canary.Analysis.MaxFailures < 0 {
			return fmt.Errorf("route %s: canary analysis max_failures must be >= 0", routeID)
		}
	}

	// Blue-green
	if route.BlueGreen.Enabled {
		if route.Canary.Enabled {
			return fmt.Errorf("route %s: blue_green is mutually exclusive with canary", routeID)
		}
		if len(route.TrafficSplit) == 0 {
			return fmt.Errorf("route %s: blue_green requires traffic_split to be configured", routeID)
		}
		if route.BlueGreen.ActiveGroup == "" {
			return fmt.Errorf("route %s: blue_green.active_group is required", routeID)
		}
		if route.BlueGreen.InactiveGroup == "" {
			return fmt.Errorf("route %s: blue_green.inactive_group is required", routeID)
		}
		activeFound, inactiveFound := false, false
		for _, split := range route.TrafficSplit {
			if split.Name == route.BlueGreen.ActiveGroup {
				activeFound = true
			}
			if split.Name == route.BlueGreen.InactiveGroup {
				inactiveFound = true
			}
		}
		if !activeFound {
			return fmt.Errorf("route %s: blue_green.active_group %q not found in traffic_split groups", routeID, route.BlueGreen.ActiveGroup)
		}
		if !inactiveFound {
			return fmt.Errorf("route %s: blue_green.inactive_group %q not found in traffic_split groups", routeID, route.BlueGreen.InactiveGroup)
		}
		if route.BlueGreen.ErrorThreshold < 0 || route.BlueGreen.ErrorThreshold > 1.0 {
			return fmt.Errorf("route %s: blue_green.error_threshold must be 0.0-1.0", routeID)
		}
	}

	// A/B test
	if route.ABTest.Enabled {
		if route.Canary.Enabled {
			return fmt.Errorf("route %s: ab_test is mutually exclusive with canary", routeID)
		}
		if route.BlueGreen.Enabled {
			return fmt.Errorf("route %s: ab_test is mutually exclusive with blue_green", routeID)
		}
		if len(route.TrafficSplit) == 0 {
			return fmt.Errorf("route %s: ab_test requires traffic_split to be configured", routeID)
		}
		if route.ABTest.ExperimentName == "" {
			return fmt.Errorf("route %s: ab_test.experiment_name is required when enabled", routeID)
		}
	}

	return nil
}

func (l *Loader) validateNetworkFeatures(route RouteConfig, _ *Config) error {
	routeID := route.ID

	// WAF
	if route.WAF.Enabled {
		if route.WAF.Mode != "" && route.WAF.Mode != "block" && route.WAF.Mode != "detect" {
			return fmt.Errorf("route %s: WAF mode must be 'block' or 'detect'", routeID)
		}
	}

	// GraphQL
	if route.GraphQL.Enabled {
		if route.GraphQL.MaxDepth < 0 {
			return fmt.Errorf("route %s: graphql max_depth must be >= 0", routeID)
		}
		if route.GraphQL.MaxComplexity < 0 {
			return fmt.Errorf("route %s: graphql max_complexity must be >= 0", routeID)
		}
		validOpTypes := map[string]bool{"query": true, "mutation": true, "subscription": true}
		for opType, limit := range route.GraphQL.OperationLimits {
			if !validOpTypes[opType] {
				return fmt.Errorf("route %s: graphql operation_limits key %q must be query, mutation, or subscription", routeID, opType)
			}
			if limit <= 0 {
				return fmt.Errorf("route %s: graphql operation_limits value for %q must be > 0", routeID, opType)
			}
		}
	}

	// WebSocket
	if route.WebSocket.Enabled {
		if route.WebSocket.ReadBufferSize != 0 && route.WebSocket.ReadBufferSize < 1 {
			return fmt.Errorf("route %s: websocket read_buffer_size must be > 0", routeID)
		}
		if route.WebSocket.WriteBufferSize != 0 && route.WebSocket.WriteBufferSize < 1 {
			return fmt.Errorf("route %s: websocket write_buffer_size must be > 0", routeID)
		}
	}

	// Load balancer
	if route.LoadBalancer != "" {
		validLBs := map[string]bool{
			"round_robin":         true,
			"least_conn":          true,
			"consistent_hash":     true,
			"least_response_time": true,
		}
		if !validLBs[route.LoadBalancer] {
			return fmt.Errorf("route %s: load_balancer must be round_robin, least_conn, consistent_hash, or least_response_time", routeID)
		}
		if route.LoadBalancer == "consistent_hash" {
			validKeys := map[string]bool{"header": true, "cookie": true, "path": true, "ip": true}
			if !validKeys[route.ConsistentHash.Key] {
				return fmt.Errorf("route %s: consistent_hash.key must be header, cookie, path, or ip", routeID)
			}
			if (route.ConsistentHash.Key == "header" || route.ConsistentHash.Key == "cookie") && route.ConsistentHash.HeaderName == "" {
				return fmt.Errorf("route %s: consistent_hash.header_name is required for header/cookie key mode", routeID)
			}
		}
		if route.LoadBalancer != "round_robin" && len(route.TrafficSplit) > 0 {
			return fmt.Errorf("route %s: load_balancer %s is incompatible with traffic_split", routeID, route.LoadBalancer)
		}
	}

	// gRPC proxy
	if route.GRPC.Enabled {
		if route.GRPC.MaxRecvMsgSize < 0 {
			return fmt.Errorf("route %s: grpc.max_recv_msg_size must be >= 0", routeID)
		}
		if route.GRPC.MaxSendMsgSize < 0 {
			return fmt.Errorf("route %s: grpc.max_send_msg_size must be >= 0", routeID)
		}
		if route.GRPC.HealthCheck.Enabled && !route.GRPC.Enabled {
			return fmt.Errorf("route %s: grpc.health_check requires grpc.enabled", routeID)
		}
	}

	// Protocol translation
	if route.Protocol.Type != "" {
		validProtocolTypes := map[string]bool{"http_to_grpc": true, "http_to_thrift": true, "grpc_to_rest": true}
		if !validProtocolTypes[route.Protocol.Type] {
			return fmt.Errorf("route %s: unknown protocol type: %s", routeID, route.Protocol.Type)
		}
		if route.GRPC.Enabled {
			return fmt.Errorf("route %s: cannot enable both grpc.enabled and protocol translation", routeID)
		}
		switch route.Protocol.Type {
		case "http_to_grpc":
			if route.Protocol.GRPC.TLS.Enabled {
				if route.Protocol.GRPC.TLS.CAFile == "" {
					return fmt.Errorf("route %s: protocol grpc tls enabled but ca_file not provided", routeID)
				}
			}
			if err := l.validateGRPCMappings(routeID, route.Protocol.GRPC); err != nil {
				return err
			}
		case "http_to_thrift":
			hasIDL := route.Protocol.Thrift.IDLFile != ""
			hasMethods := len(route.Protocol.Thrift.Methods) > 0
			if hasIDL && hasMethods {
				return fmt.Errorf("route %s: thrift.idl_file and thrift.methods are mutually exclusive", routeID)
			}
			if !hasIDL && !hasMethods {
				return fmt.Errorf("route %s: thrift.idl_file or thrift.methods is required for http_to_thrift", routeID)
			}
			if route.Protocol.Thrift.Service == "" {
				return fmt.Errorf("route %s: thrift.service is required for http_to_thrift", routeID)
			}
			if p := route.Protocol.Thrift.Protocol; p != "" && p != "binary" && p != "compact" {
				return fmt.Errorf("route %s: thrift.protocol must be 'binary' or 'compact', got %q", routeID, p)
			}
			if t := route.Protocol.Thrift.Transport; t != "" && t != "framed" && t != "buffered" {
				return fmt.Errorf("route %s: thrift.transport must be 'framed' or 'buffered', got %q", routeID, t)
			}
			if route.Protocol.Thrift.TLS.Enabled {
				if route.Protocol.Thrift.TLS.CAFile == "" {
					return fmt.Errorf("route %s: thrift tls enabled but ca_file not provided", routeID)
				}
			}
			if err := l.validateThriftMappings(routeID, route.Protocol.Thrift); err != nil {
				return err
			}
			if hasMethods {
				if err := l.validateThriftInlineSchema(routeID, route.Protocol.Thrift); err != nil {
					return err
				}
			}
		case "grpc_to_rest":
			if len(route.Protocol.REST.Mappings) == 0 {
				return fmt.Errorf("route %s: grpc_to_rest requires at least one mapping", routeID)
			}
			if err := l.validateGRPCToRESTMappings(routeID, route.Protocol.REST); err != nil {
				return err
			}
		}
	}

	// External auth
	if route.ExtAuth.Enabled {
		if route.ExtAuth.URL == "" {
			return fmt.Errorf("route %s: ext_auth.url is required when enabled", routeID)
		}
		if !strings.HasPrefix(route.ExtAuth.URL, "http://") &&
			!strings.HasPrefix(route.ExtAuth.URL, "https://") &&
			!strings.HasPrefix(route.ExtAuth.URL, "grpc://") {
			return fmt.Errorf("route %s: ext_auth.url must start with http://, https://, or grpc://", routeID)
		}
		if route.ExtAuth.Timeout < 0 {
			return fmt.Errorf("route %s: ext_auth.timeout must be >= 0", routeID)
		}
		if route.ExtAuth.CacheTTL < 0 {
			return fmt.Errorf("route %s: ext_auth.cache_ttl must be >= 0", routeID)
		}
		if route.ExtAuth.TLS.Enabled && strings.HasPrefix(route.ExtAuth.URL, "http://") {
			return fmt.Errorf("route %s: ext_auth.tls cannot be used with http:// URL", routeID)
		}
	}

	return nil
}

func (l *Loader) validateTransformsAndValidation(route RouteConfig, cfg *Config) error {
	routeID := route.ID

	// Versioning
	if route.Versioning.Enabled {
		if err := l.validateRouteVersioning(routeID, route); err != nil {
			return err
		}
	}

	// Body transforms
	if err := l.validateBodyTransform(routeID, "request", route.Transform.Request.Body); err != nil {
		return err
	}
	if err := l.validateBodyTransform(routeID, "response", route.Transform.Response.Body); err != nil {
		return err
	}

	// Access log
	if err := l.validateAccessLog(routeID, route.AccessLog); err != nil {
		return err
	}

	// OpenAPI
	if route.OpenAPI.SpecFile != "" && route.OpenAPI.SpecID != "" {
		return fmt.Errorf("route %s: openapi spec_file and spec_id are mutually exclusive", routeID)
	}
	if route.OpenAPI.SpecID != "" {
		found := false
		for _, s := range cfg.OpenAPI.Specs {
			if s.ID == route.OpenAPI.SpecID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("route %s: openapi.spec_id %q not found in openapi.specs", routeID, route.OpenAPI.SpecID)
		}
	}

	// Response validation
	if route.Validation.ResponseSchema != "" && route.Validation.ResponseSchemaFile != "" {
		return fmt.Errorf("route %s: validation response_schema and response_schema_file are mutually exclusive", routeID)
	}

	// Rewrite
	if err := l.validateRewriteConfig(routeID, route.Rewrite, route.PathPrefix, route.StripPrefix); err != nil {
		return err
	}

	return nil
}

func (l *Loader) validateTimeoutPolicy(route RouteConfig, _ *Config) error {
	if !route.TimeoutPolicy.IsActive() {
		return nil
	}
	routeID := route.ID
	if route.TimeoutPolicy.Request < 0 {
		return fmt.Errorf("route %s: timeout_policy.request must be >= 0", routeID)
	}
	if route.TimeoutPolicy.Idle < 0 {
		return fmt.Errorf("route %s: timeout_policy.idle must be >= 0", routeID)
	}
	if route.TimeoutPolicy.Backend < 0 {
		return fmt.Errorf("route %s: timeout_policy.backend must be >= 0", routeID)
	}
	if route.TimeoutPolicy.HeaderTimeout < 0 {
		return fmt.Errorf("route %s: timeout_policy.header_timeout must be >= 0", routeID)
	}
	if route.TimeoutPolicy.Backend > 0 && route.TimeoutPolicy.Request > 0 && route.TimeoutPolicy.Backend > route.TimeoutPolicy.Request {
		return fmt.Errorf("route %s: timeout_policy.backend must be <= timeout_policy.request", routeID)
	}
	if route.TimeoutPolicy.HeaderTimeout > 0 {
		limit := route.TimeoutPolicy.Backend
		if limit <= 0 {
			limit = route.TimeoutPolicy.Request
		}
		if limit > 0 && route.TimeoutPolicy.HeaderTimeout > limit {
			return fmt.Errorf("route %s: timeout_policy.header_timeout must be <= backend (or request) timeout", routeID)
		}
	}
	return nil
}

func (l *Loader) validateHealthCheckRefs(route RouteConfig, cfg *Config) error {
	routeID := route.ID

	// Per-backend health checks
	for i, b := range route.Backends {
		if b.HealthCheck != nil {
			if err := l.validateHealthCheck(fmt.Sprintf("route %s backend %d", routeID, i), *b.HealthCheck); err != nil {
				return err
			}
		}
	}
	for _, split := range route.TrafficSplit {
		for i, b := range split.Backends {
			if b.HealthCheck != nil {
				if err := l.validateHealthCheck(fmt.Sprintf("route %s traffic_split %s backend %d", routeID, split.Name, i), *b.HealthCheck); err != nil {
					return err
				}
			}
		}
	}
	if route.Versioning.Enabled {
		for ver, vcfg := range route.Versioning.Versions {
			for i, b := range vcfg.Backends {
				if b.HealthCheck != nil {
					if err := l.validateHealthCheck(fmt.Sprintf("route %s version %s backend %d", routeID, ver, i), *b.HealthCheck); err != nil {
						return err
					}
				}
			}
		}
	}

	// Upstream references
	if route.Upstream != "" {
		if _, ok := cfg.Upstreams[route.Upstream]; !ok {
			return fmt.Errorf("route %s: references unknown upstream %q", routeID, route.Upstream)
		}
	}
	for _, split := range route.TrafficSplit {
		if split.Upstream != "" {
			if _, ok := cfg.Upstreams[split.Upstream]; !ok {
				return fmt.Errorf("route %s: traffic_split %s: references unknown upstream %q", routeID, split.Name, split.Upstream)
			}
			if len(split.Backends) > 0 {
				return fmt.Errorf("route %s: traffic_split %s: upstream and backends are mutually exclusive", routeID, split.Name)
			}
		}
	}
	if route.Versioning.Enabled {
		for ver, vcfg := range route.Versioning.Versions {
			if vcfg.Upstream != "" {
				if _, ok := cfg.Upstreams[vcfg.Upstream]; !ok {
					return fmt.Errorf("route %s: versioning.versions[%s]: references unknown upstream %q", routeID, ver, vcfg.Upstream)
				}
				if len(vcfg.Backends) > 0 {
					return fmt.Errorf("route %s: versioning.versions[%s]: upstream and backends are mutually exclusive", routeID, ver)
				}
			}
		}
	}
	if route.Mirror.Enabled && route.Mirror.Upstream != "" {
		if _, ok := cfg.Upstreams[route.Mirror.Upstream]; !ok {
			return fmt.Errorf("route %s: mirror: references unknown upstream %q", routeID, route.Mirror.Upstream)
		}
		if len(route.Mirror.Backends) > 0 {
			return fmt.Errorf("route %s: mirror: upstream and backends are mutually exclusive", routeID)
		}
	}

	return nil
}

func (l *Loader) validateOutlierDetection(route RouteConfig, _ *Config) error {
	if !route.OutlierDetection.Enabled {
		return nil
	}
	routeID := route.ID
	od := route.OutlierDetection
	if od.Interval < 0 {
		return fmt.Errorf("route %s: outlier_detection.interval must be >= 0", routeID)
	}
	if od.Window < 0 {
		return fmt.Errorf("route %s: outlier_detection.window must be >= 0", routeID)
	}
	if od.MinRequests < 0 {
		return fmt.Errorf("route %s: outlier_detection.min_requests must be >= 0", routeID)
	}
	if od.ErrorRateThreshold < 0 || od.ErrorRateThreshold > 1 {
		return fmt.Errorf("route %s: outlier_detection.error_rate_threshold must be between 0.0 and 1.0", routeID)
	}
	if od.ErrorRateMultiplier < 0 {
		return fmt.Errorf("route %s: outlier_detection.error_rate_multiplier must be >= 0", routeID)
	}
	if od.LatencyMultiplier < 0 {
		return fmt.Errorf("route %s: outlier_detection.latency_multiplier must be >= 0", routeID)
	}
	if od.BaseEjectionDuration < 0 {
		return fmt.Errorf("route %s: outlier_detection.base_ejection_duration must be >= 0", routeID)
	}
	if od.MaxEjectionDuration < 0 {
		return fmt.Errorf("route %s: outlier_detection.max_ejection_duration must be >= 0", routeID)
	}
	if od.MaxEjectionDuration > 0 && od.BaseEjectionDuration > 0 && od.MaxEjectionDuration < od.BaseEjectionDuration {
		return fmt.Errorf("route %s: outlier_detection.max_ejection_duration must be >= base_ejection_duration", routeID)
	}
	if od.MaxEjectionPercent < 0 || od.MaxEjectionPercent > 100 {
		return fmt.Errorf("route %s: outlier_detection.max_ejection_percent must be between 0 and 100", routeID)
	}
	return nil
}

func (l *Loader) validateDelegatedSecurity(route RouteConfig, cfg *Config) error {
	scope := fmt.Sprintf("route %s", route.ID)
	if err := l.validateErrorPages(scope, route.ErrorPages); err != nil {
		return err
	}
	if err := l.validateNonceConfig(scope, route.Nonce, cfg.Redis.Address); err != nil {
		return err
	}
	if err := l.validateCSRFConfig(scope, route.CSRF); err != nil {
		return err
	}
	if err := l.validateGeoConfig(scope, route.Geo); err != nil {
		return err
	}
	if err := l.validateIdempotencyConfig(scope, route.Idempotency, cfg.Redis.Address); err != nil {
		return err
	}
	if err := l.validateBackendSigningConfig(scope, route.BackendSigning); err != nil {
		return err
	}
	if route.BotDetection.Enabled {
		if err := l.validateBotDetectionConfig(scope, route.BotDetection); err != nil {
			return err
		}
	}
	if err := l.validateInboundSigningConfig(scope, route.InboundSigning); err != nil {
		return err
	}
	if err := l.validateRequestDedupConfig(scope, route.RequestDedup, cfg.Redis.Address); err != nil {
		return err
	}
	if err := l.validateIPBlocklistConfig(scope, route.IPBlocklist); err != nil {
		return err
	}
	if err := l.validateClientMTLSConfig(scope, route.ClientMTLS); err != nil {
		return err
	}
	return nil
}

func (l *Loader) validateDelegatedMiddleware(route RouteConfig, _ *Config) error {
	scope := fmt.Sprintf("route %s", route.ID)
	if err := l.validateCompressionConfig(scope, route.Compression); err != nil {
		return err
	}
	if err := l.validateDecompressionConfig(scope, route.RequestDecompression); err != nil {
		return err
	}
	if err := l.validateResponseLimitConfig(scope, route.ResponseLimit); err != nil {
		return err
	}
	if err := l.validateSecurityHeadersConfig(scope, route.SecurityHeaders); err != nil {
		return err
	}
	if err := l.validateMaintenanceConfig(scope, route.Maintenance); err != nil {
		return err
	}
	if err := l.validatePIIRedactionConfig(scope, route.PIIRedaction); err != nil {
		return err
	}
	if route.PIIRedaction.Enabled && route.Passthrough {
		return fmt.Errorf("%s: pii_redaction is mutually exclusive with passthrough", scope)
	}
	if err := l.validateFieldEncryptionConfig(scope, route.FieldEncryption); err != nil {
		return err
	}
	if route.FieldEncryption.Enabled && route.Passthrough {
		return fmt.Errorf("%s: field_encryption is mutually exclusive with passthrough", scope)
	}
	if route.Baggage.Enabled {
		if len(route.Baggage.Tags) == 0 {
			return fmt.Errorf("%s: baggage requires at least one tag", scope)
		}
		validPrefixes := []string{"header:", "jwt_claim:", "query:", "cookie:", "static:"}
		for i, tag := range route.Baggage.Tags {
			if tag.Name == "" {
				return fmt.Errorf("%s: baggage.tags[%d].name is required", scope, i)
			}
			if tag.Header == "" {
				return fmt.Errorf("%s: baggage.tags[%d].header is required", scope, i)
			}
			valid := false
			for _, p := range validPrefixes {
				if strings.HasPrefix(tag.Source, p) {
					valid = true
					break
				}
			}
			if !valid {
				return fmt.Errorf("%s: baggage.tags[%d].source must start with header:, jwt_claim:, query:, cookie:, or static:", scope, i)
			}
		}
	}
	if route.Backpressure.Enabled {
		for _, code := range route.Backpressure.StatusCodes {
			if code < 100 || code > 599 {
				return fmt.Errorf("%s: backpressure.status_codes contains invalid code %d (must be 100-599)", scope, code)
			}
		}
		if route.Backpressure.MaxRetryAfter < 0 {
			return fmt.Errorf("%s: backpressure.max_retry_after must be >= 0", scope)
		}
		if route.Backpressure.DefaultDelay < 0 {
			return fmt.Errorf("%s: backpressure.default_delay must be >= 0", scope)
		}
	}
	if route.AuditLog.Enabled {
		if route.AuditLog.WebhookURL == "" {
			return fmt.Errorf("%s: audit_log.webhook_url is required when enabled", scope)
		}
		if route.AuditLog.SampleRate < 0 || route.AuditLog.SampleRate > 1.0 {
			return fmt.Errorf("%s: audit_log.sample_rate must be between 0.0 and 1.0", scope)
		}
		if route.AuditLog.MaxBodySize < 0 {
			return fmt.Errorf("%s: audit_log.max_body_size must be >= 0", scope)
		}
	}
	if err := l.validateWasmPlugins(scope, route); err != nil {
		return err
	}
	return nil
}

func (l *Loader) validateWasmPlugins(scope string, route RouteConfig) error {
	for i, wp := range route.WasmPlugins {
		if !wp.Enabled {
			continue
		}
		if wp.Path == "" {
			return fmt.Errorf("%s: wasm_plugins[%d].path is required", scope, i)
		}
		if _, err := os.Stat(wp.Path); err != nil {
			return fmt.Errorf("%s: wasm_plugins[%d].path: %w", scope, i, err)
		}
		phase := wp.Phase
		if phase == "" {
			phase = "both"
		}
		if phase != "request" && phase != "response" && phase != "both" {
			return fmt.Errorf("%s: wasm_plugins[%d].phase must be 'request', 'response', or 'both'", scope, i)
		}
		if wp.Timeout < 0 {
			return fmt.Errorf("%s: wasm_plugins[%d].timeout must be >= 0", scope, i)
		}
		if wp.PoolSize < 0 {
			return fmt.Errorf("%s: wasm_plugins[%d].pool_size must be >= 0", scope, i)
		}
		if route.Passthrough {
			return fmt.Errorf("%s: wasm_plugins is mutually exclusive with passthrough", scope)
		}
	}
	return nil
}

// validateRouteVersioning validates versioning config for a route.
func (l *Loader) validateRouteVersioning(routeID string, route RouteConfig) error {
	validSources := map[string]bool{"path": true, "header": true, "accept": true, "query": true}
	if !validSources[route.Versioning.Source] {
		return fmt.Errorf("route %s: versioning.source must be path, header, accept, or query", routeID)
	}
	if len(route.Versioning.Versions) == 0 {
		return fmt.Errorf("route %s: versioning.versions must not be empty", routeID)
	}
	if route.Versioning.DefaultVersion == "" {
		return fmt.Errorf("route %s: versioning.default_version is required", routeID)
	}
	if _, ok := route.Versioning.Versions[route.Versioning.DefaultVersion]; !ok {
		return fmt.Errorf("route %s: versioning.default_version %q must exist in versions", routeID, route.Versioning.DefaultVersion)
	}
	for ver, vcfg := range route.Versioning.Versions {
		if len(vcfg.Backends) == 0 && vcfg.Upstream == "" {
			return fmt.Errorf("route %s: versioning.versions[%s] must have at least one backend or upstream", routeID, ver)
		}
		if vcfg.Sunset != "" {
			if _, err := time.Parse("2006-01-02", vcfg.Sunset); err != nil {
				return fmt.Errorf("route %s: versioning.versions[%s].sunset must be YYYY-MM-DD format", routeID, ver)
			}
		}
	}
	if len(route.TrafficSplit) > 0 {
		return fmt.Errorf("route %s: versioning and traffic_split are mutually exclusive", routeID)
	}
	if len(route.Backends) > 0 {
		return fmt.Errorf("route %s: versioning and top-level backends are mutually exclusive", routeID)
	}
	return nil
}

// validateBotDetectionConfig validates bot detection config.
func (l *Loader) validateBotDetectionConfig(scope string, cfg BotDetectionConfig) error {
	for i, p := range cfg.Deny {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("%s: bot_detection.deny[%d]: invalid regex %q: %w", scope, i, p, err)
		}
	}
	for i, p := range cfg.Allow {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("%s: bot_detection.allow[%d]: invalid regex %q: %w", scope, i, p, err)
		}
	}
	if len(cfg.Deny) == 0 {
		return fmt.Errorf("%s: bot_detection.deny requires at least one pattern", scope)
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
	if len(s) == 3 && s[1] == 'x' && s[2] == 'x' {
		base := int(s[0]-'0') * 100
		if base < 100 || base > 500 {
			return [2]int{}, fmt.Errorf("invalid status range %q", s)
		}
		return [2]int{base, base + 99}, nil
	}
	if parts := strings.SplitN(s, "-", 2); len(parts) == 2 {
		lo, err1 := strconv.Atoi(parts[0])
		hi, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || lo < 100 || hi > 599 || lo > hi {
			return [2]int{}, fmt.Errorf("invalid status range %q", s)
		}
		return [2]int{lo, hi}, nil
	}
	code, err := strconv.Atoi(s)
	if err != nil || code < 100 || code > 599 {
		return [2]int{}, fmt.Errorf("invalid status code %q", s)
	}
	return [2]int{code, code}, nil
}

// validateHealthCheck validates a health check configuration.
func (l *Loader) validateHealthCheck(scope string, cfg HealthCheckConfig) error {
	validMethods := map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true, "POST": true}
	if cfg.Method != "" && !validMethods[cfg.Method] {
		return fmt.Errorf("%s: health_check.method must be GET, HEAD, OPTIONS, or POST", scope)
	}
	if cfg.Interval < 0 {
		return fmt.Errorf("%s: health_check.interval must be >= 0", scope)
	}
	if cfg.Timeout < 0 {
		return fmt.Errorf("%s: health_check.timeout must be >= 0", scope)
	}
	if cfg.Timeout > 0 && cfg.Interval > 0 && cfg.Timeout > cfg.Interval {
		return fmt.Errorf("%s: health_check.timeout must be <= health_check.interval", scope)
	}
	if cfg.HealthyAfter < 0 {
		return fmt.Errorf("%s: health_check.healthy_after must be >= 0", scope)
	}
	if cfg.UnhealthyAfter < 0 {
		return fmt.Errorf("%s: health_check.unhealthy_after must be >= 0", scope)
	}
	for _, s := range cfg.ExpectedStatus {
		if _, err := parseStatusRange(s); err != nil {
			return fmt.Errorf("%s: health_check.expected_status: %w", scope, err)
		}
	}
	return nil
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
	for i, op := range cfg.Flatmap {
		switch op.Type {
		case "move":
			if len(op.Args) < 2 {
				return fmt.Errorf("route %s: %s body transform flatmap[%d] 'move' requires 2 args (source, dest)", routeID, phase, i)
			}
		case "del":
			if len(op.Args) < 1 {
				return fmt.Errorf("route %s: %s body transform flatmap[%d] 'del' requires 1 arg (path)", routeID, phase, i)
			}
		case "extract":
			if len(op.Args) < 2 {
				return fmt.Errorf("route %s: %s body transform flatmap[%d] 'extract' requires 2 args (array_path, field_name)", routeID, phase, i)
			}
		case "flatten":
			if len(op.Args) < 1 {
				return fmt.Errorf("route %s: %s body transform flatmap[%d] 'flatten' requires 1 arg (path)", routeID, phase, i)
			}
		case "append":
			if len(op.Args) < 2 {
				return fmt.Errorf("route %s: %s body transform flatmap[%d] 'append' requires at least 2 args (dest, sources...)", routeID, phase, i)
			}
		default:
			return fmt.Errorf("route %s: %s body transform flatmap[%d] unknown type %q (supported: move, del, extract, flatten, append)", routeID, phase, i, op.Type)
		}
	}
	return nil
}

// validateTrafficShaping validates traffic shaping config for a given scope.
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
	if cfg.RequestQueue.Enabled {
		if cfg.RequestQueue.MaxDepth < 0 {
			return fmt.Errorf("%s: request_queue max_depth must be >= 0", scope)
		}
		if cfg.RequestQueue.MaxWait < 0 {
			return fmt.Errorf("%s: request_queue max_wait must be >= 0", scope)
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

		if phase == "response" && terminatingActions[rule.Action] {
			return fmt.Errorf("%s rule %s: terminating action %q is not allowed in response phase", phase, rule.ID, rule.Action)
		}

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
	if cfg.Method != "" && cfg.Service == "" {
		return fmt.Errorf("route %s: grpc.service is required when grpc.method is set", routeID)
	}
	if cfg.Method != "" && len(cfg.Mappings) > 0 {
		return fmt.Errorf("route %s: cannot use both grpc.method and grpc.mappings", routeID)
	}
	if len(cfg.Mappings) == 0 {
		return nil
	}
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

		key := m.HTTPMethod + " " + m.HTTPPath
		if seen[key] {
			return fmt.Errorf("route %s: mapping %d: duplicate mapping for %s", routeID, i, key)
		}
		seen[key] = true
	}

	return nil
}

// validateThriftMappings validates REST-to-Thrift method mappings
func (l *Loader) validateThriftMappings(routeID string, cfg ThriftTranslateConfig) error {
	if cfg.Method != "" && len(cfg.Mappings) > 0 {
		return fmt.Errorf("route %s: cannot use both thrift.method and thrift.mappings", routeID)
	}

	if len(cfg.Mappings) == 0 {
		return nil
	}

	validMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true,
	}

	seen := make(map[string]bool)
	for i, m := range cfg.Mappings {
		if m.HTTPMethod == "" {
			return fmt.Errorf("route %s: thrift mapping %d: http_method is required", routeID, i)
		}
		if !validMethods[m.HTTPMethod] {
			return fmt.Errorf("route %s: thrift mapping %d: invalid http_method: %s", routeID, i, m.HTTPMethod)
		}
		if m.HTTPPath == "" {
			return fmt.Errorf("route %s: thrift mapping %d: http_path is required", routeID, i)
		}
		if m.ThriftMethod == "" {
			return fmt.Errorf("route %s: thrift mapping %d: thrift_method is required", routeID, i)
		}

		key := m.HTTPMethod + " " + m.HTTPPath
		if seen[key] {
			return fmt.Errorf("route %s: thrift mapping %d: duplicate mapping for %s", routeID, i, key)
		}
		seen[key] = true
	}

	return nil
}

// validThriftTypes lists the valid scalar/container type strings for inline schemas.
var validThriftTypes = map[string]bool{
	"bool": true, "byte": true, "i16": true, "i32": true, "i64": true,
	"double": true, "string": true, "binary": true,
	"struct": true, "list": true, "set": true, "map": true,
}

// validateThriftInlineSchema validates inline method/struct/enum definitions.
func (l *Loader) validateThriftInlineSchema(routeID string, cfg ThriftTranslateConfig) error {
	for mname, mdef := range cfg.Methods {
		if mname == "" {
			return fmt.Errorf("route %s: thrift.methods: method name must not be empty", routeID)
		}
		for i, arg := range mdef.Args {
			if err := validateThriftFieldDef(routeID, fmt.Sprintf("methods.%s.args[%d]", mname, i), arg, cfg.Structs, cfg.Enums); err != nil {
				return err
			}
		}
		for i, res := range mdef.Result {
			if err := validateThriftResultFieldDef(routeID, fmt.Sprintf("methods.%s.result[%d]", mname, i), res, cfg.Structs, cfg.Enums); err != nil {
				return err
			}
		}
	}
	for sname, fields := range cfg.Structs {
		if sname == "" {
			return fmt.Errorf("route %s: thrift.structs: struct name must not be empty", routeID)
		}
		for i, f := range fields {
			if err := validateThriftFieldDef(routeID, fmt.Sprintf("structs.%s[%d]", sname, i), f, cfg.Structs, cfg.Enums); err != nil {
				return err
			}
		}
	}
	for ename, vals := range cfg.Enums {
		if ename == "" {
			return fmt.Errorf("route %s: thrift.enums: enum name must not be empty", routeID)
		}
		if len(vals) == 0 {
			return fmt.Errorf("route %s: thrift.enums.%s: must have at least one value", routeID, ename)
		}
	}
	return nil
}

func validateThriftFieldDef(routeID, path string, fd ThriftFieldDef, structs map[string][]ThriftFieldDef, enums map[string]map[string]int) error {
	if fd.ID <= 0 {
		return fmt.Errorf("route %s: thrift.%s: field id must be > 0, got %d", routeID, path, fd.ID)
	}
	return validateThriftFieldDefCommon(routeID, path, fd, structs, enums)
}

func validateThriftResultFieldDef(routeID, path string, fd ThriftFieldDef, structs map[string][]ThriftFieldDef, enums map[string]map[string]int) error {
	if fd.ID < 0 {
		return fmt.Errorf("route %s: thrift.%s: field id must be >= 0, got %d", routeID, path, fd.ID)
	}
	return validateThriftFieldDefCommon(routeID, path, fd, structs, enums)
}

func validateThriftFieldDefCommon(routeID, path string, fd ThriftFieldDef, structs map[string][]ThriftFieldDef, enums map[string]map[string]int) error {
	if fd.Name == "" {
		return fmt.Errorf("route %s: thrift.%s: field name is required", routeID, path)
	}
	if fd.Type == "" {
		return fmt.Errorf("route %s: thrift.%s: field type is required", routeID, path)
	}
	if !validThriftTypes[fd.Type] {
		if enums != nil {
			if _, ok := enums[fd.Type]; ok {
				return nil
			}
		}
		return fmt.Errorf("route %s: thrift.%s: invalid type %q", routeID, path, fd.Type)
	}
	switch fd.Type {
	case "struct":
		if fd.Struct == "" {
			return fmt.Errorf("route %s: thrift.%s: struct type requires 'struct' field name", routeID, path)
		}
		if _, ok := structs[fd.Struct]; !ok {
			return fmt.Errorf("route %s: thrift.%s: unknown struct %q", routeID, path, fd.Struct)
		}
	case "list", "set":
		if fd.Elem == "" {
			return fmt.Errorf("route %s: thrift.%s: %s type requires 'elem' field", routeID, path, fd.Type)
		}
	case "map":
		if fd.Key == "" {
			return fmt.Errorf("route %s: thrift.%s: map type requires 'key' field", routeID, path)
		}
		if fd.Value == "" {
			return fmt.Errorf("route %s: thrift.%s: map type requires 'value' field", routeID, path)
		}
	}
	return nil
}

// validateMatchConfig validates the match configuration for a route.
func (l *Loader) validateMatchConfig(routeID string, mc MatchConfig) error {
	for _, domain := range mc.Domains {
		if domain == "" {
			return fmt.Errorf("route %s: match domain must not be empty", routeID)
		}
		if strings.Contains(domain, "*") && !strings.HasPrefix(domain, "*.") {
			return fmt.Errorf("route %s: match domain wildcard must be a prefix '*.', got: %s", routeID, domain)
		}
	}

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

	for i, c := range mc.Cookies {
		if c.Name == "" {
			return fmt.Errorf("route %s: match cookie %d: name is required", routeID, i)
		}
		count := 0
		if c.Value != "" {
			count++
		}
		if c.Present != nil {
			count++
		}
		if c.Regex != "" {
			count++
		}
		if count != 1 {
			return fmt.Errorf("route %s: match cookie %q: must set exactly one of value, present, or regex", routeID, c.Name)
		}
		if c.Regex != "" {
			if _, err := regexp.Compile(c.Regex); err != nil {
				return fmt.Errorf("route %s: match cookie %q: invalid regex: %w", routeID, c.Name, err)
			}
		}
	}

	for i, b := range mc.Body {
		if b.Name == "" {
			return fmt.Errorf("route %s: match body %d: name is required", routeID, i)
		}
		count := 0
		if b.Value != "" {
			count++
		}
		if b.Present != nil {
			count++
		}
		if b.Regex != "" {
			count++
		}
		if count != 1 {
			return fmt.Errorf("route %s: match body %q: must set exactly one of value, present, or regex", routeID, b.Name)
		}
		if b.Regex != "" {
			if _, err := regexp.Compile(b.Regex); err != nil {
				return fmt.Errorf("route %s: match body %q: invalid regex: %w", routeID, b.Name, err)
			}
		}
	}

	if mc.MaxMatchBodySize < 0 {
		return fmt.Errorf("route %s: max_match_body_size must be >= 0", routeID)
	}

	return nil
}

// validateErrorPages validates an ErrorPagesConfig for a given scope.
func (l *Loader) validateErrorPages(scope string, cfg ErrorPagesConfig) error {
	if !cfg.IsActive() {
		return nil
	}
	validKeyPattern := regexp.MustCompile(`^(\d{3}|[1-5]xx|default)$`)
	for key, entry := range cfg.Pages {
		if !validKeyPattern.MatchString(key) {
			return fmt.Errorf("%s: error_pages key %q is invalid (must be a status code, Nxx class, or \"default\")", scope, key)
		}
		if len(key) == 3 && key != "def" {
			if code, err := strconv.Atoi(key); err == nil {
				if code < 100 || code > 599 {
					return fmt.Errorf("%s: error_pages key %q: status code must be between 100 and 599", scope, key)
				}
			}
		}
		if entry.HTML != "" && entry.HTMLFile != "" {
			return fmt.Errorf("%s: error_pages[%s]: html and html_file are mutually exclusive", scope, key)
		}
		if entry.JSON != "" && entry.JSONFile != "" {
			return fmt.Errorf("%s: error_pages[%s]: json and json_file are mutually exclusive", scope, key)
		}
		if entry.XML != "" && entry.XMLFile != "" {
			return fmt.Errorf("%s: error_pages[%s]: xml and xml_file are mutually exclusive", scope, key)
		}
		if entry.HTML == "" && entry.HTMLFile == "" &&
			entry.JSON == "" && entry.JSONFile == "" &&
			entry.XML == "" && entry.XMLFile == "" {
			return fmt.Errorf("%s: error_pages[%s]: at least one format (html, json, or xml) is required", scope, key)
		}
		for _, tpl := range []struct {
			name, content string
		}{
			{"html", entry.HTML},
			{"json", entry.JSON},
			{"xml", entry.XML},
		} {
			if tpl.content != "" {
				if _, err := template.New("").Parse(tpl.content); err != nil {
					return fmt.Errorf("%s: error_pages[%s].%s: invalid template: %w", scope, key, tpl.name, err)
				}
			}
		}
		for _, fp := range []struct {
			name, path string
		}{
			{"html_file", entry.HTMLFile},
			{"json_file", entry.JSONFile},
			{"xml_file", entry.XMLFile},
		} {
			if fp.path != "" {
				if _, err := os.Stat(fp.path); err != nil {
					return fmt.Errorf("%s: error_pages[%s].%s: %w", scope, key, fp.name, err)
				}
			}
		}
	}
	return nil
}

// validateNonceConfig validates a nonce config for a given scope.
func (l *Loader) validateNonceConfig(scope string, cfg NonceConfig, redisAddr string) error {
	if !cfg.Enabled {
		return nil
	}
	switch cfg.Mode {
	case "", "local", "distributed":
		// valid
	default:
		return fmt.Errorf("%s: nonce.mode must be \"local\" or \"distributed\"", scope)
	}
	switch cfg.Scope {
	case "", "global", "per_client":
		// valid
	default:
		return fmt.Errorf("%s: nonce.scope must be \"global\" or \"per_client\"", scope)
	}
	if cfg.TTL < 0 {
		return fmt.Errorf("%s: nonce.ttl must be >= 0", scope)
	}
	if cfg.MaxAge < 0 {
		return fmt.Errorf("%s: nonce.max_age must be >= 0", scope)
	}
	if cfg.MaxAge > 0 && cfg.TimestampHeader == "" {
		return fmt.Errorf("%s: nonce.max_age requires timestamp_header to be set", scope)
	}
	if cfg.Mode == "distributed" && redisAddr == "" {
		return fmt.Errorf("%s: nonce.mode \"distributed\" requires redis.address to be configured", scope)
	}
	return nil
}

// validateCSRFConfig validates a CSRF config for a given scope.
func (l *Loader) validateCSRFConfig(scope string, cfg CSRFConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Secret == "" {
		return fmt.Errorf("%s: csrf.secret is required when csrf is enabled", scope)
	}
	switch strings.ToLower(cfg.CookieSameSite) {
	case "", "strict", "lax", "none":
		// valid
	default:
		return fmt.Errorf("%s: csrf.cookie_samesite must be \"strict\", \"lax\", or \"none\"", scope)
	}
	if strings.ToLower(cfg.CookieSameSite) == "none" && !cfg.CookieSecure {
		return fmt.Errorf("%s: csrf.cookie_secure must be true when cookie_samesite is \"none\"", scope)
	}
	if cfg.TokenTTL < 0 {
		return fmt.Errorf("%s: csrf.token_ttl must be >= 0", scope)
	}
	for _, p := range cfg.AllowedOriginPatterns {
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("%s: csrf.allowed_origin_patterns: invalid regex %q: %w", scope, p, err)
		}
	}
	return nil
}

// validateGeoConfig validates a geo config for a given scope.
func (l *Loader) validateGeoConfig(scope string, cfg GeoConfig) error {
	switch cfg.Order {
	case "", "allow_first", "deny_first":
		// valid
	default:
		return fmt.Errorf("%s: geo.order must be \"allow_first\" or \"deny_first\"", scope)
	}
	return nil
}

// validateIdempotencyConfig validates an idempotency config for a given scope.
func (l *Loader) validateIdempotencyConfig(scope string, cfg IdempotencyConfig, redisAddr string) error {
	if !cfg.Enabled {
		return nil
	}
	switch cfg.Mode {
	case "", "local", "distributed":
		// valid
	default:
		return fmt.Errorf("%s: idempotency.mode must be \"local\" or \"distributed\"", scope)
	}
	switch cfg.KeyScope {
	case "", "global", "per_client":
		// valid
	default:
		return fmt.Errorf("%s: idempotency.key_scope must be \"global\" or \"per_client\"", scope)
	}
	if cfg.TTL < 0 {
		return fmt.Errorf("%s: idempotency.ttl must be >= 0", scope)
	}
	if cfg.MaxKeyLength < 0 {
		return fmt.Errorf("%s: idempotency.max_key_length must be >= 0", scope)
	}
	if cfg.MaxBodySize < 0 {
		return fmt.Errorf("%s: idempotency.max_body_size must be >= 0", scope)
	}
	for _, m := range cfg.Methods {
		if !validHTTPMethods[m] {
			return fmt.Errorf("%s: idempotency.methods contains invalid HTTP method: %s", scope, m)
		}
	}
	if cfg.Mode == "distributed" && redisAddr == "" {
		return fmt.Errorf("%s: idempotency.mode \"distributed\" requires redis.address to be configured", scope)
	}
	return nil
}

// validateBackendSigningConfig validates a backend signing config for a given scope.
func (l *Loader) validateBackendSigningConfig(scope string, cfg BackendSigningConfig) error {
	if !cfg.Enabled {
		return nil
	}
	switch cfg.Algorithm {
	case "", "hmac-sha256", "hmac-sha512":
		// valid
	default:
		return fmt.Errorf("%s: backend_signing.algorithm must be \"hmac-sha256\" or \"hmac-sha512\"", scope)
	}
	if cfg.Secret == "" {
		return fmt.Errorf("%s: backend_signing.secret is required", scope)
	}
	decoded, err := base64.StdEncoding.DecodeString(cfg.Secret)
	if err != nil {
		return fmt.Errorf("%s: backend_signing.secret must be valid base64: %v", scope, err)
	}
	if len(decoded) < 32 {
		return fmt.Errorf("%s: backend_signing.secret must decode to at least 32 bytes (got %d)", scope, len(decoded))
	}
	if cfg.KeyID == "" {
		return fmt.Errorf("%s: backend_signing.key_id is required", scope)
	}
	for _, h := range cfg.SignedHeaders {
		if strings.ContainsAny(h, " \t\r\n") {
			return fmt.Errorf("%s: backend_signing.signed_headers contains header with whitespace: %q", scope, h)
		}
	}
	if strings.ContainsAny(cfg.HeaderPrefix, " \t\r\n") {
		return fmt.Errorf("%s: backend_signing.header_prefix must not contain whitespace", scope)
	}
	return nil
}

func (l *Loader) validateInboundSigningConfig(scope string, cfg InboundSigningConfig) error {
	if !cfg.Enabled {
		return nil
	}
	switch cfg.Algorithm {
	case "", "hmac-sha256", "hmac-sha512":
		// valid
	default:
		return fmt.Errorf("%s: inbound_signing.algorithm must be \"hmac-sha256\" or \"hmac-sha512\"", scope)
	}
	if cfg.Secret == "" {
		return fmt.Errorf("%s: inbound_signing.secret is required", scope)
	}
	decoded, err := base64.StdEncoding.DecodeString(cfg.Secret)
	if err != nil {
		return fmt.Errorf("%s: inbound_signing.secret must be valid base64: %v", scope, err)
	}
	if len(decoded) < 32 {
		return fmt.Errorf("%s: inbound_signing.secret must decode to at least 32 bytes (got %d)", scope, len(decoded))
	}
	return nil
}

func (l *Loader) validatePIIRedactionConfig(scope string, cfg PIIRedactionConfig) error {
	if !cfg.Enabled {
		return nil
	}
	validBuiltIns := map[string]bool{"email": true, "credit_card": true, "ssn": true, "phone": true}
	for _, name := range cfg.BuiltIns {
		if !validBuiltIns[name] {
			return fmt.Errorf("%s: pii_redaction.built_ins: unknown pattern %q (must be email, credit_card, ssn, phone)", scope, name)
		}
	}
	for i, custom := range cfg.Custom {
		if _, err := regexp.Compile(custom.Pattern); err != nil {
			return fmt.Errorf("%s: pii_redaction.custom[%d]: invalid pattern: %w", scope, i, err)
		}
	}
	if cfg.Scope != "" && cfg.Scope != "response" && cfg.Scope != "request" && cfg.Scope != "both" {
		return fmt.Errorf("%s: pii_redaction.scope must be \"response\", \"request\", or \"both\"", scope)
	}
	if len(cfg.BuiltIns) == 0 && len(cfg.Custom) == 0 {
		return fmt.Errorf("%s: pii_redaction requires at least one of built_ins or custom patterns", scope)
	}
	return nil
}

func (l *Loader) validateFieldEncryptionConfig(scope string, cfg FieldEncryptionConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Algorithm != "" && cfg.Algorithm != "aes-gcm-256" {
		return fmt.Errorf("%s: field_encryption.algorithm must be \"aes-gcm-256\"", scope)
	}
	if cfg.KeyBase64 == "" {
		return fmt.Errorf("%s: field_encryption.key_base64 is required", scope)
	}
	decoded, err := base64.StdEncoding.DecodeString(cfg.KeyBase64)
	if err != nil {
		return fmt.Errorf("%s: field_encryption.key_base64 must be valid base64: %v", scope, err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("%s: field_encryption.key_base64 must decode to exactly 32 bytes (got %d)", scope, len(decoded))
	}
	if len(cfg.EncryptFields) == 0 && len(cfg.DecryptFields) == 0 {
		return fmt.Errorf("%s: field_encryption requires at least one of encrypt_fields or decrypt_fields", scope)
	}
	if cfg.Encoding != "" && cfg.Encoding != "base64" && cfg.Encoding != "hex" {
		return fmt.Errorf("%s: field_encryption.encoding must be \"base64\" or \"hex\"", scope)
	}
	return nil
}

// validateTransportConfig validates a transport config for a given scope.
func (l *Loader) validateTransportConfig(scope string, cfg TransportConfig) error {
	if cfg.MaxIdleConns < 0 {
		return fmt.Errorf("%s: transport.max_idle_conns must be >= 0", scope)
	}
	if cfg.MaxIdleConnsPerHost < 0 {
		return fmt.Errorf("%s: transport.max_idle_conns_per_host must be >= 0", scope)
	}
	if cfg.MaxConnsPerHost < 0 {
		return fmt.Errorf("%s: transport.max_conns_per_host must be >= 0", scope)
	}
	if cfg.IdleConnTimeout < 0 {
		return fmt.Errorf("%s: transport.idle_conn_timeout must be >= 0", scope)
	}
	if cfg.DialTimeout < 0 {
		return fmt.Errorf("%s: transport.dial_timeout must be >= 0", scope)
	}
	if cfg.TLSHandshakeTimeout < 0 {
		return fmt.Errorf("%s: transport.tls_handshake_timeout must be >= 0", scope)
	}
	if cfg.ResponseHeaderTimeout < 0 {
		return fmt.Errorf("%s: transport.response_header_timeout must be >= 0", scope)
	}
	if cfg.ExpectContinueTimeout < 0 {
		return fmt.Errorf("%s: transport.expect_continue_timeout must be >= 0", scope)
	}
	if cfg.CAFile != "" {
		if _, err := os.Stat(cfg.CAFile); os.IsNotExist(err) {
			return fmt.Errorf("%s: transport.ca_file does not exist: %s", scope, cfg.CAFile)
		}
	}
	if (cfg.CertFile != "") != (cfg.KeyFile != "") {
		return fmt.Errorf("%s: transport.cert_file and transport.key_file must both be set", scope)
	}
	if cfg.CertFile != "" {
		if _, err := os.Stat(cfg.CertFile); os.IsNotExist(err) {
			return fmt.Errorf("%s: transport.cert_file does not exist: %s", scope, cfg.CertFile)
		}
	}
	if cfg.KeyFile != "" {
		if _, err := os.Stat(cfg.KeyFile); os.IsNotExist(err) {
			return fmt.Errorf("%s: transport.key_file does not exist: %s", scope, cfg.KeyFile)
		}
	}
	if cfg.EnableHTTP3 != nil && *cfg.EnableHTTP3 && cfg.ForceHTTP2 != nil && *cfg.ForceHTTP2 {
		return fmt.Errorf("%s: transport.enable_http3 and transport.force_http2 are mutually exclusive", scope)
	}
	return nil
}

// validCompressionAlgorithms is the set of supported compression algorithms.
var validCompressionAlgorithms = map[string]bool{
	"gzip": true,
	"br":   true,
	"zstd": true,
}

// validateCompressionConfig validates a compression config for a given scope.
func (l *Loader) validateCompressionConfig(scope string, cfg CompressionConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Level < 0 || cfg.Level > 11 {
		return fmt.Errorf("%s: compression.level must be 0-11", scope)
	}
	if cfg.MinSize < 0 {
		return fmt.Errorf("%s: compression.min_size must be >= 0", scope)
	}
	for _, algo := range cfg.Algorithms {
		if !validCompressionAlgorithms[algo] {
			return fmt.Errorf("%s: compression.algorithms: unsupported algorithm %q (valid: gzip, br, zstd)", scope, algo)
		}
	}
	return nil
}

// validDecompressionAlgorithms is the set of supported request decompression algorithms.
var validDecompressionAlgorithms = map[string]bool{
	"gzip":    true,
	"deflate": true,
	"br":      true,
	"zstd":    true,
}

// validateDecompressionConfig validates a request decompression config for a given scope.
func (l *Loader) validateDecompressionConfig(scope string, cfg RequestDecompressionConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.MaxDecompressedSize < 0 {
		return fmt.Errorf("%s: request_decompression.max_decompressed_size must be >= 0", scope)
	}
	for _, algo := range cfg.Algorithms {
		if !validDecompressionAlgorithms[algo] {
			return fmt.Errorf("%s: request_decompression.algorithms: unsupported algorithm %q (valid: gzip, deflate, br, zstd)", scope, algo)
		}
	}
	return nil
}

// validResponseLimitActions is the set of valid response_limit.action values.
var validResponseLimitActions = map[string]bool{
	"reject":   true,
	"truncate": true,
	"log_only": true,
}

// validateResponseLimitConfig validates a response limit config.
func (l *Loader) validateResponseLimitConfig(scope string, cfg ResponseLimitConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.MaxSize < 0 {
		return fmt.Errorf("%s: response_limit.max_size must be >= 0", scope)
	}
	if cfg.MaxSize == 0 {
		return fmt.Errorf("%s: response_limit.max_size must be set when enabled", scope)
	}
	if cfg.Action != "" && !validResponseLimitActions[cfg.Action] {
		return fmt.Errorf("%s: response_limit.action must be one of: reject, truncate, log_only", scope)
	}
	return nil
}

// validateSecurityHeadersConfig validates a security headers config.
func (l *Loader) validateSecurityHeadersConfig(scope string, cfg SecurityHeadersConfig) error {
	if !cfg.Enabled {
		return nil
	}
	for name := range cfg.CustomHeaders {
		if name == "" {
			return fmt.Errorf("%s: security_headers.custom_headers: header name must not be empty", scope)
		}
	}
	return nil
}

// validateMaintenanceConfig validates a maintenance config.
func (l *Loader) validateMaintenanceConfig(scope string, cfg MaintenanceConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.StatusCode != 0 && (cfg.StatusCode < 100 || cfg.StatusCode > 599) {
		return fmt.Errorf("%s: maintenance.status_code must be a valid HTTP status (100-599)", scope)
	}
	for _, cidr := range cfg.ExcludeIPs {
		if strings.Contains(cidr, "/") {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return fmt.Errorf("%s: maintenance.exclude_ips: invalid CIDR %q: %w", scope, cidr, err)
			}
		} else {
			if ip := net.ParseIP(cidr); ip == nil {
				return fmt.Errorf("%s: maintenance.exclude_ips: invalid IP %q", scope, cidr)
			}
		}
	}
	return nil
}

// validateTrustedProxiesConfig validates the trusted proxies config.
func (l *Loader) validateTrustedProxiesConfig(cfg TrustedProxiesConfig) error {
	for _, cidr := range cfg.CIDRs {
		if strings.Contains(cidr, "/") {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return fmt.Errorf("trusted_proxies.cidrs: invalid CIDR %q: %w", cidr, err)
			}
		} else {
			if ip := net.ParseIP(cidr); ip == nil {
				return fmt.Errorf("trusted_proxies.cidrs: invalid IP %q", cidr)
			}
		}
	}
	if cfg.MaxHops < 0 {
		return fmt.Errorf("trusted_proxies.max_hops must be >= 0")
	}
	return nil
}

// validateShutdownConfig validates the graceful shutdown config.
func (l *Loader) validateShutdownConfig(cfg ShutdownConfig) error {
	if cfg.Timeout < 0 {
		return fmt.Errorf("shutdown.timeout must be >= 0")
	}
	if cfg.DrainDelay < 0 {
		return fmt.Errorf("shutdown.drain_delay must be >= 0")
	}
	if cfg.Timeout > 0 && cfg.DrainDelay > 0 && cfg.DrainDelay >= cfg.Timeout {
		return fmt.Errorf("shutdown.drain_delay (%s) must be less than shutdown.timeout (%s)", cfg.DrainDelay, cfg.Timeout)
	}
	return nil
}

// validateRewriteConfig validates URL rewrite settings for a route.
func (l *Loader) validateRewriteConfig(routeID string, rc RewriteConfig, pathPrefix, stripPrefix bool) error {
	hasPrefix := rc.Prefix != ""
	hasRegex := rc.Regex != ""
	hasReplacement := rc.Replacement != ""

	if !hasPrefix && !hasRegex && !hasReplacement && rc.Host == "" {
		return nil
	}

	if hasPrefix && hasRegex {
		return fmt.Errorf("route %s: rewrite.prefix and rewrite.regex are mutually exclusive", routeID)
	}

	if hasPrefix && !pathPrefix {
		return fmt.Errorf("route %s: rewrite.prefix requires path_prefix: true", routeID)
	}

	if hasPrefix && stripPrefix {
		return fmt.Errorf("route %s: rewrite.prefix and strip_prefix are mutually exclusive", routeID)
	}

	if hasRegex && !hasReplacement {
		return fmt.Errorf("route %s: rewrite.regex requires rewrite.replacement", routeID)
	}
	if hasReplacement && !hasRegex {
		return fmt.Errorf("route %s: rewrite.replacement requires rewrite.regex", routeID)
	}

	if hasRegex {
		if _, err := regexp.Compile(rc.Regex); err != nil {
			return fmt.Errorf("route %s: rewrite.regex is invalid: %w", routeID, err)
		}
	}

	return nil
}

// validateSSRFProtectionConfig validates the global SSRF protection config.
func (l *Loader) validateSSRFProtectionConfig(cfg SSRFProtectionConfig) error {
	if !cfg.Enabled {
		return nil
	}
	for i, cidr := range cfg.AllowCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("ssrf_protection.allow_cidrs[%d]: invalid CIDR %q: %w", i, cidr, err)
		}
	}
	return nil
}

// validateRequestDedupConfig validates request dedup config for a given scope.
func (l *Loader) validateRequestDedupConfig(scope string, cfg RequestDedupConfig, redisAddr string) error {
	if !cfg.Enabled {
		return nil
	}
	switch cfg.Mode {
	case "", "local", "distributed":
		// valid
	default:
		return fmt.Errorf("%s: request_dedup.mode must be \"local\" or \"distributed\", got %q", scope, cfg.Mode)
	}
	if cfg.TTL < 0 {
		return fmt.Errorf("%s: request_dedup.ttl must be >= 0", scope)
	}
	if cfg.MaxBodySize < 0 {
		return fmt.Errorf("%s: request_dedup.max_body_size must be >= 0", scope)
	}
	if cfg.Mode == "distributed" && redisAddr == "" {
		return fmt.Errorf("%s: request_dedup.mode \"distributed\" requires redis.address to be configured", scope)
	}
	return nil
}

// validateIPBlocklistConfig validates IP blocklist config for a given scope.
func (l *Loader) validateIPBlocklistConfig(scope string, cfg IPBlocklistConfig) error {
	if !cfg.Enabled {
		return nil
	}
	switch cfg.Action {
	case "", "block", "log":
		// valid
	default:
		return fmt.Errorf("%s: ip_blocklist.action must be \"block\" or \"log\", got %q", scope, cfg.Action)
	}
	for i, entry := range cfg.Static {
		if ip := net.ParseIP(entry); ip == nil {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return fmt.Errorf("%s: ip_blocklist.static[%d]: %q is not a valid IP or CIDR", scope, i, entry)
			}
		}
	}
	for i, feed := range cfg.Feeds {
		if feed.URL == "" {
			return fmt.Errorf("%s: ip_blocklist.feeds[%d]: url is required", scope, i)
		}
		switch feed.Format {
		case "", "text", "json":
			// valid
		default:
			return fmt.Errorf("%s: ip_blocklist.feeds[%d]: format must be \"text\" or \"json\", got %q", scope, i, feed.Format)
		}
		if feed.RefreshInterval > 0 && feed.RefreshInterval < time.Second {
			return fmt.Errorf("%s: ip_blocklist.feeds[%d]: refresh_interval must be >= 1s", scope, i)
		}
	}
	return nil
}

// validateClientMTLSConfig validates client mTLS config for a given scope.
func (l *Loader) validateClientMTLSConfig(scope string, cfg ClientMTLSConfig) error {
	if !cfg.Enabled {
		return nil
	}
	switch cfg.ClientAuth {
	case "", "request", "require", "verify":
		// valid
	default:
		return fmt.Errorf("%s: client_mtls.client_auth must be \"request\", \"require\", or \"verify\", got %q", scope, cfg.ClientAuth)
	}
	mode := cfg.ClientAuth
	if mode == "" {
		mode = "verify"
	}
	if mode == "verify" {
		if cfg.ClientCAFile == "" && len(cfg.ClientCAs) == 0 {
			return fmt.Errorf("%s: client_mtls: verify mode requires client_ca_file or client_cas", scope)
		}
	}
	if cfg.ClientCAFile != "" {
		if _, err := os.Stat(cfg.ClientCAFile); err != nil {
			return fmt.Errorf("%s: client_mtls.client_ca_file: %w", scope, err)
		}
	}
	for i, f := range cfg.ClientCAs {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("%s: client_mtls.client_cas[%d]: %w", scope, i, err)
		}
	}
	return nil
}

// validateGRPCToRESTMappings validates gRPC-to-REST method mappings.
func (l *Loader) validateGRPCToRESTMappings(routeID string, cfg RESTTranslateConfig) error {
	validMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true,
	}

	seen := make(map[string]bool)
	for i, m := range cfg.Mappings {
		if m.GRPCService == "" {
			return fmt.Errorf("route %s: grpc_to_rest mapping %d: grpc_service is required", routeID, i)
		}
		if m.GRPCMethod == "" {
			return fmt.Errorf("route %s: grpc_to_rest mapping %d: grpc_method is required", routeID, i)
		}
		if m.HTTPMethod == "" {
			return fmt.Errorf("route %s: grpc_to_rest mapping %d: http_method is required", routeID, i)
		}
		if !validMethods[m.HTTPMethod] {
			return fmt.Errorf("route %s: grpc_to_rest mapping %d: invalid http_method: %s", routeID, i, m.HTTPMethod)
		}
		if m.HTTPPath == "" {
			return fmt.Errorf("route %s: grpc_to_rest mapping %d: http_path is required", routeID, i)
		}

		key := "/" + m.GRPCService + "/" + m.GRPCMethod
		if seen[key] {
			return fmt.Errorf("route %s: grpc_to_rest mapping %d: duplicate mapping for %s", routeID, i, key)
		}
		seen[key] = true
	}

	return nil
}

func (l *Loader) validateTenants(tc TenantsConfig, routeIDs map[string]bool) error {
	if tc.Key == "" {
		return fmt.Errorf("tenants: key is required when enabled")
	}
	validKey := false
	if tc.Key == "client_id" {
		validKey = true
	}
	if strings.HasPrefix(tc.Key, "header:") && len(tc.Key) > len("header:") {
		validKey = true
	}
	if strings.HasPrefix(tc.Key, "jwt_claim:") && len(tc.Key) > len("jwt_claim:") {
		validKey = true
	}
	if !validKey {
		return fmt.Errorf("tenants: key must be 'client_id', 'header:<name>', or 'jwt_claim:<name>'")
	}
	if len(tc.Tenants) == 0 {
		return fmt.Errorf("tenants: at least one tenant must be defined")
	}
	if tc.DefaultTenant != "" {
		if _, ok := tc.Tenants[tc.DefaultTenant]; !ok {
			return fmt.Errorf("tenants: default_tenant %q not found in tenants map", tc.DefaultTenant)
		}
	}
	// Validate tiers
	for tierName, tier := range tc.Tiers {
		if tier.MaxBodySize < 0 {
			return fmt.Errorf("tenants.tiers[%s]: max_body_size must be >= 0", tierName)
		}
		if tier.Priority < 0 || tier.Priority > 10 {
			return fmt.Errorf("tenants.tiers[%s]: priority must be between 0 and 10", tierName)
		}
		if tier.Timeout < 0 {
			return fmt.Errorf("tenants.tiers[%s]: timeout must be >= 0", tierName)
		}
		if tier.RateLimit != nil && tier.RateLimit.Rate <= 0 {
			return fmt.Errorf("tenants.tiers[%s]: rate_limit.rate must be > 0", tierName)
		}
		if tier.Quota != nil {
			validPeriods := map[string]bool{"hourly": true, "daily": true, "monthly": true, "yearly": true}
			if tier.Quota.Limit <= 0 {
				return fmt.Errorf("tenants.tiers[%s]: quota.limit must be > 0", tierName)
			}
			if !validPeriods[tier.Quota.Period] {
				return fmt.Errorf("tenants.tiers[%s]: quota.period must be hourly, daily, monthly, or yearly", tierName)
			}
		}
		for k := range tier.ResponseHeaders {
			if k == "" {
				return fmt.Errorf("tenants.tiers[%s]: response_headers contains empty header name", tierName)
			}
		}
	}

	validPeriods := map[string]bool{"hourly": true, "daily": true, "monthly": true, "yearly": true}
	for name, t := range tc.Tenants {
		// Validate tier reference exists
		if t.Tier != "" {
			if _, ok := tc.Tiers[t.Tier]; !ok {
				return fmt.Errorf("tenants[%s]: references unknown tier %q", name, t.Tier)
			}
		}
		if t.RateLimit != nil && t.RateLimit.Rate <= 0 {
			return fmt.Errorf("tenants[%s]: rate_limit.rate must be > 0", name)
		}
		if t.Quota != nil {
			if t.Quota.Limit <= 0 {
				return fmt.Errorf("tenants[%s]: quota.limit must be > 0", name)
			}
			if !validPeriods[t.Quota.Period] {
				return fmt.Errorf("tenants[%s]: quota.period must be hourly, daily, monthly, or yearly", name)
			}
		}
		if t.MaxBodySize < 0 {
			return fmt.Errorf("tenants[%s]: max_body_size must be >= 0", name)
		}
		if t.Priority < 0 || t.Priority > 10 {
			return fmt.Errorf("tenants[%s]: priority must be between 0 and 10", name)
		}
		if t.Timeout < 0 {
			return fmt.Errorf("tenants[%s]: timeout must be >= 0", name)
		}
		for k := range t.ResponseHeaders {
			if k == "" {
				return fmt.Errorf("tenants[%s]: response_headers contains empty header name", name)
			}
		}
		for _, rid := range t.Routes {
			if !routeIDs[rid] {
				return fmt.Errorf("tenants[%s]: references unknown route %q", name, rid)
			}
		}
	}
	return nil
}

func (l *Loader) validateTenantBackends(route RouteConfig, cfg *Config) error {
	if len(route.TenantBackends) == 0 {
		return nil
	}
	for tid, backends := range route.TenantBackends {
		if !cfg.Tenants.Enabled {
			return fmt.Errorf("route %s: tenant_backends requires tenants.enabled", route.ID)
		}
		if _, ok := cfg.Tenants.Tenants[tid]; !ok {
			return fmt.Errorf("route %s: tenant_backends references unknown tenant %q", route.ID, tid)
		}
		if len(backends) == 0 {
			return fmt.Errorf("route %s: tenant_backends[%s] must have at least one backend", route.ID, tid)
		}
		for i, b := range backends {
			if b.URL == "" {
				return fmt.Errorf("route %s: tenant_backends[%s][%d] missing url", route.ID, tid, i)
			}
		}
	}
	return nil
}
