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

// validateRoute validates a single route configuration.
func (l *Loader) validateRoute(route RouteConfig, cfg *Config) error {
	routeID := route.ID
	scope := fmt.Sprintf("route %s", routeID)

	// === Basic requirements ===
	// Must have either backends, service discovery, versioning, upstream ref, echo, static, sequential, or aggregate
	if len(route.Backends) == 0 && route.Service.Name == "" && !route.Versioning.Enabled && route.Upstream == "" && !route.Echo && !route.Static.Enabled && !route.Sequential.Enabled && !route.Aggregate.Enabled {
		return fmt.Errorf("route %s: must have either backends, service name, or upstream", routeID)
	}

	// === Echo mutual exclusions ===
	if route.Echo {
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
	}

	// === Mock response ===
	if route.MockResponse.Enabled {
		if route.MockResponse.StatusCode != 0 && (route.MockResponse.StatusCode < 100 || route.MockResponse.StatusCode > 599) {
			return fmt.Errorf("route %s: mock_response.status_code must be 100-599", routeID)
		}
	}

	// === Backend auth ===
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

	// === Status mapping ===
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

	// === Static file serving ===
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
	}

	// === Passthrough mutual exclusions ===
	if route.Passthrough {
		if route.Transform.Request.Body.IsActive() || route.Transform.Response.Body.IsActive() {
			return fmt.Errorf("route %s: passthrough is mutually exclusive with body transforms", routeID)
		}
		if route.Validation.Enabled {
			return fmt.Errorf("route %s: passthrough is mutually exclusive with validation", routeID)
		}
		if route.Compression.Enabled {
			return fmt.Errorf("route %s: passthrough is mutually exclusive with compression", routeID)
		}
		if route.Cache.Enabled {
			return fmt.Errorf("route %s: passthrough is mutually exclusive with cache", routeID)
		}
		if route.GraphQL.Enabled {
			return fmt.Errorf("route %s: passthrough is mutually exclusive with graphql", routeID)
		}
		if route.OpenAPI.SpecFile != "" || route.OpenAPI.SpecID != "" {
			return fmt.Errorf("route %s: passthrough is mutually exclusive with openapi", routeID)
		}
		if route.RequestDecompression.Enabled {
			return fmt.Errorf("route %s: passthrough is mutually exclusive with request_decompression", routeID)
		}
		if route.ResponseLimit.Enabled {
			return fmt.Errorf("route %s: passthrough is mutually exclusive with response_limit", routeID)
		}
	}

	// === Spike arrest ===
	if route.SpikeArrest.Enabled && route.SpikeArrest.Rate <= 0 {
		return fmt.Errorf("route %s: spike_arrest rate must be > 0 when enabled", routeID)
	}

	// === Content replacer ===
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
	if route.Passthrough && route.ContentReplacer.Enabled {
		return fmt.Errorf("route %s: passthrough is mutually exclusive with content_replacer", routeID)
	}

	// === Follow redirects ===
	if route.FollowRedirects.Enabled && route.FollowRedirects.MaxRedirects < 0 {
		return fmt.Errorf("route %s: follow_redirects max_redirects must be >= 0", routeID)
	}

	// === Body generator ===
	if route.BodyGenerator.Enabled {
		if route.BodyGenerator.Template == "" {
			return fmt.Errorf("route %s: body_generator requires a template", routeID)
		}
		if route.Passthrough {
			return fmt.Errorf("route %s: body_generator is mutually exclusive with passthrough", routeID)
		}
	}

	// === Sequential proxy ===
	if route.Sequential.Enabled {
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
		if route.Passthrough {
			return fmt.Errorf("route %s: sequential is mutually exclusive with passthrough", routeID)
		}
	}

	// === Quota ===
	if route.Quota.Enabled {
		if route.Quota.Limit <= 0 {
			return fmt.Errorf("route %s: quota limit must be > 0", routeID)
		}
		validPeriods := map[string]bool{"hourly": true, "daily": true, "monthly": true}
		if !validPeriods[route.Quota.Period] {
			return fmt.Errorf("route %s: quota period must be hourly, daily, or monthly", routeID)
		}
		if route.Quota.Key == "" {
			return fmt.Errorf("route %s: quota key is required", routeID)
		}
	}

	// === Aggregate ===
	if route.Aggregate.Enabled {
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
		if route.Passthrough {
			return fmt.Errorf("route %s: aggregate is mutually exclusive with passthrough", routeID)
		}
	}

	// === Response body generator ===
	if route.ResponseBodyGenerator.Enabled {
		if route.ResponseBodyGenerator.Template == "" {
			return fmt.Errorf("route %s: response_body_generator requires a template", routeID)
		}
		if route.Passthrough {
			return fmt.Errorf("route %s: response_body_generator is mutually exclusive with passthrough", routeID)
		}
	}

	// === Param forwarding ===
	if route.ParamForwarding.Enabled {
		if len(route.ParamForwarding.Headers) == 0 && len(route.ParamForwarding.QueryParams) == 0 && len(route.ParamForwarding.Cookies) == 0 {
			return fmt.Errorf("route %s: param_forwarding requires at least one of headers, query_params, or cookies", routeID)
		}
	}

	// === Content negotiation ===
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
		if route.Passthrough {
			return fmt.Errorf("route %s: content_negotiation is mutually exclusive with passthrough", routeID)
		}
	}

	// === CDN cache headers ===
	if route.CDNCacheHeaders.Enabled {
		if route.CDNCacheHeaders.CacheControl == "" && route.CDNCacheHeaders.SurrogateControl == "" && len(route.CDNCacheHeaders.Vary) == 0 {
			return fmt.Errorf("route %s: cdn_cache_headers requires at least one of cache_control, surrogate_control, or vary", routeID)
		}
	}

	// === Cache bucket name ===
	if route.Cache.Bucket != "" {
		for _, c := range route.Cache.Bucket {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
				return fmt.Errorf("route %s: cache bucket name must be alphanumeric with hyphens/underscores", routeID)
			}
		}
	}

	// === Backend encoding ===
	if route.BackendEncoding.Encoding != "" {
		if route.BackendEncoding.Encoding != "xml" && route.BackendEncoding.Encoding != "yaml" {
			return fmt.Errorf("route %s: backend_encoding encoding must be 'xml' or 'yaml', got %q", routeID, route.BackendEncoding.Encoding)
		}
		if route.Passthrough {
			return fmt.Errorf("route %s: backend_encoding is mutually exclusive with passthrough", routeID)
		}
	}

	// === Upstream vs inline backends ===
	if route.Upstream != "" {
		if len(route.Backends) > 0 {
			return fmt.Errorf("route %s: upstream and backends are mutually exclusive", routeID)
		}
		if route.Service.Name != "" {
			return fmt.Errorf("route %s: upstream and service are mutually exclusive", routeID)
		}
	}

	// === Match config ===
	if err := l.validateMatchConfig(routeID, route.Match); err != nil {
		return err
	}

	// === Rate limiting ===
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

	// === Tiered rate limits ===
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

	// === Per-route rules ===
	if err := l.validateRules(route.Rules.Request, "request"); err != nil {
		return fmt.Errorf("route %s rules: %w", routeID, err)
	}
	if err := l.validateRules(route.Rules.Response, "response"); err != nil {
		return fmt.Errorf("route %s rules: %w", routeID, err)
	}

	// === Per-route traffic shaping ===
	if err := l.validateTrafficShaping(route.TrafficShaping, scope); err != nil {
		return err
	}
	if route.TrafficShaping.Priority.Enabled && !cfg.TrafficShaping.Priority.Enabled {
		return fmt.Errorf("route %s: per-route priority requires global priority to be enabled", routeID)
	}

	// === Sticky sessions ===
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

	// === Traffic split ===
	if len(route.TrafficSplit) > 0 {
		totalWeight := 0
		for _, split := range route.TrafficSplit {
			totalWeight += split.Weight
		}
		if totalWeight != 100 {
			return fmt.Errorf("route %s: traffic_split weights must sum to 100, got %d", routeID, totalWeight)
		}
	}

	// === Mirror ===
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

	// === CORS regex ===
	for _, pattern := range route.CORS.AllowOriginPatterns {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("route %s: cors allow_origin_patterns: invalid regex %q: %w", routeID, pattern, err)
		}
	}

	// === WAF ===
	if route.WAF.Enabled {
		if route.WAF.Mode != "" && route.WAF.Mode != "block" && route.WAF.Mode != "detect" {
			return fmt.Errorf("route %s: WAF mode must be 'block' or 'detect'", routeID)
		}
	}

	// === GraphQL ===
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

	// === Coalesce ===
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

	// === Canary ===
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
	}

	// === Retry policy ===
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
	if route.RetryPolicy.Hedging.Enabled {
		if route.RetryPolicy.Hedging.MaxRequests < 2 {
			return fmt.Errorf("route %s: retry_policy hedging max_requests must be >= 2", routeID)
		}
		if route.RetryPolicy.MaxRetries > 0 {
			return fmt.Errorf("route %s: retry_policy cannot use both hedging and max_retries", routeID)
		}
	}

	// === Circuit breaker ===
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
	}

	// === Cache ===
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

	// === WebSocket ===
	if route.WebSocket.Enabled {
		if route.WebSocket.ReadBufferSize != 0 && route.WebSocket.ReadBufferSize < 1 {
			return fmt.Errorf("route %s: websocket read_buffer_size must be > 0", routeID)
		}
		if route.WebSocket.WriteBufferSize != 0 && route.WebSocket.WriteBufferSize < 1 {
			return fmt.Errorf("route %s: websocket write_buffer_size must be > 0", routeID)
		}
	}

	// === Load balancer ===
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

	// === Protocol translation ===
	if route.Protocol.Type != "" {
		validProtocolTypes := map[string]bool{"http_to_grpc": true, "http_to_thrift": true}
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
		}
	}

	// === External auth ===
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

	// === Versioning ===
	if route.Versioning.Enabled {
		if err := l.validateRouteVersioning(routeID, route); err != nil {
			return err
		}
	}

	// === Body transforms ===
	if err := l.validateBodyTransform(routeID, "request", route.Transform.Request.Body); err != nil {
		return err
	}
	if err := l.validateBodyTransform(routeID, "response", route.Transform.Response.Body); err != nil {
		return err
	}

	// === Access log ===
	if err := l.validateAccessLog(routeID, route.AccessLog); err != nil {
		return err
	}

	// === OpenAPI ===
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

	// === Response validation ===
	if route.Validation.ResponseSchema != "" && route.Validation.ResponseSchemaFile != "" {
		return fmt.Errorf("route %s: validation response_schema and response_schema_file are mutually exclusive", routeID)
	}

	// === Rewrite ===
	if err := l.validateRewriteConfig(routeID, route.Rewrite, route.PathPrefix, route.StripPrefix); err != nil {
		return err
	}

	// === Timeout policy ===
	if route.TimeoutPolicy.IsActive() {
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
	}

	// === Per-backend health checks ===
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

	// === Upstream references ===
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

	// === Error pages ===
	if err := l.validateErrorPages(scope, route.ErrorPages); err != nil {
		return err
	}

	// === Nonce ===
	if err := l.validateNonceConfig(scope, route.Nonce, cfg.Redis.Address); err != nil {
		return err
	}

	// === Outlier detection ===
	if route.OutlierDetection.Enabled {
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
	}

	// === CSRF ===
	if err := l.validateCSRFConfig(scope, route.CSRF); err != nil {
		return err
	}

	// === Geo ===
	if err := l.validateGeoConfig(scope, route.Geo); err != nil {
		return err
	}

	// === Idempotency ===
	if err := l.validateIdempotencyConfig(scope, route.Idempotency, cfg.Redis.Address); err != nil {
		return err
	}

	// === Backend signing ===
	if err := l.validateBackendSigningConfig(scope, route.BackendSigning); err != nil {
		return err
	}

	// === Compression ===
	if err := l.validateCompressionConfig(scope, route.Compression); err != nil {
		return err
	}

	// === Request decompression ===
	if err := l.validateDecompressionConfig(scope, route.RequestDecompression); err != nil {
		return err
	}

	// === Response limit ===
	if err := l.validateResponseLimitConfig(scope, route.ResponseLimit); err != nil {
		return err
	}

	// === Security headers ===
	if err := l.validateSecurityHeadersConfig(scope, route.SecurityHeaders); err != nil {
		return err
	}

	// === Maintenance ===
	if err := l.validateMaintenanceConfig(scope, route.Maintenance); err != nil {
		return err
	}

	// === Bot detection ===
	if route.BotDetection.Enabled {
		if err := l.validateBotDetectionConfig(scope, route.BotDetection); err != nil {
			return err
		}
	}

	// === Proxy rate limit ===
	if route.ProxyRateLimit.Enabled {
		if route.ProxyRateLimit.Rate <= 0 {
			return fmt.Errorf("route %s: proxy_rate_limit.rate must be > 0", routeID)
		}
	}

	// === Claims propagation ===
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
