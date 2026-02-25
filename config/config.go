package config

import (
	"net"
	"time"

	"github.com/goccy/go-yaml"
)

// Protocol defines the listener protocol type
type Protocol string

const (
	ProtocolHTTP Protocol = "http"
	ProtocolTCP  Protocol = "tcp"
	ProtocolUDP  Protocol = "udp"
)

// UpstreamConfig defines a named backend pool that can be referenced by multiple routes.
type UpstreamConfig struct {
	Backends       []BackendConfig      `yaml:"backends"`
	Service        ServiceConfig        `yaml:"service"`
	LoadBalancer   string               `yaml:"load_balancer"`
	ConsistentHash ConsistentHashConfig `yaml:"consistent_hash"`
	HealthCheck    *HealthCheckConfig   `yaml:"health_check"`
	Transport      TransportConfig      `yaml:"transport"`
}

// Config represents the complete gateway configuration
type Config struct {
	Listeners      []ListenerConfig              `yaml:"listeners"`
	Registry       RegistryConfig                `yaml:"registry"`
	Authentication AuthenticationConfig          `yaml:"authentication"`
	Upstreams      map[string]UpstreamConfig     `yaml:"upstreams"`
	Routes         []RouteConfig                 `yaml:"routes"`
	TCPRoutes      []TCPRouteConfig              `yaml:"tcp_routes"`      // TCP L4 routes
	UDPRoutes      []UDPRouteConfig              `yaml:"udp_routes"`      // UDP L4 routes
	Logging        LoggingConfig        `yaml:"logging"`
	Admin          AdminConfig          `yaml:"admin"`
	Tracing        TracingConfig        `yaml:"tracing"`         // Feature 9: Distributed tracing
	IPFilter       IPFilterConfig       `yaml:"ip_filter"`       // Feature 2: Global IP filter
	Rules          RulesConfig          `yaml:"rules"`           // Global rules engine
	TrafficShaping TrafficShapingConfig `yaml:"traffic_shaping"` // Global traffic shaping
	Redis          RedisConfig          `yaml:"redis"`           // Redis for distributed features
	WAF            WAFConfig            `yaml:"waf"`             // Global WAF settings
	DNSResolver    DNSResolverConfig    `yaml:"dns_resolver"`    // Custom DNS resolver for backends
	OpenAPI        OpenAPIConfig        `yaml:"openapi"`         // OpenAPI spec-based validation and route generation
	Webhooks       WebhooksConfig       `yaml:"webhooks"`        // Event webhook notifications
	HealthCheck    HealthCheckConfig    `yaml:"health_check"`    // Global health check settings
	ErrorPages     ErrorPagesConfig     `yaml:"error_pages"`     // Global custom error pages
	Nonce          NonceConfig          `yaml:"nonce"`           // Global nonce replay prevention
	CSRF           CSRFConfig           `yaml:"csrf"`            // Global CSRF protection
	Geo            GeoConfig            `yaml:"geo"`             // Global geo filtering
	Idempotency    IdempotencyConfig    `yaml:"idempotency"`     // Global idempotency key support
	BackendSigning         BackendSigningConfig         `yaml:"backend_signing"`          // Global backend request signing
	Transport              TransportConfig              `yaml:"transport"`                // Global upstream transport settings
	RequestDecompression   RequestDecompressionConfig   `yaml:"request_decompression"`    // Global request decompression
	ResponseLimit          ResponseLimitConfig          `yaml:"response_limit"`           // Global response size limit
	SecurityHeaders        SecurityHeadersConfig        `yaml:"security_headers"`         // Global security response headers
	Maintenance            MaintenanceConfig            `yaml:"maintenance"`              // Global maintenance mode
	Shutdown               ShutdownConfig               `yaml:"shutdown"`                 // Graceful shutdown settings
	TrustedProxies         TrustedProxiesConfig         `yaml:"trusted_proxies"`          // Trusted proxy IP extraction
	BotDetection           BotDetectionConfig           `yaml:"bot_detection"`            // Global bot detection
	AICrawlControl         AICrawlConfig                `yaml:"ai_crawl_control"`         // Global AI crawler control
	ClientMTLS             ClientMTLSConfig             `yaml:"client_mtls"`              // Global per-route client mTLS verification
	HTTPSRedirect          HTTPSRedirectConfig          `yaml:"https_redirect"`           // Automatic HTTP→HTTPS redirect
	AllowedHosts           AllowedHostsConfig           `yaml:"allowed_hosts"`            // Host header validation
	TokenRevocation        TokenRevocationConfig        `yaml:"token_revocation"`         // JWT token revocation / blocklist
	ServiceRateLimit       ServiceRateLimitConfig       `yaml:"service_rate_limit"`        // Global service-level rate limit
	SpikeArrest            SpikeArrestConfig            `yaml:"spike_arrest"`              // Global spike arrest defaults
	DebugEndpoint          DebugEndpointConfig          `yaml:"debug_endpoint"`            // Debug endpoint for request inspection
	CDNCacheHeaders        CDNCacheConfig               `yaml:"cdn_cache_headers"`         // Global CDN cache header injection
	EdgeCacheRules         EdgeCacheRulesConfig         `yaml:"edge_cache_rules"`          // Global conditional edge cache rules
	RetryBudgets           map[string]BudgetConfig      `yaml:"retry_budgets"`             // Named shared retry budget pools
	InboundSigning         InboundSigningConfig         `yaml:"inbound_signing"`           // Global inbound request signature verification
	SSRFProtection         SSRFProtectionConfig         `yaml:"ssrf_protection"`           // SSRF protection for outbound connections
	IPBlocklist            IPBlocklistConfig            `yaml:"ip_blocklist"`              // Dynamic IP blocklist
	LoadShedding           LoadSheddingConfig           `yaml:"load_shedding"`             // System-level load shedding
	AuditLog               AuditLogConfig               `yaml:"audit_log"`                 // Global audit logging defaults
	Wasm                   WasmConfig                   `yaml:"wasm"`                      // WASM plugin runtime settings
	Tenants                TenantsConfig                `yaml:"tenants"`                   // Multi-tenancy configuration
	CompletionHeader       bool                         `yaml:"completion_header"`         // Add X-Gateway-Completed header to aggregate/sequential responses
	Deprecation            DeprecationConfig            `yaml:"deprecation"`               // Global API deprecation lifecycle (RFC 8594)
	ConsumerGroups         ConsumerGroupsConfig         `yaml:"consumer_groups"`           // Consumer group definitions
	Baggage                BaggageConfig                `yaml:"baggage"`                   // Global baggage propagation defaults
	Extensions             map[string]yaml.RawMessage   `yaml:"extensions,omitempty"`      // Plugin extension config (raw YAML, decoded by plugins)
}

// ListenerConfig defines a listener configuration
type ListenerConfig struct {
	ID       string             `yaml:"id"`
	Address  string             `yaml:"address"`   // e.g., ":8080"
	Protocol Protocol           `yaml:"protocol"`
	TLS      TLSConfig          `yaml:"tls"`
	HTTP     HTTPListenerConfig `yaml:"http,omitempty"`
	TCP      TCPListenerConfig  `yaml:"tcp,omitempty"`
	UDP      UDPListenerConfig  `yaml:"udp,omitempty"`
}

// HTTPListenerConfig defines HTTP-specific listener settings
type HTTPListenerConfig struct {
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	MaxHeaderBytes    int           `yaml:"max_header_bytes"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	EnableHTTP3       bool          `yaml:"enable_http3"` // serve HTTP/3 over QUIC on same port
}

// TCPListenerConfig defines TCP-specific listener settings
type TCPListenerConfig struct {
	SNIRouting     bool          `yaml:"sni_routing"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	IdleTimeout    time.Duration `yaml:"idle_timeout"`
	ProxyProtocol  bool          `yaml:"proxy_protocol"`
}

// UDPListenerConfig defines UDP-specific listener settings
type UDPListenerConfig struct {
	SessionTimeout  time.Duration `yaml:"session_timeout"`
	ReadBufferSize  int           `yaml:"read_buffer_size"`
	WriteBufferSize int           `yaml:"write_buffer_size"`
}

// TCPRouteConfig defines a TCP route
type TCPRouteConfig struct {
	ID       string          `yaml:"id"`
	Listener string          `yaml:"listener"`
	Match    TCPMatchConfig  `yaml:"match"`
	Backends []BackendConfig `yaml:"backends"`
}

// TCPMatchConfig defines TCP route matching criteria
type TCPMatchConfig struct {
	SNI        []string `yaml:"sni"`
	SourceCIDR []string `yaml:"source_cidr"`
}

// UDPRouteConfig defines a UDP route
type UDPRouteConfig struct {
	ID       string          `yaml:"id"`
	Listener string          `yaml:"listener"`
	Backends []BackendConfig `yaml:"backends"`
}

// ParsedSourceCIDRs parses the SourceCIDR strings into net.IPNet
func (m *TCPMatchConfig) ParsedSourceCIDRs() ([]*net.IPNet, error) {
	var cidrs []*net.IPNet
	for _, cidr := range m.SourceCIDR {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		cidrs = append(cidrs, ipNet)
	}
	return cidrs, nil
}

// RegistryConfig defines service registry settings
type RegistryConfig struct {
	Type       string           `yaml:"type"` // consul, etcd, kubernetes, memory, dns
	Consul     ConsulConfig     `yaml:"consul"`
	Etcd       EtcdConfig       `yaml:"etcd"`
	Kubernetes KubernetesConfig `yaml:"kubernetes"`
	Memory     MemoryConfig     `yaml:"memory"`
	DNSSRV     DNSSRVConfig     `yaml:"dns"`
}

// DNSSRVConfig defines DNS SRV service discovery settings.
// Not to be confused with DNSResolverConfig which handles custom DNS for backend connections.
type DNSSRVConfig struct {
	Domain       string        `yaml:"domain"`        // required: e.g. "service.consul", "svc.cluster.local"
	Protocol     string        `yaml:"protocol"`      // default "tcp"
	Nameserver   string        `yaml:"nameserver"`    // optional custom DNS server "host:port"
	PollInterval time.Duration `yaml:"poll_interval"` // default 30s
}

// ConsulConfig defines Consul-specific settings
type ConsulConfig struct {
	Address    string `yaml:"address"`
	Scheme     string `yaml:"scheme"`
	Datacenter string `yaml:"datacenter"`
	Token      string `yaml:"token"`
	Namespace  string `yaml:"namespace"`
}

// EtcdConfig defines etcd-specific settings
type EtcdConfig struct {
	Endpoints []string `yaml:"endpoints"`
	Username  string   `yaml:"username"`
	Password  string   `yaml:"password"`
	TLS       TLSConfig `yaml:"tls"`
}

// TLSConfig defines TLS settings
// TLSCertPair holds a TLS certificate+key pair for SNI-based selection.
// CertData/KeyData are preferred over file paths when set (e.g. from K8s Secrets).
type TLSCertPair struct {
	CertData []byte   `yaml:"-"`         // PEM bytes (in-memory, never written to disk)
	KeyData  []byte   `yaml:"-"`         // PEM bytes (in-memory, never written to disk)
	CertFile string   `yaml:"cert_file"` // file path (standalone mode)
	KeyFile  string   `yaml:"key_file"`  // file path (standalone mode)
	Hosts    []string `yaml:"hosts"`     // SNI hostnames this cert applies to
}

type TLSConfig struct {
	Enabled      bool            `yaml:"enabled"`
	CertFile     string          `yaml:"cert_file"`
	KeyFile      string          `yaml:"key_file"`
	Certificates []TLSCertPair  `yaml:"certificates"` // multi-cert SNI
	CAFile       string          `yaml:"ca_file"`
	ClientAuth   string          `yaml:"client_auth"`   // Feature 11: mTLS - none, request, require, verify
	ClientCAFile string          `yaml:"client_ca_file"` // Feature 11: mTLS
	ACME         ACMEConfig      `yaml:"acme"`           // Automatic certificate provisioning via ACME/Let's Encrypt
}

// ACMEConfig defines ACME (Let's Encrypt) automatic certificate provisioning settings.
type ACMEConfig struct {
	Enabled       bool     `yaml:"enabled"`
	Domains       []string `yaml:"domains"`
	Email         string   `yaml:"email"`
	DirectoryURL  string   `yaml:"directory_url"`  // ACME directory (default: Let's Encrypt production)
	CacheDir      string   `yaml:"cache_dir"`      // Certificate cache directory (default: /var/lib/gateway/acme)
	ChallengeType string   `yaml:"challenge_type"` // "tls-alpn-01" (default) or "http-01"
	HTTPAddress   string   `yaml:"http_address"`   // HTTP-01 challenge bind address (default ":80")
}

// KubernetesConfig defines Kubernetes-specific settings
type KubernetesConfig struct {
	Namespace     string `yaml:"namespace"`
	LabelSelector string `yaml:"label_selector"`
	InCluster     bool   `yaml:"in_cluster"`
	KubeConfig    string `yaml:"kubeconfig"`
}

// MemoryConfig defines in-memory registry settings
type MemoryConfig struct {
	APIEnabled bool `yaml:"api_enabled"`
	APIPort    int  `yaml:"api_port"`
}

// AuthenticationConfig defines auth settings
type AuthenticationConfig struct {
	APIKey APIKeyConfig `yaml:"api_key"`
	JWT    JWTConfig    `yaml:"jwt"`
	OAuth  OAuthConfig  `yaml:"oauth"` // Feature 7: OAuth 2.0 / OIDC
}

// TokenExchangeConfig defines OAuth2/OIDC token exchange (RFC 8693) settings.
type TokenExchangeConfig struct {
	Enabled          bool              `yaml:"enabled"`
	ValidationMode   string            `yaml:"validation_mode"`    // "jwt" (local JWKS) or "introspection"
	JWKSURL          string            `yaml:"jwks_url"`           // for jwt mode
	TrustedIssuers   []string          `yaml:"trusted_issuers"`    // for jwt mode
	IntrospectionURL string            `yaml:"introspection_url"`  // for introspection mode
	ClientID         string            `yaml:"client_id"`          // for introspection mode
	ClientSecret     string            `yaml:"client_secret"`      // for introspection mode
	Issuer           string            `yaml:"issuer"`             // issuer claim in minted tokens
	Audience         []string          `yaml:"audience"`           // audience claim in minted tokens
	Scopes           []string          `yaml:"scopes"`             // scopes in minted tokens
	TokenLifetime    time.Duration     `yaml:"token_lifetime"`     // lifetime of minted tokens
	SigningAlgorithm string            `yaml:"signing_algorithm"`  // RS256, RS512, HS256, HS512
	SigningKeyFile   string            `yaml:"signing_key_file"`   // PEM file for RSA signing
	SigningKey       string            `yaml:"signing_key"`        // inline PEM for RSA signing
	SigningSecret    string            `yaml:"signing_secret"`     // base64 secret for HMAC signing
	CacheTTL         time.Duration     `yaml:"cache_ttl"`          // exchange result cache TTL
	ClaimMappings    map[string]string `yaml:"claim_mappings"`     // subject claim → issued claim mappings
}

// APIKeyConfig defines API key authentication settings
type APIKeyConfig struct {
	Enabled    bool                   `yaml:"enabled"`
	Header     string                 `yaml:"header"`
	QueryParam string                 `yaml:"query_param"`
	Keys       []APIKeyEntry          `yaml:"keys"`
	Management APIKeyManagementConfig `yaml:"management"`
}

// APIKeyManagementConfig defines settings for managed API key generation/rotation/revocation.
type APIKeyManagementConfig struct {
	Enabled          bool                  `yaml:"enabled"`
	KeyLength        int                   `yaml:"key_length"`         // bytes (default 32)
	KeyPrefix        string                `yaml:"key_prefix"`         // e.g., "gw_"
	Store            string                `yaml:"store"`              // "memory" (default) or "redis"
	DefaultRateLimit *KeyRateLimitConfig   `yaml:"default_rate_limit"` // default per-key rate limit
}

// KeyRateLimitConfig defines per-key rate limit settings.
type KeyRateLimitConfig struct {
	Rate   int           `yaml:"rate"`
	Period time.Duration `yaml:"period"`
	Burst  int           `yaml:"burst"`
}

// APIKeyEntry represents a single API key
type APIKeyEntry struct {
	Key       string    `yaml:"key"`
	ClientID  string    `yaml:"client_id"`
	Name      string    `yaml:"name"`
	ExpiresAt string    `yaml:"expires_at"` // Feature 14: RFC3339 expiration
	Roles     []string  `yaml:"roles"`      // Feature 14: Role-based access
}

// JWTConfig defines JWT authentication settings
type JWTConfig struct {
	Enabled             bool          `yaml:"enabled"`
	Secret              string        `yaml:"secret"`
	PublicKey           string        `yaml:"public_key"`
	Issuer              string        `yaml:"issuer"`
	Audience            []string      `yaml:"audience"`
	Algorithm           string        `yaml:"algorithm"`             // HS256, RS256
	JWKSURL             string        `yaml:"jwks_url"`              // JWKS endpoint for dynamic key fetching
	JWKSRefreshInterval time.Duration `yaml:"jwks_refresh_interval"` // default 1h
}

// OAuthConfig defines OAuth 2.0 / OIDC settings (Feature 7)
type OAuthConfig struct {
	Enabled              bool          `yaml:"enabled"`
	IntrospectionURL     string        `yaml:"introspection_url"`
	ClientID             string        `yaml:"client_id"`
	ClientSecret         string        `yaml:"client_secret"`
	JWKSURL              string        `yaml:"jwks_url"`
	JWKSRefreshInterval  time.Duration `yaml:"jwks_refresh_interval"`
	Issuer               string        `yaml:"issuer"`
	Audience             string        `yaml:"audience"`
	Scopes               []string      `yaml:"scopes"`
	CacheTTL             time.Duration `yaml:"cache_ttl"`
}

// RouteConfig defines a single route
type RouteConfig struct {
	ID             string               `yaml:"id"`
	Path           string               `yaml:"path"`
	PathPrefix     bool                 `yaml:"path_prefix"`
	Methods        []string             `yaml:"methods"`
	Match          MatchConfig          `yaml:"match"`
	Backends       []BackendConfig      `yaml:"backends"`
	Service        ServiceConfig        `yaml:"service"`
	Upstream       string               `yaml:"upstream"` // reference to named upstream in Config.Upstreams
	Auth           RouteAuthConfig      `yaml:"auth"`
	RateLimit      RateLimitConfig      `yaml:"rate_limit"`
	Transform      TransformConfig      `yaml:"transform"`
	Timeout        time.Duration        `yaml:"timeout"`
	Retries        int                  `yaml:"retries"`
	StripPrefix    bool                 `yaml:"strip_prefix"`
	RetryPolicy    RetryConfig          `yaml:"retry_policy"`
	TimeoutPolicy  TimeoutConfig        `yaml:"timeout_policy"`
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker"`
	Cache          CacheConfig          `yaml:"cache"`
	WebSocket      WebSocketConfig      `yaml:"websocket"`
	MaxBodySize    int64                `yaml:"max_body_size"`    // Feature 1: Request body size limit (bytes)
	IPFilter       IPFilterConfig       `yaml:"ip_filter"`        // Feature 2: Per-route IP filter
	CORS           CORSConfig           `yaml:"cors"`             // Feature 3: CORS settings
	Compression    CompressionConfig    `yaml:"compression"`      // Feature 4: Response compression
	TrafficSplit   []TrafficSplitConfig `yaml:"traffic_split"`    // Feature 6: Canary/weighted splitting
	Validation     ValidationConfig     `yaml:"validation"`       // Feature 8: Request validation
	Mirror         MirrorConfig         `yaml:"mirror"`           // Feature 10: Traffic mirroring
	GRPC           GRPCConfig           `yaml:"grpc"`             // Feature 12: gRPC proxying
	Rules          RulesConfig          `yaml:"rules"`            // Per-route rules engine
	Protocol       ProtocolConfig       `yaml:"protocol"`         // Protocol translation
	TrafficShaping TrafficShapingConfig `yaml:"traffic_shaping"` // Per-route traffic shaping
	Sticky         StickyConfig         `yaml:"sticky"`          // Sticky sessions for traffic split
	WAF            WAFConfig            `yaml:"waf"`             // Per-route WAF settings
	LoadBalancer   string               `yaml:"load_balancer"`   // "round_robin"|"least_conn"|"consistent_hash"|"least_response_time"
	ConsistentHash ConsistentHashConfig `yaml:"consistent_hash"` // Config for consistent_hash LB
	GraphQL            GraphQLConfig            `yaml:"graphql"`              // GraphQL query analysis and protection
	GraphQLFederation  GraphQLFederationConfig  `yaml:"graphql_federation"`   // GraphQL federation / schema stitching
	Coalesce           CoalesceConfig           `yaml:"coalesce"`             // Request coalescing (singleflight)
	Canary         CanaryConfig         `yaml:"canary"`          // Canary deployment with automated rollback
	ExtAuth        ExtAuthConfig        `yaml:"ext_auth"`        // External auth service
	Versioning     VersioningConfig     `yaml:"versioning"`      // API versioning
	AccessLog      AccessLogConfig      `yaml:"access_log"`      // Per-route access log overrides
	OpenAPI        OpenAPIRouteConfig   `yaml:"openapi"`         // OpenAPI spec-based validation
	ErrorPages     ErrorPagesConfig     `yaml:"error_pages"`     // Per-route custom error pages
	Nonce             NonceConfig             `yaml:"nonce"`              // Per-route nonce replay prevention
	CSRF              CSRFConfig              `yaml:"csrf"`               // Per-route CSRF protection
	Idempotency       IdempotencyConfig       `yaml:"idempotency"`        // Per-route idempotency key support
	OutlierDetection OutlierDetectionConfig  `yaml:"outlier_detection"`  // Per-route outlier detection
	Geo              GeoConfig               `yaml:"geo"`                // Per-route geo filtering
	BackendSigning       BackendSigningConfig       `yaml:"backend_signing"`       // Per-route backend request signing
	RequestDecompression RequestDecompressionConfig `yaml:"request_decompression"` // Per-route request decompression
	ResponseLimit        ResponseLimitConfig        `yaml:"response_limit"`        // Per-route response size limit
	SecurityHeaders      SecurityHeadersConfig      `yaml:"security_headers"`      // Per-route security response headers
	Maintenance          MaintenanceConfig          `yaml:"maintenance"`           // Per-route maintenance mode
	Rewrite              RewriteConfig              `yaml:"rewrite"`               // URL rewriting (prefix, regex, host override)
	BotDetection         BotDetectionConfig         `yaml:"bot_detection"`         // Per-route bot detection
	AICrawlControl       AICrawlConfig              `yaml:"ai_crawl_control"`      // Per-route AI crawler control
	ClientMTLS           ClientMTLSConfig           `yaml:"client_mtls"`           // Per-route client mTLS verification
	ProxyRateLimit       ProxyRateLimitConfig       `yaml:"proxy_rate_limit"`      // Per-route backend rate limiting
	MockResponse         MockResponseConfig         `yaml:"mock_response"`         // Per-route mock responses
	ClaimsPropagation    ClaimsPropagationConfig    `yaml:"claims_propagation"`    // JWT claims propagation to backend headers
	BackendAuth          BackendAuthConfig          `yaml:"backend_auth"`          // OAuth2 client_credentials for backend calls
	StatusMapping        StatusMappingConfig        `yaml:"status_mapping"`        // Remap backend response status codes
	Static               StaticConfig               `yaml:"static"`                // Serve static files (replaces proxy)
	Passthrough          bool                       `yaml:"passthrough"`           // Skip body-processing middleware
	Echo                 bool                       `yaml:"echo"`                  // Echo handler (no backend needed)
	SpikeArrest          SpikeArrestConfig          `yaml:"spike_arrest"`          // Per-route spike arrest
	ContentReplacer      ContentReplacerConfig      `yaml:"content_replacer"`      // Per-route response content replacement
	FollowRedirects      FollowRedirectsConfig      `yaml:"follow_redirects"`      // Follow backend 3xx redirects
	BodyGenerator        BodyGeneratorConfig         `yaml:"body_generator"`        // Generate request body from template
	Sequential           SequentialConfig            `yaml:"sequential"`            // Chain multiple backend calls
	Quota                QuotaConfig                 `yaml:"quota"`                 // Per-client usage quota enforcement
	Aggregate            AggregateConfig             `yaml:"aggregate"`             // Parallel multi-backend response aggregation
	ResponseBodyGenerator ResponseBodyGeneratorConfig `yaml:"response_body_generator"` // Rewrite response body with Go template
	ParamForwarding      ParamForwardingConfig       `yaml:"param_forwarding"`      // Zero-trust parameter forwarding
	ContentNegotiation   ContentNegotiationConfig    `yaml:"content_negotiation"`   // Accept header → response encoding
	CDNCacheHeaders      CDNCacheConfig              `yaml:"cdn_cache_headers"`     // Per-route CDN cache header injection
	EdgeCacheRules       EdgeCacheRulesConfig        `yaml:"edge_cache_rules"`      // Per-route conditional edge cache rules
	BackendEncoding      BackendEncodingConfig       `yaml:"backend_encoding"`      // Decode XML/YAML backend responses to JSON
	SSE                  SSEConfig                   `yaml:"sse"`                   // Server-Sent Events proxy
	InboundSigning       InboundSigningConfig        `yaml:"inbound_signing"`       // Per-route inbound request signature verification
	PIIRedaction         PIIRedactionConfig          `yaml:"pii_redaction"`         // Per-route PII redaction
	FieldEncryption      FieldEncryptionConfig       `yaml:"field_encryption"`      // Per-route field-level encryption
	BlueGreen            BlueGreenConfig             `yaml:"blue_green"`            // Blue-green deployment
	ABTest               ABTestConfig                `yaml:"ab_test"`               // A/B testing with metric collection
	FastCGI              FastCGIConfig               `yaml:"fastcgi"`               // FastCGI proxy (replaces proxy)
	RequestDedup         RequestDedupConfig          `yaml:"request_dedup"`         // Per-route request deduplication
	IPBlocklist          IPBlocklistConfig           `yaml:"ip_blocklist"`          // Per-route dynamic IP blocklist
	Baggage              BaggageConfig               `yaml:"baggage"`               // Per-route baggage propagation
	Backpressure         BackpressureConfig          `yaml:"backpressure"`          // Per-route backend backpressure detection
	AuditLog             AuditLogConfig              `yaml:"audit_log"`             // Per-route audit logging
	Modifiers            []ModifierConfig            `yaml:"modifiers"`             // Martian-style request/response modifiers
	FieldReplacer        FieldReplacerConfig         `yaml:"field_replacer"`        // Field-level content replacement
	JMESPath             JMESPathConfig              `yaml:"jmespath"`              // JMESPath query on response body
	BackendResponse      BackendResponseConfig       `yaml:"backend_response"`      // Backend response handling (is_collection, etc.)
	OutputEncoding       string                      `yaml:"output_encoding"`       // Override Accept-header content negotiation (json, xml, yaml, json-collection, string)
	ErrorHandling        ErrorHandlingConfig         `yaml:"error_handling"`        // Structured error detail modes
	Lua                  LuaConfig                   `yaml:"lua"`                   // Lua scripting engine
	WasmPlugins          []WasmPluginConfig          `yaml:"wasm_plugins"`          // WASM plugin chain
	Lambda               LambdaConfig                `yaml:"lambda"`                // AWS Lambda backend
	AMQP                 AMQPConfig                  `yaml:"amqp"`                  // AMQP/RabbitMQ backend
	PubSub               PubSubConfig                `yaml:"pubsub"`                // Pub/Sub backend (Go CDK)
	Tenant               RouteTenantConfig              `yaml:"tenant"`                 // Per-route tenant restrictions
	TenantBackends       map[string][]BackendConfig    `yaml:"tenant_backends,omitempty"` // Per-tenant dedicated backends
	CompletionHeader     bool                          `yaml:"completion_header"`      // Add X-Gateway-Completed header
	SessionAffinity      SessionAffinityConfig          `yaml:"session_affinity"`       // Cookie-based backend pinning
	TrafficReplay        TrafficReplayConfig            `yaml:"traffic_replay"`         // Record and replay traffic
	TokenExchange        TokenExchangeConfig            `yaml:"token_exchange"`         // OAuth2/OIDC token exchange (RFC 8693)
	Deprecation          DeprecationConfig              `yaml:"deprecation"`            // API deprecation lifecycle (RFC 8594)
	SLO                  SLOConfig                      `yaml:"slo"`                    // SLI/SLO enforcement with error budget
	ETag                 ETagConfig                     `yaml:"etag"`                   // Per-route ETag generation and conditional requests
	Streaming            StreamingConfig                `yaml:"streaming"`              // Per-route response streaming controls
	ResponseSigning      ResponseSigningConfig          `yaml:"response_signing"`       // Per-route response body signing
	OPA                  OPAConfig                      `yaml:"opa"`                    // Per-route OPA policy engine
	RequestCost          RequestCostConfig              `yaml:"request_cost"`           // Per-route request cost tracking
	Connect              ConnectConfig                  `yaml:"connect"`                // HTTP CONNECT tunneling
	AI                   AIConfig                       `yaml:"ai"`                     // AI gateway (LLM proxy)
	Extensions           map[string]yaml.RawMessage     `yaml:"extensions,omitempty"`   // Plugin extension config (raw YAML, decoded by plugins)
}

// StickyConfig defines sticky session settings for consistent traffic group assignment.
type StickyConfig struct {
	Enabled    bool          `yaml:"enabled"`
	Mode       string        `yaml:"mode"`        // "cookie", "header", "hash"
	CookieName string        `yaml:"cookie_name"` // default "X-Traffic-Group"
	HashKey    string        `yaml:"hash_key"`    // header name for header/hash mode
	TTL        time.Duration `yaml:"ttl"`         // cookie TTL, default 24h
}

// SessionAffinityConfig defines cookie-based backend pinning (individual backend, not traffic group).
type SessionAffinityConfig struct {
	Enabled    bool          `yaml:"enabled"`
	CookieName string        `yaml:"cookie_name"` // default "X-Session-Backend"
	TTL        time.Duration `yaml:"ttl"`         // cookie TTL, default 1h
	Path       string        `yaml:"path"`        // cookie path, default "/"
	Secure     bool          `yaml:"secure"`      // Secure flag on cookie
	SameSite   string        `yaml:"same_site"`   // "lax"|"strict"|"none", default "lax"
}

// TrafficReplayConfig defines per-route traffic recording and replay settings.
type TrafficReplayConfig struct {
	Enabled       bool                       `yaml:"enabled"`
	MaxRecordings int                        `yaml:"max_recordings"` // ring buffer size, default 10000
	Percentage    int                        `yaml:"percentage"`     // sampling 0-100, default 100
	MaxBodySize   int64                      `yaml:"max_body_size"`  // max body capture bytes, default 65536
	Conditions    TrafficReplayConditions    `yaml:"conditions"`
}

// TrafficReplayConditions defines filtering conditions for traffic recording.
type TrafficReplayConditions struct {
	Methods   []string `yaml:"methods"`
	PathRegex string   `yaml:"path_regex"`
}

// ConsistentHashConfig defines consistent hash load balancer settings.
type ConsistentHashConfig struct {
	Key        string `yaml:"key"`         // "header"|"cookie"|"path"|"ip"
	HeaderName string `yaml:"header_name"` // required for header/cookie
	Replicas   int    `yaml:"replicas"`    // virtual nodes per backend, default 150
}

// RetryConfig defines retry policy settings
type RetryConfig struct {
	MaxRetries        int           `yaml:"max_retries"`
	InitialBackoff    time.Duration `yaml:"initial_backoff"`
	MaxBackoff        time.Duration `yaml:"max_backoff"`
	BackoffMultiplier float64       `yaml:"backoff_multiplier"`
	RetryableStatuses []int         `yaml:"retryable_statuses"`
	RetryableMethods  []string      `yaml:"retryable_methods"`
	PerTryTimeout     time.Duration `yaml:"per_try_timeout"`
	Budget            BudgetConfig  `yaml:"budget"`
	BudgetPool        string        `yaml:"budget_pool"` // reference to named shared budget in Config.RetryBudgets
	Hedging           HedgingConfig `yaml:"hedging"`
}

// BudgetConfig defines retry budget settings to prevent retry storms.
type BudgetConfig struct {
	Ratio      float64       `yaml:"ratio"`       // max ratio of retries to total requests (0.0-1.0)
	MinRetries int           `yaml:"min_retries"` // always allow at least N retries/sec
	Window     time.Duration `yaml:"window"`      // sliding window (default 10s)
}

// HedgingConfig defines request hedging settings.
type HedgingConfig struct {
	Enabled     bool          `yaml:"enabled"`
	MaxRequests int           `yaml:"max_requests"` // total concurrent (original + hedged), default 2
	Delay       time.Duration `yaml:"delay"`        // wait before hedging
}

// TimeoutConfig defines timeout policy settings
type TimeoutConfig struct {
	Request       time.Duration `yaml:"request"`
	Idle          time.Duration `yaml:"idle"`
	Backend       time.Duration `yaml:"backend"`
	HeaderTimeout time.Duration `yaml:"header_timeout"`
}

// IsActive returns true if any timeout is configured.
func (c TimeoutConfig) IsActive() bool {
	return c.Request > 0 || c.Idle > 0 || c.Backend > 0 || c.HeaderTimeout > 0
}

// CircuitBreakerConfig defines circuit breaker settings
type CircuitBreakerConfig struct {
	Enabled          bool          `yaml:"enabled"`
	FailureThreshold int           `yaml:"failure_threshold"`
	MaxRequests      int           `yaml:"max_requests"`
	Timeout          time.Duration `yaml:"timeout"`
	Mode             string        `yaml:"mode"`              // "local" (default) or "distributed"
	TenantIsolation  bool          `yaml:"tenant_isolation"`  // when true, each tenant gets its own circuit breaker
}

// CacheConfig defines request caching settings
type CacheConfig struct {
	Enabled              bool          `yaml:"enabled"`
	TTL                  time.Duration `yaml:"ttl"`
	MaxSize              int           `yaml:"max_size"`
	MaxBodySize          int64         `yaml:"max_body_size"`
	KeyHeaders           []string      `yaml:"key_headers"`
	Methods              []string      `yaml:"methods"`
	Mode                 string        `yaml:"mode"`                   // "local" (default) or "distributed" (Redis-backed)
	Conditional          bool          `yaml:"conditional"`            // enable ETag/Last-Modified/304 support
	Bucket               string        `yaml:"bucket"`                 // named shared cache bucket (routes with same bucket share a store)
	StaleWhileRevalidate time.Duration `yaml:"stale_while_revalidate"` // serve stale while refreshing in background
	StaleIfError         time.Duration `yaml:"stale_if_error"`         // serve stale on backend 5xx errors
	TagHeaders           []string      `yaml:"tag_headers"`            // response headers to extract cache tags from (values split on space/comma)
	Tags                 []string      `yaml:"tags"`                   // static tags applied to all entries on this route
}

// WebSocketConfig defines WebSocket proxy settings
type WebSocketConfig struct {
	Enabled         bool          `yaml:"enabled"`
	ReadBufferSize  int           `yaml:"read_buffer_size"`
	WriteBufferSize int           `yaml:"write_buffer_size"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	PingInterval    time.Duration `yaml:"ping_interval"`
	PongTimeout     time.Duration `yaml:"pong_timeout"`
}

// SSEConfig defines Server-Sent Events proxy settings.
type SSEConfig struct {
	Enabled            bool           `yaml:"enabled"`
	HeartbeatInterval  time.Duration  `yaml:"heartbeat_interval"`   // send `: heartbeat\n\n` on idle (0 = disabled)
	RetryMS            int            `yaml:"retry_ms"`             // inject `retry:` field on connect (0 = don't inject)
	ConnectEvent       string         `yaml:"connect_event"`        // event data to send on connect (empty = none)
	DisconnectEvent    string         `yaml:"disconnect_event"`     // event data to send on disconnect (empty = none)
	MaxIdle            time.Duration  `yaml:"max_idle"`             // close connection after idle (0 = no limit)
	ForwardLastEventID bool           `yaml:"forward_last_event_id"` // forward Last-Event-ID header to backend (default true)
	Fanout             SSEFanoutConfig `yaml:"fanout"`
}

// SSEFanoutConfig defines SSE fan-out settings.
type SSEFanoutConfig struct {
	Enabled          bool          `yaml:"enabled"`
	BufferSize       int           `yaml:"buffer_size"`        // ring buffer for catch-up (default 256)
	ClientBufferSize int           `yaml:"client_buffer_size"` // per-client channel buffer (default 64)
	ReconnectDelay   time.Duration `yaml:"reconnect_delay"`    // upstream reconnect delay (default 1s)
	MaxReconnects    int           `yaml:"max_reconnects"`     // 0=unlimited
	EventFiltering   bool          `yaml:"event_filtering"`    // clients filter by event type via query param
	FilterParam      string        `yaml:"filter_param"`       // query param name (default "event_type")
}

// IPFilterConfig defines IP allow/deny list settings (Feature 2)
type IPFilterConfig struct {
	Enabled bool     `yaml:"enabled"`
	Allow   []string `yaml:"allow"`        // CIDR list
	Deny    []string `yaml:"deny"`         // CIDR list
	Order   string   `yaml:"order"`        // "allow_first" or "deny_first"
}

// CORSConfig defines CORS settings (Feature 3)
type CORSConfig struct {
	Enabled             bool     `yaml:"enabled"`
	AllowOrigins        []string `yaml:"allow_origins"`
	AllowOriginPatterns []string `yaml:"allow_origin_patterns"` // regex patterns
	AllowMethods        []string `yaml:"allow_methods"`
	AllowHeaders        []string `yaml:"allow_headers"`
	ExposeHeaders       []string `yaml:"expose_headers"`
	AllowCredentials    bool     `yaml:"allow_credentials"`
	AllowPrivateNetwork bool     `yaml:"allow_private_network"` // Access-Control-Allow-Private-Network
	MaxAge              int      `yaml:"max_age"`               // seconds
}

// CompressionConfig defines response compression settings (Feature 4)
type CompressionConfig struct {
	Enabled      bool     `yaml:"enabled"`
	Level        int      `yaml:"level"`         // 0-11, default 6
	MinSize      int      `yaml:"min_size"`      // default 1024 bytes
	ContentTypes []string `yaml:"content_types"` // MIME types to compress
	Algorithms   []string `yaml:"algorithms"`    // "gzip", "br", "zstd"; default all three
}

// RequestDecompressionConfig controls automatic request body decompression.
type RequestDecompressionConfig struct {
	Enabled             bool     `yaml:"enabled"`
	Algorithms          []string `yaml:"algorithms"`            // "gzip", "deflate", "br", "zstd"; default all four
	MaxDecompressedSize int64    `yaml:"max_decompressed_size"` // zip bomb protection; default 52428800 (50MB)
}

// ResponseLimitConfig controls maximum response body size from backends.
type ResponseLimitConfig struct {
	Enabled bool   `yaml:"enabled"`
	MaxSize int64  `yaml:"max_size"` // max response body in bytes
	Action  string `yaml:"action"`   // "reject" (default: 502 if known, discard if streaming), "truncate", "log_only"
}

// SecurityHeadersConfig defines automatic security response headers.
type SecurityHeadersConfig struct {
	Enabled                    bool   `yaml:"enabled"`
	StrictTransportSecurity    string `yaml:"strict_transport_security"`     // HSTS, e.g. "max-age=31536000; includeSubDomains"
	ContentSecurityPolicy      string `yaml:"content_security_policy"`       // CSP header value
	XContentTypeOptions        string `yaml:"x_content_type_options"`        // default "nosniff"
	XFrameOptions              string `yaml:"x_frame_options"`               // e.g. "DENY", "SAMEORIGIN"
	ReferrerPolicy             string `yaml:"referrer_policy"`               // e.g. "strict-origin-when-cross-origin"
	PermissionsPolicy          string `yaml:"permissions_policy"`            // e.g. "camera=(), microphone=()"
	CrossOriginOpenerPolicy    string `yaml:"cross_origin_opener_policy"`    // e.g. "same-origin"
	CrossOriginEmbedderPolicy  string `yaml:"cross_origin_embedder_policy"`  // e.g. "require-corp"
	CrossOriginResourcePolicy  string `yaml:"cross_origin_resource_policy"`  // e.g. "same-origin"
	XPermittedCrossDomainPolicies string `yaml:"x_permitted_cross_domain_policies"` // e.g. "none"
	CustomHeaders              map[string]string `yaml:"custom_headers"`    // arbitrary extra headers
}

// MaintenanceConfig defines maintenance mode settings.
type MaintenanceConfig struct {
	Enabled     bool              `yaml:"enabled"`
	StatusCode  int               `yaml:"status_code"`   // default 503
	Body        string            `yaml:"body"`           // response body (default JSON message)
	ContentType string            `yaml:"content_type"`   // default "application/json"
	RetryAfter  string            `yaml:"retry_after"`    // Retry-After header value (seconds or HTTP-date)
	ExcludePaths []string         `yaml:"exclude_paths"`  // paths that bypass maintenance (glob patterns)
	ExcludeIPs   []string         `yaml:"exclude_ips"`    // CIDRs that bypass maintenance
	Headers     map[string]string `yaml:"headers"`        // extra response headers
}

// TrustedProxiesConfig defines trusted proxy settings for real client IP extraction.
type TrustedProxiesConfig struct {
	CIDRs   []string `yaml:"cidrs"`    // trusted proxy CIDRs (e.g. "10.0.0.0/8", "127.0.0.1/32")
	Headers []string `yaml:"headers"`  // headers to check for client IP (default: X-Forwarded-For, X-Real-IP)
	MaxHops int      `yaml:"max_hops"` // maximum number of hops to walk back in XFF chain (0 = unlimited)
}

// ShutdownConfig defines graceful shutdown settings.
type ShutdownConfig struct {
	Timeout    time.Duration `yaml:"timeout"`     // total shutdown timeout (default 30s)
	DrainDelay time.Duration `yaml:"drain_delay"` // delay before stopping listeners (default 0s)
}

// BotDetectionConfig defines User-Agent based bot blocking.
type BotDetectionConfig struct {
	Enabled bool     `yaml:"enabled"`
	Deny    []string `yaml:"deny"`  // regex patterns to block
	Allow   []string `yaml:"allow"` // regex patterns to allow (bypass deny)
}

// AICrawlConfig defines AI crawler detection and policy enforcement.
type AICrawlConfig struct {
	Enabled          bool                  `yaml:"enabled"`
	DefaultAction    string                `yaml:"default_action"`     // "monitor" (default), "allow", "block"
	Policies         []AICrawlPolicyConfig `yaml:"policies"`
	CustomCrawlers   []CustomCrawlerConfig `yaml:"custom_crawlers"`
	BlockStatus      int                   `yaml:"block_status"`       // HTTP status for blocked requests (default 403)
	BlockBody        string                `yaml:"block_body"`         // optional response body (sent as-is)
	BlockContentType string                `yaml:"block_content_type"` // Content-Type for block response (default "text/plain")
	ExposeHeaders    bool                  `yaml:"expose_headers"`     // add X-AI-Crawler-* response headers (default false)
}

// AICrawlPolicyConfig defines a per-crawler policy.
type AICrawlPolicyConfig struct {
	Crawler       string   `yaml:"crawler"`        // name matching built-in or custom crawler
	Action        string   `yaml:"action"`         // "allow", "block", "monitor"
	DisallowPaths []string `yaml:"disallow_paths"` // doublestar glob patterns to block
	AllowPaths    []string `yaml:"allow_paths"`    // doublestar glob patterns to exclusively allow
}

// CustomCrawlerConfig defines a user-provided AI crawler pattern.
type CustomCrawlerConfig struct {
	Name    string `yaml:"name"`
	Pattern string `yaml:"pattern"` // regex for User-Agent matching
}

// ClientMTLSConfig defines per-route client certificate verification.
type ClientMTLSConfig struct {
	Enabled      bool     `yaml:"enabled"`
	ClientAuth   string   `yaml:"client_auth"`    // "request"|"require"|"verify" (default "verify")
	ClientCAFile string   `yaml:"client_ca_file"` // single CA PEM path
	ClientCAs    []string `yaml:"client_cas"`     // multiple CA PEM paths
	AllowExpired bool     `yaml:"allow_expired"`  // skip expiry check (testing)
}

// ProxyRateLimitConfig defines backend-side rate limiting.
type ProxyRateLimitConfig struct {
	Enabled bool          `yaml:"enabled"`
	Rate    int           `yaml:"rate"`   // requests per period to backend
	Period  time.Duration `yaml:"period"` // time period
	Burst   int           `yaml:"burst"`
}

// ETagConfig defines ETag generation and conditional request handling.
type ETagConfig struct {
	Enabled bool `yaml:"enabled"`
	Weak    bool `yaml:"weak"` // prefix ETag with W/ for weak validation
}

// StreamingConfig defines response streaming controls.
type StreamingConfig struct {
	Enabled          bool          `yaml:"enabled"`
	FlushInterval    time.Duration `yaml:"flush_interval"`
	DisableBuffering bool          `yaml:"disable_buffering"`
}

// OPAConfig defines Open Policy Agent (OPA) policy engine settings.
type OPAConfig struct {
	Enabled     bool          `yaml:"enabled"`
	URL         string        `yaml:"url"`
	PolicyPath  string        `yaml:"policy_path"`
	Timeout     time.Duration `yaml:"timeout"`
	FailOpen    bool          `yaml:"fail_open"`
	IncludeBody bool          `yaml:"include_body"`
	CacheTTL    time.Duration `yaml:"cache_ttl"`
	Headers     []string      `yaml:"headers"`
}

// MockResponseConfig defines static mock responses.
type MockResponseConfig struct {
	Enabled       bool              `yaml:"enabled"`
	StatusCode    int               `yaml:"status_code"`
	Headers       map[string]string `yaml:"headers"`
	Body          string            `yaml:"body"`
	FromSpec      bool              `yaml:"from_spec"`       // generate mock responses from OpenAPI spec
	DefaultStatus int               `yaml:"default_status"`  // which response status to mock (default 200)
	Seed          int64             `yaml:"seed"`            // deterministic fake data seed (0 = random)
}

// HTTPSRedirectConfig defines automatic HTTP→HTTPS redirect settings.
type HTTPSRedirectConfig struct {
	Enabled   bool `yaml:"enabled"`
	Port      int  `yaml:"port"`      // target HTTPS port (default 443)
	Permanent bool `yaml:"permanent"` // true=301, false=302 (default)
}

// AllowedHostsConfig defines Host header validation settings.
type AllowedHostsConfig struct {
	Enabled bool     `yaml:"enabled"`
	Hosts   []string `yaml:"hosts"` // exact or "*.example.com" wildcard
}

// ClaimsPropagationConfig defines JWT claims propagation to backend headers.
type ClaimsPropagationConfig struct {
	Enabled bool              `yaml:"enabled"`
	Claims  map[string]string `yaml:"claims"` // claim_name -> header_name
}

// TokenRevocationConfig defines JWT token revocation / blocklist settings.
type TokenRevocationConfig struct {
	Enabled    bool          `yaml:"enabled"`
	Mode       string        `yaml:"mode"`        // "local" (default) or "distributed"
	DefaultTTL time.Duration `yaml:"default_ttl"` // default 24h
}

// BackendAuthConfig defines OAuth2 client_credentials token injection for backend calls.
type BackendAuthConfig struct {
	Enabled      bool              `yaml:"enabled"`
	Type         string            `yaml:"type"`          // "oauth2_client_credentials"
	TokenURL     string            `yaml:"token_url"`
	ClientID     string            `yaml:"client_id"`
	ClientSecret string            `yaml:"client_secret"`
	Scopes       []string          `yaml:"scopes"`
	ExtraParams  map[string]string `yaml:"extra_params"`
	Timeout      time.Duration     `yaml:"timeout"` // default 10s
}

// StatusMappingConfig defines per-route backend response status code remapping.
type StatusMappingConfig struct {
	Enabled  bool        `yaml:"enabled"`
	Mappings map[int]int `yaml:"mappings"` // backend_code -> client_code
}

// StaticConfig defines static file serving for a route (replaces proxy).
type StaticConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Root         string `yaml:"root"`          // directory path
	Index        string `yaml:"index"`         // default "index.html"
	Browse       bool   `yaml:"browse"`        // directory listing (default false)
	CacheControl string `yaml:"cache_control"` // Cache-Control header value
}

// FastCGIConfig defines FastCGI proxy settings for a route (replaces proxy).
type FastCGIConfig struct {
	Enabled      bool              `yaml:"enabled"`
	Address      string            `yaml:"address"`        // "host:port" (TCP) or "/path/to.sock" (unix)
	Network      string            `yaml:"network"`        // "tcp" or "unix"; auto-detected if empty
	DocumentRoot string            `yaml:"document_root"`  // DOCUMENT_ROOT / SCRIPT_FILENAME base
	ScriptName   string            `yaml:"script_name"`    // fixed entry point (e.g. "/index.php"); empty = filesystem mode
	Index        string            `yaml:"index"`          // default index file (default "index.php")
	ConnTimeout  time.Duration     `yaml:"conn_timeout"`   // connection timeout (default 5s)
	ReadTimeout  time.Duration     `yaml:"read_timeout"`   // read timeout (default 30s)
	Params       map[string]string `yaml:"params"`         // extra FastCGI params
	PoolSize     int               `yaml:"pool_size"`      // connection pool size (default 8)
}

// DeprecationConfig defines API deprecation lifecycle settings (RFC 8594).
type DeprecationConfig struct {
	Enabled             bool            `yaml:"enabled"`
	SunsetDate          string          `yaml:"sunset_date"`           // RFC 3339
	Message             string          `yaml:"message"`               // human-readable notice
	Link                string          `yaml:"link"`                  // URL to replacement API
	LinkRelation        string          `yaml:"link_relation"`         // default "successor-version"
	ResponseAfterSunset *SunsetResponse `yaml:"response_after_sunset"` // optional blocking after sunset
	LogLevel            string          `yaml:"log_level"`             // "warn" (default) or "info"
}

// SunsetResponse defines the response returned after a sunset date has passed.
type SunsetResponse struct {
	Status  int               `yaml:"status"`  // default 410
	Body    string            `yaml:"body"`
	Headers map[string]string `yaml:"headers"`
}

// ConsumerGroupsConfig defines consumer group settings.
type ConsumerGroupsConfig struct {
	Enabled bool                       `yaml:"enabled"`
	Groups  map[string]ConsumerGroup   `yaml:"groups"`
}

// ConsumerGroup defines a single consumer group with resource policies and metadata.
type ConsumerGroup struct {
	RateLimit int               `yaml:"rate_limit"`
	Quota     int64             `yaml:"quota"`
	Priority  int               `yaml:"priority"`
	Metadata  map[string]string `yaml:"metadata"`
}

// SLOConfig defines SLI/SLO enforcement settings for a route.
type SLOConfig struct {
	Enabled         bool          `yaml:"enabled"`
	Target          float64       `yaml:"target"`            // e.g. 0.999
	Window          time.Duration `yaml:"window"`            // e.g. "720h"
	Actions         []string      `yaml:"actions"`           // "log_warning", "add_header", "shed_load"
	ShedLoadPercent float64       `yaml:"shed_load_percent"` // 0-100, default 10
	ErrorCodes      []int         `yaml:"error_codes"`       // default: 500-599
}

// ConnectConfig defines HTTP CONNECT tunneling settings for a route.
type ConnectConfig struct {
	Enabled        bool          `yaml:"enabled"`
	AllowedHosts   []string      `yaml:"allowed_hosts"`
	AllowedPorts   []int         `yaml:"allowed_ports"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	IdleTimeout    time.Duration `yaml:"idle_timeout"`
	MaxTunnels     int           `yaml:"max_tunnels"`
}

// AIConfig configures the AI gateway for a route.
type AIConfig struct {
	Enabled       bool              `yaml:"enabled"`
	Provider      string            `yaml:"provider"`        // "openai", "anthropic", "azure_openai", "gemini"
	Model         string            `yaml:"model"`           // default model
	ModelMapping  map[string]string `yaml:"model_mapping"`   // client model → provider model
	APIKey        string            `yaml:"api_key"`         // env var reference: ${OPENAI_API_KEY}
	BaseURL       string            `yaml:"base_url"`        // override provider base URL
	APIVersion    string            `yaml:"api_version"`     // Azure: required
	DeploymentID  string            `yaml:"deployment_id"`   // Azure: required
	ProjectID     string            `yaml:"project_id"`      // Gemini: GCP project
	Region        string            `yaml:"region"`          // Gemini: GCP region
	OrgID         string            `yaml:"org_id"`          // OpenAI: organization
	Timeout       time.Duration     `yaml:"timeout"`         // per-request timeout (default 60s)
	MaxTokens     int               `yaml:"max_tokens"`      // enforce cap (overrides client if larger)
	Temperature   *float64          `yaml:"temperature"`     // override (nil = use client value)
	StreamDefault bool              `yaml:"stream_default"`  // stream by default if client omits
	PassHeaders   []string          `yaml:"pass_headers"`    // client→provider header forwarding
	IdleTimeout   time.Duration     `yaml:"idle_timeout"`    // per-event SSE idle timeout (default 30s)
	MaxBodySize   int64             `yaml:"max_body_size"`   // max request body read (default 10MB)

	PromptGuard    AIPromptGuardConfig    `yaml:"prompt_guard"`
	PromptDecorate AIPromptDecorateConfig `yaml:"prompt_decorate"`
	RateLimit      AIRateLimitConfig      `yaml:"rate_limit"`
}

// AIPromptGuardConfig configures prompt injection detection.
type AIPromptGuardConfig struct {
	DenyPatterns  []string `yaml:"deny_patterns"`
	AllowPatterns []string `yaml:"allow_patterns"`
	DenyAction    string   `yaml:"deny_action"`    // "block" (default) or "log"
	MaxPromptLen  int      `yaml:"max_prompt_len"` // 0 = unlimited
}

// AIPromptDecorateConfig configures message prepend/append.
type AIPromptDecorateConfig struct {
	Prepend []AIPromptMessage `yaml:"prepend"`
	Append  []AIPromptMessage `yaml:"append"`
}

// AIPromptMessage defines a message to prepend/append to chat completions.
type AIPromptMessage struct {
	Role    string `yaml:"role"`    // "system", "user", "assistant"
	Content string `yaml:"content"`
}

// AIRateLimitConfig configures token-based rate limiting for AI routes.
type AIRateLimitConfig struct {
	TokensPerMinute int64  `yaml:"tokens_per_minute"`
	TokensPerDay    int64  `yaml:"tokens_per_day"`
	Key             string `yaml:"key"` // "ip", "client_id", "header:<name>", etc.
}

// RewriteConfig defines URL rewriting rules for a route.
type RewriteConfig struct {
	Prefix      string `yaml:"prefix"`      // replace matched path prefix with this value
	Regex       string `yaml:"regex"`       // regex pattern to match on request path
	Replacement string `yaml:"replacement"` // replacement string for regex (supports $1, $2 capture groups)
	Host        string `yaml:"host"`        // override Host header sent to backend
	URL         string `yaml:"url"`         // full URL override (scheme://host:port/path?query) — takes precedence when set
}

// MetricsConfig defines Prometheus metrics settings (Feature 5)
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"` // default "/metrics"
}

// TrafficSplitConfig defines canary/weighted traffic split settings (Feature 6)
type TrafficSplitConfig struct {
	Name         string            `yaml:"name"`
	Weight       int               `yaml:"weight"`        // percentage 0-100
	Backends     []BackendConfig   `yaml:"backends"`
	Upstream     string            `yaml:"upstream"`      // reference to named upstream (alternative to inline backends)
	MatchHeaders map[string]string `yaml:"match_headers"` // header-based override
}

// ValidationConfig defines request validation settings (Feature 8)
type ValidationConfig struct {
	Enabled            bool   `yaml:"enabled"`
	SchemaFile         string `yaml:"schema_file"`          // path to JSON schema file
	Schema             string `yaml:"schema"`               // inline JSON schema
	ResponseSchemaFile string `yaml:"response_schema_file"` // path to response JSON schema file
	ResponseSchema     string `yaml:"response_schema"`      // inline response JSON schema
	LogOnly            bool   `yaml:"log_only"`             // log instead of reject
}

// TracingConfig defines distributed tracing settings (Feature 9)
type TracingConfig struct {
	Enabled     bool              `yaml:"enabled"`
	Exporter    string            `yaml:"exporter"`     // "otlp"
	Endpoint    string            `yaml:"endpoint"`
	ServiceName string            `yaml:"service_name"`
	SampleRate  float64           `yaml:"sample_rate"`  // 0.0 to 1.0
	Insecure    bool              `yaml:"insecure"`     // use insecure gRPC connection
	Headers     map[string]string `yaml:"headers"`      // extra headers for OTLP exporter
}

// MirrorConfig defines traffic mirroring settings (Feature 10)
type MirrorConfig struct {
	Enabled    bool                   `yaml:"enabled"`
	Backends   []BackendConfig        `yaml:"backends"`
	Upstream   string                 `yaml:"upstream"`   // reference to named upstream (alternative to inline backends)
	Percentage int                    `yaml:"percentage"` // 0-100
	Conditions MirrorConditionsConfig `yaml:"conditions"`
	Compare    MirrorCompareConfig    `yaml:"compare"`
}

// MirrorConditionsConfig defines conditions for when to mirror requests.
type MirrorConditionsConfig struct {
	Methods   []string          `yaml:"methods"`
	Headers   map[string]string `yaml:"headers"`
	PathRegex string            `yaml:"path_regex"`
}

// MirrorCompareConfig defines response comparison settings for mirrored traffic.
type MirrorCompareConfig struct {
	Enabled          bool     `yaml:"enabled"`
	LogMismatches    bool     `yaml:"log_mismatches"`
	DetailedDiff     bool     `yaml:"detailed_diff"`
	MaxBodyCapture   int64    `yaml:"max_body_capture"`
	MaxMismatches    int      `yaml:"max_mismatches"`
	IgnoreHeaders    []string `yaml:"ignore_headers"`
	IgnoreJSONFields []string `yaml:"ignore_json_fields"`
}

// GRPCConfig defines gRPC proxying settings (Feature 12)
type GRPCConfig struct {
	Enabled             bool                   `yaml:"enabled"`
	DeadlinePropagation bool                   `yaml:"deadline_propagation"`
	MaxRecvMsgSize      int                    `yaml:"max_recv_msg_size"` // bytes, 0=unlimited
	MaxSendMsgSize      int                    `yaml:"max_send_msg_size"` // bytes, 0=unlimited
	Authority           string                 `yaml:"authority"`         // override :authority
	MetadataTransforms  GRPCMetadataTransforms `yaml:"metadata_transforms"`
	HealthCheck         GRPCHealthCheckConfig  `yaml:"health_check"`
	Reflection          GRPCReflectionConfig   `yaml:"reflection"`
}

// GRPCReflectionConfig defines gRPC reflection proxy settings.
type GRPCReflectionConfig struct {
	Enabled  bool          `yaml:"enabled"`
	CacheTTL time.Duration `yaml:"cache_ttl"` // default 5m
}

// GRPCMetadataTransforms defines metadata mapping rules for gRPC proxying.
type GRPCMetadataTransforms struct {
	RequestMap  map[string]string `yaml:"request_map"`  // HTTP header → gRPC metadata
	ResponseMap map[string]string `yaml:"response_map"` // gRPC metadata → HTTP header
	StripPrefix string            `yaml:"strip_prefix"` // auto-strip prefix from headers
	Passthrough []string          `yaml:"passthrough"`  // pass as-is
}

// GRPCHealthCheckConfig defines gRPC health check settings.
type GRPCHealthCheckConfig struct {
	Enabled bool   `yaml:"enabled"` // use grpc.health.v1 instead of HTTP
	Service string `yaml:"service"` // service name (empty = overall)
}

// ProtocolConfig defines protocol translation settings per route.
type ProtocolConfig struct {
	Type    string                 `yaml:"type"` // "http_to_grpc", "http_to_thrift", "grpc_to_rest", "rest_to_graphql", "rest_to_soap"
	GRPC    GRPCTranslateConfig    `yaml:"grpc"`
	Thrift  ThriftTranslateConfig  `yaml:"thrift"`
	REST    RESTTranslateConfig    `yaml:"rest"`
	GraphQL GraphQLProtocolConfig  `yaml:"graphql"`
	SOAP    SOAPProtocolConfig     `yaml:"soap"`
	GRPCWeb GRPCWebTranslateConfig `yaml:"grpc_web"`
}

// RESTTranslateConfig defines gRPC-to-REST translation settings.
type RESTTranslateConfig struct {
	Timeout         time.Duration       `yaml:"timeout"`          // default 30s
	DescriptorFiles []string            `yaml:"descriptor_files"` // .pb descriptor set paths
	Mappings        []GRPCToRESTMapping `yaml:"mappings"`         // required
}

// GRPCToRESTMapping defines a gRPC method to REST endpoint mapping.
type GRPCToRESTMapping struct {
	GRPCService string `yaml:"grpc_service"` // fully-qualified service name
	GRPCMethod  string `yaml:"grpc_method"`  // method name
	HTTPMethod  string `yaml:"http_method"`  // GET/POST/PUT/DELETE/PATCH
	HTTPPath    string `yaml:"http_path"`    // /users/{user_id}
	Body        string `yaml:"body"`         // "*"=whole body, ""=query params only
}

// GRPCTranslateConfig defines HTTP-to-gRPC translation settings.
type GRPCTranslateConfig struct {
	Service            string              `yaml:"service"`              // optional: fully-qualified service name
	Method             string              `yaml:"method"`               // optional: fixed gRPC method name (requires service)
	Timeout            time.Duration       `yaml:"timeout"`              // per-call timeout (default 30s)
	DescriptorCacheTTL time.Duration       `yaml:"descriptor_cache_ttl"` // default 5m
	TLS                ProtocolTLSConfig   `yaml:"tls"`
	Mappings           []GRPCMethodMapping `yaml:"mappings"` // REST-to-gRPC method mappings
}

// GRPCMethodMapping defines a REST-to-gRPC method mapping.
type GRPCMethodMapping struct {
	HTTPMethod string `yaml:"http_method"` // GET, POST, PUT, DELETE, PATCH
	HTTPPath   string `yaml:"http_path"`   // /users/:user_id or /users/{user_id}
	GRPCMethod string `yaml:"grpc_method"` // GetUser (method name within the service)
	Body       string `yaml:"body"`        // "*" = whole body, "field" = nested under field, "" = no body (use params only)
}

// ProtocolTLSConfig defines TLS settings for protocol translation.
type ProtocolTLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
}

// ThriftTranslateConfig defines HTTP-to-Thrift translation settings.
type ThriftTranslateConfig struct {
	IDLFile     string                `yaml:"idl_file"`     // path to .thrift IDL file (alternative to methods)
	Service     string                `yaml:"service"`      // Thrift service name (required)
	Method      string                `yaml:"method"`       // optional: fixed method name
	Timeout     time.Duration         `yaml:"timeout"`      // per-call timeout (default 30s)
	Protocol    string                `yaml:"protocol"`     // "binary" (default) or "compact"
	Transport   string                `yaml:"transport"`    // "framed" (default) or "buffered"
	Multiplexed bool                  `yaml:"multiplexed"`  // use TMultiplexedProtocol
	TLS         ProtocolTLSConfig     `yaml:"tls"`
	Mappings    []ThriftMethodMapping `yaml:"mappings"` // REST-to-Thrift method mappings
	Methods     map[string]ThriftMethodDef  `yaml:"methods"`  // inline method definitions (alternative to idl_file)
	Structs     map[string][]ThriftFieldDef `yaml:"structs"`  // inline struct definitions
	Enums       map[string]map[string]int   `yaml:"enums"`    // inline enum definitions
}

// ThriftMethodDef defines an inline Thrift method schema.
type ThriftMethodDef struct {
	Args   []ThriftFieldDef `yaml:"args"`
	Result []ThriftFieldDef `yaml:"result"` // field 0 = success return, 1+ = exceptions
	Oneway bool             `yaml:"oneway"`
	Void   bool             `yaml:"void"`
}

// ThriftFieldDef defines a Thrift field with its ID, name, and type info.
type ThriftFieldDef struct {
	ID     int32  `yaml:"id"`
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`    // bool, byte, i16, i32, i64, double, string, binary, struct, list, set, map, or enum name
	Struct string `yaml:"struct"`  // struct name (when type=struct)
	Elem   string `yaml:"elem"`    // element type (when type=list/set)
	Key    string `yaml:"key"`     // key type (when type=map)
	Value  string `yaml:"value"`   // value type (when type=map)
}

// ThriftMethodMapping defines a REST-to-Thrift method mapping.
type ThriftMethodMapping struct {
	HTTPMethod   string `yaml:"http_method"`   // GET, POST, PUT, DELETE, PATCH
	HTTPPath     string `yaml:"http_path"`     // /users/:user_id or /users/{user_id}
	ThriftMethod string `yaml:"thrift_method"` // GetUser (method name within the service)
	Body         string `yaml:"body"`          // "*" = whole body, "field" = nested under field, "" = no body
}

// WAFConfig defines Web Application Firewall settings.
type WAFConfig struct {
	Enabled      bool     `yaml:"enabled"`
	Mode         string   `yaml:"mode"`           // "block" or "detect" (log only)
	RuleFiles    []string `yaml:"rule_files"`     // paths to SecLang rule files
	InlineRules  []string `yaml:"inline_rules"`   // inline SecLang rules
	SQLInjection bool     `yaml:"sql_injection"`  // enable built-in SQLi rules
	XSS          bool     `yaml:"xss"`            // enable built-in XSS rules
}

// GraphQLConfig defines GraphQL query analysis and protection settings.
type GraphQLConfig struct {
	Enabled          bool                      `yaml:"enabled"`
	MaxDepth         int                       `yaml:"max_depth"`         // 0 = unlimited
	MaxComplexity    int                       `yaml:"max_complexity"`    // 0 = unlimited
	Introspection    bool                      `yaml:"introspection"`     // allow introspection (default false)
	OperationLimits  map[string]int            `yaml:"operation_limits"`  // e.g. {"query": 100, "mutation": 10} req/s
	PersistedQueries PersistedQueriesConfig    `yaml:"persisted_queries"` // Automatic Persisted Queries (APQ)
	Subscriptions    GraphQLSubscriptionConfig `yaml:"subscriptions"`     // GraphQL subscription (WebSocket) settings
	Batching         GraphQLBatchingConfig     `yaml:"batching"`          // Query batching settings
}

// GraphQLBatchingConfig defines GraphQL query batching settings.
type GraphQLBatchingConfig struct {
	Enabled      bool   `yaml:"enabled"`
	MaxBatchSize int    `yaml:"max_batch_size"` // max queries per batch (default 10, 0 = unlimited)
	Mode         string `yaml:"mode"`           // "pass_through" or "split" (default "pass_through")
}

// PersistedQueriesConfig defines GraphQL Automatic Persisted Queries settings.
type PersistedQueriesConfig struct {
	Enabled bool `yaml:"enabled"`
	MaxSize int  `yaml:"max_size"` // LRU max entries, default 1000
}

// GraphQLSubscriptionConfig defines GraphQL subscription (WebSocket) settings.
type GraphQLSubscriptionConfig struct {
	Enabled        bool          `yaml:"enabled"`
	Protocol       string        `yaml:"protocol"`        // "graphql-ws" or "graphql-transport-ws" (default)
	PingInterval   time.Duration `yaml:"ping_interval"`   // keepalive ping interval (default 30s)
	MaxConnections int           `yaml:"max_connections"` // max concurrent subscription connections per route (0 = unlimited)
}

// GraphQLFederationConfig defines GraphQL federation / schema stitching settings.
type GraphQLFederationConfig struct {
	Enabled         bool                      `yaml:"enabled"`
	RefreshInterval time.Duration             `yaml:"refresh_interval"` // schema re-introspection interval (default 5m)
	Sources         []GraphQLFederationSource `yaml:"sources"`          // backend GraphQL sources (>= 2)
}

// GraphQLFederationSource defines a backend GraphQL source for federation.
type GraphQLFederationSource struct {
	Name string `yaml:"name"` // unique source name
	URL  string `yaml:"url"`  // backend GraphQL endpoint URL
}

// CoalesceConfig defines request coalescing (singleflight) settings.
type CoalesceConfig struct {
	Enabled    bool          `yaml:"enabled"`
	Timeout    time.Duration `yaml:"timeout"`      // max wait for coalesced requests (default 30s)
	KeyHeaders []string      `yaml:"key_headers"`  // headers included in coalesce key
	Methods    []string      `yaml:"methods"`      // eligible methods (default GET+HEAD)
}

// CanaryConfig defines canary deployment settings.
type CanaryConfig struct {
	Enabled     bool                 `yaml:"enabled"`
	CanaryGroup string               `yaml:"canary_group"`
	AutoStart   bool                 `yaml:"auto_start"`
	Steps       []CanaryStepConfig   `yaml:"steps"`
	Analysis    CanaryAnalysisConfig `yaml:"analysis"`
}

// CanaryStepConfig defines a single canary weight step.
type CanaryStepConfig struct {
	Weight int           `yaml:"weight"` // 0-100
	Pause  time.Duration `yaml:"pause"`  // hold duration before next step
}

// CanaryAnalysisConfig defines health analysis thresholds for canary rollback.
type CanaryAnalysisConfig struct {
	ErrorThreshold       float64       `yaml:"error_threshold"`         // 0.0-1.0
	LatencyThreshold     time.Duration `yaml:"latency_threshold"`       // max p99
	MaxErrorRateIncrease float64       `yaml:"max_error_rate_increase"` // canary/baseline error ratio (0 = disabled)
	MaxLatencyIncrease   float64       `yaml:"max_latency_increase"`    // canary/baseline p99 ratio (0 = disabled)
	MaxFailures          int           `yaml:"max_failures"`            // consecutive failures before rollback (0 = immediate)
	MinRequests          int           `yaml:"min_requests"`            // min samples before eval
	Interval             time.Duration `yaml:"interval"`                // eval frequency
}

// ExtAuthConfig configures external authentication for a route.
type ExtAuthConfig struct {
	Enabled         bool             `yaml:"enabled"`
	URL             string           `yaml:"url"`               // http:// or grpc:// URL
	Timeout         time.Duration    `yaml:"timeout"`            // default 5s
	FailOpen        bool             `yaml:"fail_open"`          // allow on error (default false = fail closed)
	HeadersToSend   []string         `yaml:"headers_to_send"`    // request headers to forward (empty = all)
	HeadersToInject []string         `yaml:"headers_to_inject"`  // auth response headers to copy to upstream (empty = all from auth response)
	CacheTTL        time.Duration    `yaml:"cache_ttl"`          // cache successful auth results (0 = no cache)
	TLS             ExtAuthTLSConfig `yaml:"tls"`
}

// ExtAuthTLSConfig configures TLS for ext auth connections.
type ExtAuthTLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CAFile   string `yaml:"ca_file"`
	CertFile string `yaml:"cert_file"` // for mTLS
	KeyFile  string `yaml:"key_file"`  // for mTLS
}

// VersioningConfig defines API versioning settings per route.
type VersioningConfig struct {
	Enabled        bool                            `yaml:"enabled"`
	Source         string                          `yaml:"source"`          // "path", "header", "accept", "query"
	HeaderName     string                          `yaml:"header_name"`     // default "X-API-Version"
	QueryParam     string                          `yaml:"query_param"`     // default "version"
	PathPrefix     string                          `yaml:"path_prefix"`     // default "/v"
	StripPrefix    bool                            `yaml:"strip_prefix"`    // strip /vN from forwarded path
	DefaultVersion string                          `yaml:"default_version"`
	Versions       map[string]VersionBackendConfig `yaml:"versions"`
}

// VersionBackendConfig defines backends and metadata for a specific API version.
type VersionBackendConfig struct {
	Backends   []BackendConfig `yaml:"backends"`
	Upstream   string          `yaml:"upstream"`   // reference to named upstream (alternative to inline backends)
	Deprecated bool            `yaml:"deprecated"` // adds Deprecation: true header
	Sunset     string          `yaml:"sunset"`     // adds Sunset header (YYYY-MM-DD)
}

// AccessLogConfig defines per-route access log settings.
type AccessLogConfig struct {
	Enabled          *bool                `yaml:"enabled"`           // nil=inherit global, false=disable
	Format           string               `yaml:"format"`            // override global format
	HeadersInclude   []string             `yaml:"headers_include"`   // headers to log
	HeadersExclude   []string             `yaml:"headers_exclude"`   // headers to exclude
	SensitiveHeaders []string             `yaml:"sensitive_headers"` // headers to mask
	Body             AccessLogBodyConfig  `yaml:"body"`
	Conditions       AccessLogConditions  `yaml:"conditions"`
}

// AccessLogBodyConfig defines body capture settings for access logging.
type AccessLogBodyConfig struct {
	Enabled      bool     `yaml:"enabled"`
	MaxSize      int      `yaml:"max_size"`       // default 4096
	ContentTypes []string `yaml:"content_types"`  // e.g. ["application/json"]
	Request      bool     `yaml:"request"`        // capture request body
	Response     bool     `yaml:"response"`       // capture response body
}

// AccessLogConditions defines conditions for when to emit access logs.
type AccessLogConditions struct {
	StatusCodes []string `yaml:"status_codes"` // "4xx", "5xx", "200", "200-299"
	Methods     []string `yaml:"methods"`      // "POST", "DELETE"
	SampleRate  float64  `yaml:"sample_rate"`  // 0.0-1.0 (0 = log all)
}

// OpenAPIConfig defines top-level OpenAPI settings for spec-based validation and route generation.
type OpenAPIConfig struct {
	Specs           []OpenAPISpecConfig   `yaml:"specs"`
	SchemaEvolution SchemaEvolutionConfig `yaml:"schema_evolution"`
}

// SchemaEvolutionConfig defines schema compatibility checking during config reload.
type SchemaEvolutionConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Mode        string `yaml:"mode"`         // "warn" (log) or "block" (reject reload)
	StoreDir    string `yaml:"store_dir"`    // directory for spec version history
	MaxVersions int    `yaml:"max_versions"` // max stored versions per spec (default 10)
}

// OpenAPISpecConfig defines a single OpenAPI spec for route generation.
type OpenAPISpecConfig struct {
	ID              string                   `yaml:"id"`
	File            string                   `yaml:"file"`
	DefaultBackends []BackendConfig          `yaml:"default_backends"`
	RoutePrefix     string                   `yaml:"route_prefix"`
	StripPrefix     bool                     `yaml:"strip_prefix"`
	Validation      OpenAPIValidationOptions `yaml:"validation"`
}

// OpenAPIValidationOptions defines validation settings for an OpenAPI spec.
type OpenAPIValidationOptions struct {
	Request  *bool `yaml:"request"`  // default true
	Response bool  `yaml:"response"` // default false
	LogOnly  bool  `yaml:"log_only"` // default false
}

// OpenAPIRouteConfig defines per-route OpenAPI validation settings.
type OpenAPIRouteConfig struct {
	SpecFile         string `yaml:"spec_file"`
	SpecID           string `yaml:"spec_id"`           // reference to top-level spec by ID
	OperationID      string `yaml:"operation_id"`      // specific operation
	ValidateRequest  *bool  `yaml:"validate_request"`  // default true
	ValidateResponse bool   `yaml:"validate_response"` // default false
	LogOnly          bool   `yaml:"log_only"`          // default false
}

// BodyTransformConfig defines request/response body transformation settings (Feature 13)
type BodyTransformConfig struct {
	AddFields    map[string]string `yaml:"add_fields"`
	RemoveFields []string          `yaml:"remove_fields"`
	RenameFields map[string]string `yaml:"rename_fields"`
	SetFields    map[string]string `yaml:"set_fields"`
	AllowFields  []string          `yaml:"allow_fields"`
	DenyFields   []string          `yaml:"deny_fields"`
	Template     string            `yaml:"template"`
	Target       string            `yaml:"target"`  // gjson path to extract as root response
	Flatmap      []FlatmapOperation `yaml:"flatmap"` // array manipulation operations
	Group        string            `yaml:"group"`   // wrap entire result under this JSON key
}

// FlatmapOperation defines a single flatmap array manipulation.
type FlatmapOperation struct {
	Type string   `yaml:"type"` // "move", "del", "extract", "flatten", "append"
	Args []string `yaml:"args"` // operation-specific arguments
}

// IsActive returns true if any body transform operation is configured.
func (c BodyTransformConfig) IsActive() bool {
	return len(c.AddFields) > 0 || len(c.RemoveFields) > 0 || len(c.RenameFields) > 0 ||
		len(c.SetFields) > 0 || len(c.AllowFields) > 0 || len(c.DenyFields) > 0 ||
		c.Template != "" || c.Target != "" || len(c.Flatmap) > 0
}

// MatchConfig defines route match criteria for domain/header/query/cookie matching
type MatchConfig struct {
	Domains          []string             `yaml:"domains"`
	Headers          []HeaderMatchConfig  `yaml:"headers"`
	Query            []QueryMatchConfig   `yaml:"query"`
	Cookies          []CookieMatchConfig  `yaml:"cookies"`
	Body             []BodyMatchConfig    `yaml:"body"`
	MaxMatchBodySize int64                `yaml:"max_match_body_size"`
}

// HeaderMatchConfig defines a single header match criterion
type HeaderMatchConfig struct {
	Name    string `yaml:"name"`
	Value   string `yaml:"value"`
	Present *bool  `yaml:"present"`
	Regex   string `yaml:"regex"`
}

// QueryMatchConfig defines a single query parameter match criterion
type QueryMatchConfig struct {
	Name    string `yaml:"name"`
	Value   string `yaml:"value"`
	Present *bool  `yaml:"present"`
	Regex   string `yaml:"regex"`
}

// CookieMatchConfig defines a single cookie match criterion
type CookieMatchConfig struct {
	Name    string `yaml:"name"`
	Value   string `yaml:"value"`
	Present *bool  `yaml:"present"`
	Regex   string `yaml:"regex"`
}

// BodyMatchConfig defines a single request body field match criterion using gjson paths.
type BodyMatchConfig struct {
	Name    string `yaml:"name"`    // gjson path (required)
	Value   string `yaml:"value"`   // exact match
	Present *bool  `yaml:"present"` // field existence check
	Regex   string `yaml:"regex"`   // regex match on string value
}

// BackendConfig defines a static backend
type BackendConfig struct {
	URL         string             `yaml:"url"`
	Weight      int                `yaml:"weight"`
	HealthCheck *HealthCheckConfig `yaml:"health_check"` // nil = inherit global
}

// ServiceConfig defines service discovery settings for a route
type ServiceConfig struct {
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`
}

// RouteAuthConfig defines authentication for a route
type RouteAuthConfig struct {
	Required bool     `yaml:"required"`
	Methods  []string `yaml:"methods"` // jwt, api_key, oauth
}

// RateLimitConfig defines rate limiting settings
type RateLimitConfig struct {
	Enabled     bool                    `yaml:"enabled"`
	Rate        int                     `yaml:"rate"`
	Period      time.Duration           `yaml:"period"`
	Burst       int                     `yaml:"burst"`
	PerIP       bool                    `yaml:"per_ip"`
	Key         string                  `yaml:"key"`          // Custom key extraction: "ip", "client_id", "header:<name>", "cookie:<name>", "jwt_claim:<name>"
	Mode        string                  `yaml:"mode"`         // "local" (default) or "distributed"
	Algorithm   string                  `yaml:"algorithm"`    // "token_bucket" (default) or "sliding_window"
	Tiers       map[string]TierConfig   `yaml:"tiers"`        // per-tier rate limits
	TierKey     string                  `yaml:"tier_key"`     // "header:<name>" or "jwt_claim:<name>"
	DefaultTier string                  `yaml:"default_tier"` // fallback tier name
}

// TierConfig defines rate limits for a single tier.
type TierConfig struct {
	Rate   int           `yaml:"rate"`
	Period time.Duration `yaml:"period"`
	Burst  int           `yaml:"burst"`
}

// RedisConfig defines Redis connection settings for distributed features.
type RedisConfig struct {
	Address     string        `yaml:"address"`
	Password    string        `yaml:"password"`
	DB          int           `yaml:"db"`
	TLS         bool          `yaml:"tls"`
	PoolSize    int           `yaml:"pool_size"`
	DialTimeout time.Duration `yaml:"dial_timeout"`
}

// DNSResolverConfig defines custom DNS resolver settings for backend connections.
type DNSResolverConfig struct {
	Nameservers []string      `yaml:"nameservers"` // e.g. "10.0.0.53:53"
	Timeout     time.Duration `yaml:"timeout"`     // per-query timeout
}

// TransformConfig defines request/response transformations
type TransformConfig struct {
	Request  RequestTransform  `yaml:"request"`
	Response ResponseTransform `yaml:"response"`
}

// RequestTransform defines request transformations
type RequestTransform struct {
	Headers HeaderTransform     `yaml:"headers"`
	Body    BodyTransformConfig `yaml:"body"` // Feature 13
}

// ResponseTransform defines response transformations
type ResponseTransform struct {
	Headers HeaderTransform     `yaml:"headers"`
	Body    BodyTransformConfig `yaml:"body"` // Feature 13
}

// HeaderTransform defines header transformations
type HeaderTransform struct {
	Add    map[string]string `yaml:"add"`
	Set    map[string]string `yaml:"set"`
	Remove []string          `yaml:"remove"`
}

// LoggingConfig defines logging settings
type LoggingConfig struct {
	Format   string            `yaml:"format"`
	Level    string            `yaml:"level"`
	Output   string            `yaml:"output"`
	Rotation LogRotationConfig `yaml:"rotation"`
}

// LogRotationConfig defines log file rotation settings (powered by lumberjack).
type LogRotationConfig struct {
	MaxSize    int  `yaml:"max_size"`    // max megabytes before rotation (default 100)
	MaxBackups int  `yaml:"max_backups"` // old rotated files to keep (default 3)
	MaxAge     int  `yaml:"max_age"`     // days to retain old files (default 28)
	Compress   bool `yaml:"compress"`    // gzip rotated files (default true)
	LocalTime  bool `yaml:"local_time"`  // use local time in backup filenames (default false)
}

// AdminConfig defines admin API settings
type AdminConfig struct {
	Enabled    bool             `yaml:"enabled"`
	Port       int              `yaml:"port"`
	Pprof      bool             `yaml:"pprof"`      // Enable /debug/pprof/* endpoints
	Metrics    MetricsConfig    `yaml:"metrics"`     // Feature 5: Prometheus metrics
	Readiness  ReadinessConfig  `yaml:"readiness"`   // Readiness probe configuration
	Catalog    CatalogConfig    `yaml:"catalog"`     // Developer portal / API catalog
	GRPCHealth GRPCHealthConfig `yaml:"grpc_health"` // gRPC health check server
}

// GRPCHealthConfig defines gRPC health check server settings.
type GRPCHealthConfig struct {
	Enabled bool   `yaml:"enabled"`
	Address string `yaml:"address"` // default ":9090"
}

// CatalogConfig defines developer portal / API catalog settings.
type CatalogConfig struct {
	Enabled     bool      `yaml:"enabled"`
	Title       string    `yaml:"title"`       // Portal title (default "API Gateway")
	Description string    `yaml:"description"` // Portal description
}

// ReadinessConfig defines readiness probe settings.
type ReadinessConfig struct {
	MinHealthyBackends int  `yaml:"min_healthy_backends"` // default 1
	RequireRedis       bool `yaml:"require_redis"`
}

// RulesConfig defines request and response phase rules.
type RulesConfig struct {
	Request  []RuleConfig `yaml:"request"`
	Response []RuleConfig `yaml:"response"`
}

// RuleConfig defines a single rule.
type RuleConfig struct {
	ID          string               `yaml:"id"`
	Enabled     *bool                `yaml:"enabled"`       // default true
	Expression  string               `yaml:"expression"`
	Action      string               `yaml:"action"`        // block, custom_response, redirect, set_headers, rewrite, group, log, delay, set_var, set_status, set_body, cache_bypass, lua
	StatusCode  int                  `yaml:"status_code"`
	Body        string               `yaml:"body"`
	RedirectURL string               `yaml:"redirect_url"`
	Headers     HeaderTransform      `yaml:"headers"`
	Description string               `yaml:"description"`
	Rewrite     *RewriteActionConfig `yaml:"rewrite"`
	Group       string               `yaml:"group"`       // traffic split group name
	LogMessage  string               `yaml:"log_message"` // optional custom log message
	LuaScript   string               `yaml:"lua_script"`  // inline Lua for lua action
	Delay       time.Duration        `yaml:"delay"`        // delay duration for delay action
	Variables   map[string]string    `yaml:"variables"`   // key-value pairs for set_var action
}

// RewriteActionConfig defines path/query/header rewriting for the rewrite action.
type RewriteActionConfig struct {
	Path    string          `yaml:"path"`
	Query   string          `yaml:"query"`
	Headers HeaderTransform `yaml:"headers"`
}

// TrafficShapingConfig defines traffic shaping settings.
type TrafficShapingConfig struct {
	Throttle             ThrottleConfig             `yaml:"throttle"`
	Bandwidth            BandwidthConfig            `yaml:"bandwidth"`
	Priority             PriorityConfig             `yaml:"priority"`
	FaultInjection       FaultInjectionConfig       `yaml:"fault_injection"`
	AdaptiveConcurrency  AdaptiveConcurrencyConfig  `yaml:"adaptive_concurrency"`
	RequestQueue         RequestQueueConfig         `yaml:"request_queue"`
}

// RequestQueueConfig defines bounded request queuing settings.
type RequestQueueConfig struct {
	Enabled  bool          `yaml:"enabled"`
	MaxDepth int           `yaml:"max_depth"` // max queued requests (default 100)
	MaxWait  time.Duration `yaml:"max_wait"`  // max queue wait time (default 30s)
}

// AdaptiveConcurrencyConfig defines adaptive concurrency limiting settings.
type AdaptiveConcurrencyConfig struct {
	Enabled            bool          `yaml:"enabled"`
	MinConcurrency     int           `yaml:"min_concurrency"`
	MaxConcurrency     int           `yaml:"max_concurrency"`
	LatencyTolerance   float64       `yaml:"latency_tolerance"`
	AdjustmentInterval time.Duration `yaml:"adjustment_interval"`
	SmoothingFactor    float64       `yaml:"smoothing_factor"`
	MinLatencySamples  int           `yaml:"min_latency_samples"`
}

// ThrottleConfig defines request throttling settings.
type ThrottleConfig struct {
	Enabled bool          `yaml:"enabled"`
	Rate    int           `yaml:"rate"`      // requests per second
	Burst   int           `yaml:"burst"`     // token bucket capacity
	MaxWait time.Duration `yaml:"max_wait"`  // max queue time (default 30s)
	PerIP   bool          `yaml:"per_ip"`
}

// BandwidthConfig defines bandwidth limiting settings.
type BandwidthConfig struct {
	Enabled       bool  `yaml:"enabled"`
	RequestRate   int64 `yaml:"request_rate"`    // bytes/sec (0 = unlimited)
	ResponseRate  int64 `yaml:"response_rate"`   // bytes/sec (0 = unlimited)
	RequestBurst  int64 `yaml:"request_burst"`   // default = request_rate
	ResponseBurst int64 `yaml:"response_burst"`  // default = response_rate
}

// PriorityConfig defines priority-based admission control settings.
type PriorityConfig struct {
	Enabled       bool                  `yaml:"enabled"`
	MaxConcurrent int                   `yaml:"max_concurrent"`
	MaxWait       time.Duration         `yaml:"max_wait"`
	DefaultLevel  int                   `yaml:"default_level"` // 1=highest, 10=lowest, default 5
	Levels        []PriorityLevelConfig `yaml:"levels"`
}

// PriorityLevelConfig defines a priority level matching rule.
type PriorityLevelConfig struct {
	Level     int               `yaml:"level"`
	Headers   map[string]string `yaml:"headers"`    // match if all headers present with value
	ClientIDs []string          `yaml:"client_ids"` // match if auth client_id in list
}

// FaultInjectionConfig defines fault injection settings for chaos testing.
type FaultInjectionConfig struct {
	Enabled bool             `yaml:"enabled"`
	Delay   FaultDelayConfig `yaml:"delay"`
	Abort   FaultAbortConfig `yaml:"abort"`
}

// FaultDelayConfig defines delay injection settings.
type FaultDelayConfig struct {
	Percentage int           `yaml:"percentage"` // 0-100
	Duration   time.Duration `yaml:"duration"`
}

// FaultAbortConfig defines abort injection settings.
type FaultAbortConfig struct {
	Percentage int `yaml:"percentage"`  // 0-100
	StatusCode int `yaml:"status_code"` // HTTP status to return
}

// WebhooksConfig defines event webhook notification settings.
type WebhooksConfig struct {
	Enabled   bool              `yaml:"enabled"`
	Endpoints []WebhookEndpoint `yaml:"endpoints"`
	Retry     WebhookRetryConfig `yaml:"retry"`
	Timeout   time.Duration     `yaml:"timeout"`
	Workers   int               `yaml:"workers"`
	QueueSize int               `yaml:"queue_size"`
}

// WebhookEndpoint defines a single webhook receiver.
type WebhookEndpoint struct {
	ID      string            `yaml:"id"`
	URL     string            `yaml:"url"`
	Secret  string            `yaml:"secret"`
	Events  []string          `yaml:"events"`
	Headers map[string]string `yaml:"headers"`
	Routes  []string          `yaml:"routes"`
}

// WebhookRetryConfig defines retry settings for webhook delivery.
type WebhookRetryConfig struct {
	MaxRetries int           `yaml:"max_retries"`
	Backoff    time.Duration `yaml:"backoff"`
	MaxBackoff time.Duration `yaml:"max_backoff"`
}

// HealthCheckConfig defines backend health check settings.
type HealthCheckConfig struct {
	Path           string        `yaml:"path"`             // default "/health"
	Method         string        `yaml:"method"`           // default "GET"
	Interval       time.Duration `yaml:"interval"`         // default 10s
	Timeout        time.Duration `yaml:"timeout"`          // default 5s
	HealthyAfter   int           `yaml:"healthy_after"`    // default 2
	UnhealthyAfter int           `yaml:"unhealthy_after"`  // default 3
	ExpectedStatus []string      `yaml:"expected_status"`  // e.g. ["200", "2xx", "200-299"]; default 200-399
}

// ErrorPagesConfig defines custom error page settings.
type ErrorPagesConfig struct {
	Enabled bool                      `yaml:"enabled"`
	Pages   map[string]ErrorPageEntry `yaml:"pages"` // keys: "404", "4xx", "5xx", "default"
}

// IsActive returns true if error pages are configured and enabled.
func (c ErrorPagesConfig) IsActive() bool {
	return c.Enabled && len(c.Pages) > 0
}

// ErrorPageEntry defines templates for a single error page.
type ErrorPageEntry struct {
	HTML     string `yaml:"html"`
	HTMLFile string `yaml:"html_file"`
	JSON     string `yaml:"json"`
	JSONFile string `yaml:"json_file"`
	XML      string `yaml:"xml"`
	XMLFile  string `yaml:"xml_file"`
}

// NonceConfig defines replay prevention nonce settings.
type NonceConfig struct {
	Enabled         bool          `yaml:"enabled"`
	Header          string        `yaml:"header"`           // default "X-Nonce"
	QueryParam      string        `yaml:"query_param"`      // optional query parameter name (e.g. "nonce")
	TTL             time.Duration `yaml:"ttl"`              // default 5m
	Mode            string        `yaml:"mode"`             // "local" (default) | "distributed"
	Scope           string        `yaml:"scope"`            // "global" (default) | "per_client"
	Required        bool          `yaml:"required"`         // default true — reject if header missing
	TimestampHeader string        `yaml:"timestamp_header"` // optional, e.g. "X-Timestamp"
	MaxAge          time.Duration `yaml:"max_age"`          // max request age (requires timestamp_header)
}

// CSRFConfig defines CSRF protection settings using double-submit cookie pattern.
type CSRFConfig struct {
	Enabled               bool          `yaml:"enabled"`
	CookieName            string        `yaml:"cookie_name"`             // default "_csrf"
	HeaderName            string        `yaml:"header_name"`             // default "X-CSRF-Token"
	Secret                string        `yaml:"secret"`                  // HMAC key (required when enabled)
	TokenTTL              time.Duration `yaml:"token_ttl"`               // default 1h
	SafeMethods           []string      `yaml:"safe_methods"`            // default GET,HEAD,OPTIONS,TRACE
	AllowedOrigins        []string      `yaml:"allowed_origins"`         // exact origin matches
	AllowedOriginPatterns []string      `yaml:"allowed_origin_patterns"` // regex patterns
	CookiePath            string        `yaml:"cookie_path"`             // default "/"
	CookieDomain          string        `yaml:"cookie_domain"`
	CookieSecure          bool          `yaml:"cookie_secure"`           // default true (set explicitly in YAML)
	CookieSameSite        string        `yaml:"cookie_samesite"`         // strict/lax/none, default "lax"
	CookieHTTPOnly        bool          `yaml:"cookie_http_only"`        // default false (JS must read cookie)
	InjectToken           bool          `yaml:"inject_token"`            // default true (set explicitly in YAML)
	ShadowMode            bool          `yaml:"shadow_mode"`             // log but don't reject
	ExemptPaths           []string      `yaml:"exempt_paths"`            // glob patterns
}

// IdempotencyConfig defines idempotency key support for mutation requests.
type IdempotencyConfig struct {
	Enabled      bool          `yaml:"enabled"`
	HeaderName   string        `yaml:"header_name"`    // default "Idempotency-Key"
	TTL          time.Duration `yaml:"ttl"`            // default 24h
	Methods      []string      `yaml:"methods"`        // default ["POST","PUT","PATCH"]
	Enforce      bool          `yaml:"enforce"`        // reject mutations without key (422)
	KeyScope     string        `yaml:"key_scope"`      // "global" (default) or "per_client"
	Mode         string        `yaml:"mode"`           // "local" (default) or "distributed"
	MaxKeyLength int           `yaml:"max_key_length"` // default 256
	MaxBodySize  int64         `yaml:"max_body_size"`  // max response body to store, default 1MB
}

// OutlierDetectionConfig defines passive per-backend outlier detection settings.
type OutlierDetectionConfig struct {
	Enabled              bool          `yaml:"enabled"`
	Interval             time.Duration `yaml:"interval"`               // default 10s
	Window               time.Duration `yaml:"window"`                 // default 30s
	MinRequests          int           `yaml:"min_requests"`           // default 10
	ErrorRateThreshold   float64       `yaml:"error_rate_threshold"`   // 0.0-1.0, default 0.5
	ErrorRateMultiplier  float64       `yaml:"error_rate_multiplier"`  // vs median, default 2.0
	LatencyMultiplier    float64       `yaml:"latency_multiplier"`     // p99 vs median p99, default 3.0
	BaseEjectionDuration time.Duration `yaml:"base_ejection_duration"` // default 30s
	MaxEjectionDuration  time.Duration `yaml:"max_ejection_duration"`  // default 5m
	MaxEjectionPercent   float64       `yaml:"max_ejection_percent"`   // 0-100, default 50
}

// GeoConfig defines geolocation filtering settings.
type GeoConfig struct {
	Enabled        bool     `yaml:"enabled"`
	Database       string   `yaml:"database"`        // global only: path to .mmdb or .ipdb
	InjectHeaders  bool     `yaml:"inject_headers"`  // inject X-Geo-Country / X-Geo-City headers
	AllowCountries []string `yaml:"allow_countries"` // ISO 3166-1 alpha-2
	DenyCountries  []string `yaml:"deny_countries"`
	AllowCities    []string `yaml:"allow_cities"`
	DenyCities     []string `yaml:"deny_cities"`
	Order          string   `yaml:"order"`           // "allow_first" or "deny_first" (default)
	ShadowMode     bool     `yaml:"shadow_mode"`     // log but don't reject
}

// BackendSigningConfig defines request signing for backend verification.
type BackendSigningConfig struct {
	Enabled        bool     `yaml:"enabled"`
	Algorithm      string   `yaml:"algorithm"`        // "hmac-sha256" (default), "hmac-sha512", "rsa-sha256", "rsa-sha512", "rsa-pss-sha256"
	Secret         string   `yaml:"secret"`            // base64-encoded HMAC secret, min 32 decoded bytes
	KeyID          string   `yaml:"key_id"`            // key identifier for rotation
	SignedHeaders  []string `yaml:"signed_headers"`    // headers to include in signature
	IncludeBody    *bool    `yaml:"include_body"`      // default true (*bool for merge semantics)
	HeaderPrefix   string   `yaml:"header_prefix"`     // default "X-Gateway-"
	PrivateKey     string   `yaml:"private_key"`       // PEM-encoded RSA private key (for RSA algos)
	PrivateKeyFile string   `yaml:"private_key_file"`  // path to PEM-encoded RSA private key file
}

// ResponseSigningConfig defines response body signing.
type ResponseSigningConfig struct {
	Enabled        bool     `yaml:"enabled"`
	Algorithm      string   `yaml:"algorithm"`        // "hmac-sha256" (default), "hmac-sha512", "rsa-sha256"
	Secret         string   `yaml:"secret"`            // base64-encoded HMAC secret, min 32 decoded bytes
	KeyFile        string   `yaml:"key_file"`          // path to PEM-encoded RSA private key file
	KeyID          string   `yaml:"key_id"`            // key identifier included in signature header
	Header         string   `yaml:"header"`            // response header name (default "X-Response-Signature")
	IncludeHeaders []string `yaml:"include_headers"`   // response headers to include in signature
}

// InboundSigningConfig defines inbound request signature verification.
type InboundSigningConfig struct {
	Enabled       bool          `yaml:"enabled"`
	Algorithm     string        `yaml:"algorithm"`       // "hmac-sha256" (default), "hmac-sha512", "rsa-sha256", "rsa-sha512", "rsa-pss-sha256"
	Secret        string        `yaml:"secret"`          // base64-encoded HMAC secret, min 32 decoded bytes
	KeyID         string        `yaml:"key_id"`          // expected key identifier
	SignedHeaders []string      `yaml:"signed_headers"`  // headers to include in verification
	IncludeBody   *bool         `yaml:"include_body"`    // default true
	HeaderPrefix  string        `yaml:"header_prefix"`   // default "X-Gateway-"
	MaxAge        time.Duration `yaml:"max_age"`         // max age of timestamp (default 5m)
	ShadowMode    bool          `yaml:"shadow_mode"`     // log but don't reject
	PublicKey     string        `yaml:"public_key"`      // PEM-encoded RSA public key (for RSA algos)
	PublicKeyFile string        `yaml:"public_key_file"` // path to PEM-encoded RSA public key file
}

// PIIRedactionConfig defines PII pattern redaction settings.
type PIIRedactionConfig struct {
	Enabled  bool        `yaml:"enabled"`
	BuiltIns []string    `yaml:"built_ins"`  // email, credit_card, ssn, phone
	Custom   []PIIPattern `yaml:"custom"`    // custom regex patterns
	Scope    string      `yaml:"scope"`      // "response" (default), "request", "both"
	MaskChar string      `yaml:"mask_char"`  // default "*"
	Headers  []string    `yaml:"headers"`    // headers to redact
}

// PIIPattern defines a custom PII pattern.
type PIIPattern struct {
	Name        string `yaml:"name"`
	Pattern     string `yaml:"pattern"`
	Replacement string `yaml:"replacement"` // optional override
}

// FieldEncryptionConfig defines payload field-level encryption settings.
type FieldEncryptionConfig struct {
	Enabled       bool     `yaml:"enabled"`
	Algorithm     string   `yaml:"algorithm"`      // "aes-gcm-256" only
	KeyBase64     string   `yaml:"key_base64"`     // base64-encoded 32-byte key
	EncryptFields []string `yaml:"encrypt_fields"` // gjson paths to encrypt in request
	DecryptFields []string `yaml:"decrypt_fields"` // gjson paths to decrypt in response
	Encoding      string   `yaml:"encoding"`       // "base64" (default), "hex"
}

// BlueGreenConfig defines blue-green deployment settings.
type BlueGreenConfig struct {
	Enabled           bool          `yaml:"enabled"`
	ActiveGroup       string        `yaml:"active_group"`
	InactiveGroup     string        `yaml:"inactive_group"`
	HealthGate        HealthGate    `yaml:"health_gate"`
	AutoPromoteDelay  time.Duration `yaml:"auto_promote_delay"`
	RollbackOnError   bool          `yaml:"rollback_on_error"`
	ErrorThreshold    float64       `yaml:"error_threshold"`
	ObservationWindow time.Duration `yaml:"observation_window"`
	MinRequests       int           `yaml:"min_requests"`
}

// HealthGate defines health checking requirements for blue-green promotion.
type HealthGate struct {
	MinHealthy int           `yaml:"min_healthy"`
	Timeout    time.Duration `yaml:"timeout"`
}

// ABTestConfig defines A/B testing metric collection settings.
type ABTestConfig struct {
	Enabled        bool   `yaml:"enabled"`
	ExperimentName string `yaml:"experiment_name"`
}

// TransportConfig defines upstream HTTP transport (connection pool) settings.
type TransportConfig struct {
	MaxIdleConns          int           `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost   int           `yaml:"max_idle_conns_per_host"`
	MaxConnsPerHost       int           `yaml:"max_conns_per_host"`
	IdleConnTimeout       time.Duration `yaml:"idle_conn_timeout"`
	DialTimeout           time.Duration `yaml:"dial_timeout"`
	TLSHandshakeTimeout   time.Duration `yaml:"tls_handshake_timeout"`
	ResponseHeaderTimeout time.Duration `yaml:"response_header_timeout"`
	ExpectContinueTimeout time.Duration `yaml:"expect_continue_timeout"`
	DisableKeepAlives     bool          `yaml:"disable_keep_alives"`
	InsecureSkipVerify    bool          `yaml:"insecure_skip_verify"`
	CAFile                string        `yaml:"ca_file"`
	CertFile              string        `yaml:"cert_file"`
	KeyFile               string        `yaml:"key_file"`
	ForceHTTP2            *bool         `yaml:"force_http2"`
	EnableHTTP3           *bool         `yaml:"enable_http3"` // connect via QUIC to upstream
}

// ServiceRateLimitConfig defines global gateway-wide throughput cap.
type ServiceRateLimitConfig struct {
	Enabled bool          `yaml:"enabled"`
	Rate    int           `yaml:"rate"`   // requests per period
	Period  time.Duration `yaml:"period"` // default 1s
	Burst   int           `yaml:"burst"`  // burst capacity (default = rate)
}

// SpikeArrestConfig defines continuous rate enforcement with immediate rejection.
type SpikeArrestConfig struct {
	Enabled bool          `yaml:"enabled"`
	Rate    int           `yaml:"rate"`   // max requests per period
	Period  time.Duration `yaml:"period"` // default 1s
	Burst   int           `yaml:"burst"`  // requests before arrest (default = rate)
	PerIP   bool          `yaml:"per_ip"`
}

// ContentReplacerConfig defines response content replacement rules.
type ContentReplacerConfig struct {
	Enabled      bool              `yaml:"enabled"`
	Replacements []ReplacementRule `yaml:"replacements"`
}

// ReplacementRule defines a single regex-based replacement.
type ReplacementRule struct {
	Pattern     string `yaml:"pattern"`     // regex pattern
	Replacement string `yaml:"replacement"` // replacement string ($1, $2 capture groups)
	Scope       string `yaml:"scope"`       // "body" (default) or "header:<name>"
}

// DebugEndpointConfig defines the debug endpoint settings.
type DebugEndpointConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"` // default "/__debug"
}

// FollowRedirectsConfig enables following backend 3xx redirects.
type FollowRedirectsConfig struct {
	Enabled      bool `yaml:"enabled"`
	MaxRedirects int  `yaml:"max_redirects"` // default 10
}

// BodyGeneratorConfig defines a Go template that generates request bodies.
type BodyGeneratorConfig struct {
	Enabled     bool              `yaml:"enabled"`
	Template    string            `yaml:"template"`      // Go text/template string
	ContentType string            `yaml:"content_type"`  // default "application/json"
	Variables   map[string]string `yaml:"variables"`     // custom static variables available in templates as .Variables
}

// SequentialConfig enables chaining multiple backend calls.
type SequentialConfig struct {
	Enabled bool             `yaml:"enabled"`
	Steps   []SequentialStep `yaml:"steps"`
}

// SequentialStep defines a single step in a sequential proxy chain.
type SequentialStep struct {
	URL          string            `yaml:"url"`            // Go template
	Method       string            `yaml:"method"`         // default: GET
	Headers      map[string]string `yaml:"headers"`        // Go template values
	BodyTemplate string            `yaml:"body_template"`  // Go template for request body
	Timeout      time.Duration     `yaml:"timeout"`        // per-step timeout (default 5s)
	Variables    map[string]string `yaml:"variables"`      // custom static variables available in templates as .Variables
	Encoding     string            `yaml:"encoding"`       // response encoding: "no-op" stores full metadata, "string" wraps as content
}

// QuotaConfig defines per-client usage quota enforcement.
type QuotaConfig struct {
	Enabled bool   `yaml:"enabled"`
	Limit   int64  `yaml:"limit"`   // max requests per period
	Period  string `yaml:"period"`  // "hourly", "daily", "monthly", "yearly"
	Key     string `yaml:"key"`     // "ip", "client_id", "header:<name>", "jwt_claim:<name>"
	Redis   bool   `yaml:"redis"`   // use Redis for distributed tracking
}

// RequestCostConfig defines request cost tracking settings.
type RequestCostConfig struct {
	Enabled      bool           `yaml:"enabled"`
	Cost         int            `yaml:"cost"`           // default cost per request (default 1)
	CostByMethod map[string]int `yaml:"cost_by_method"` // per-method cost overrides
	Key          string         `yaml:"key"`            // consumer key: "ip", "client_id", "header:<name>"
	Budget       *CostBudget    `yaml:"budget"`         // optional per-consumer budget enforcement
}

// CostBudget defines a cost budget for request cost tracking.
type CostBudget struct {
	Limit  int64  `yaml:"limit"`  // max cost per window
	Window string `yaml:"window"` // "hour", "day", "month", or Go duration
	Action string `yaml:"action"` // "reject" (default) or "log_only"
}

// TenantsConfig defines multi-tenancy settings.
type TenantsConfig struct {
	Enabled       bool                          `yaml:"enabled"`
	Key           string                        `yaml:"key"`            // "header:<name>", "jwt_claim:<name>", "client_id"
	DefaultTenant string                        `yaml:"default_tenant"` // fallback tenant ID (empty = reject unknown)
	Tiers         map[string]TenantTierConfig   `yaml:"tiers,omitempty"`
	Tenants       map[string]TenantConfig       `yaml:"tenants"`
}

// TenantTierConfig defines defaults for a plan/tier. Tenants referencing this tier
// inherit these values unless overridden with tenant-specific settings.
type TenantTierConfig struct {
	RateLimit       *TenantRateLimitConfig `yaml:"rate_limit,omitempty"`
	Quota           *TenantQuotaConfig     `yaml:"quota,omitempty"`
	MaxBodySize     int64                  `yaml:"max_body_size,omitempty"`
	Priority        int                    `yaml:"priority,omitempty"`
	Timeout         time.Duration          `yaml:"timeout,omitempty"`
	Metadata        map[string]string      `yaml:"metadata,omitempty"`
	ResponseHeaders map[string]string      `yaml:"response_headers,omitempty"`
}

// TenantConfig defines per-tenant resource governance.
type TenantConfig struct {
	RateLimit       *TenantRateLimitConfig `yaml:"rate_limit,omitempty"`
	Quota           *TenantQuotaConfig     `yaml:"quota,omitempty"`
	Routes          []string               `yaml:"routes,omitempty"`           // allowed route IDs (empty = all)
	MaxBodySize     int64                  `yaml:"max_body_size,omitempty"`
	Priority        int                    `yaml:"priority,omitempty"`
	Timeout         time.Duration          `yaml:"timeout,omitempty"`          // per-tenant request timeout
	Metadata        map[string]string      `yaml:"metadata,omitempty"`
	ResponseHeaders map[string]string      `yaml:"response_headers,omitempty"` // custom response headers per tenant
	Tier            string                 `yaml:"tier,omitempty"`             // tier/plan reference
}

// TenantRateLimitConfig defines per-tenant rate limiting.
type TenantRateLimitConfig struct {
	Rate   int           `yaml:"rate"`
	Period time.Duration `yaml:"period"`
	Burst  int           `yaml:"burst"`
}

// TenantQuotaConfig defines per-tenant usage quotas.
type TenantQuotaConfig struct {
	Limit  int64  `yaml:"limit"`
	Period string `yaml:"period"` // "hourly", "daily", "monthly", "yearly"
}

// RouteTenantConfig defines per-route tenant restrictions.
type RouteTenantConfig struct {
	Required bool     `yaml:"required"` // reject if no tenant resolved
	Allowed  []string `yaml:"allowed"`  // restrict to specific tenant IDs
}

// AggregateConfig enables parallel multi-backend calls with JSON response merging.
type AggregateConfig struct {
	Enabled           bool               `yaml:"enabled"`
	Timeout           time.Duration      `yaml:"timeout"`            // default 5s
	FailStrategy      string             `yaml:"fail_strategy"`      // "abort" (default) or "partial"
	Backends          []AggregateBackend `yaml:"backends"`
	ResponseTransform BodyTransformConfig `yaml:"response_transform"` // post-merge body transform (flatmap, allow/deny, etc.)
}

// AggregateBackend defines one backend in an aggregate call.
type AggregateBackend struct {
	Name      string            `yaml:"name"`      // unique name (required)
	URL       string            `yaml:"url"`       // Go template
	Method    string            `yaml:"method"`    // default GET
	Headers   map[string]string `yaml:"headers"`   // Go template values
	Group     string            `yaml:"group"`     // wrap response under this JSON key
	Required  bool              `yaml:"required"`  // abort if fails (relevant for partial)
	Timeout   time.Duration     `yaml:"timeout"`   // per-backend override
	Variables map[string]string `yaml:"variables"` // custom static variables available in templates as .Variables
	Encoding  string            `yaml:"encoding"`  // backend response encoding (xml, yaml, etc.) — decoded to JSON before merge
	Transform BodyTransformConfig `yaml:"transform"` // per-backend response transform (allow/deny/rename/set/remove fields)
}

// ResponseBodyGeneratorConfig defines a Go template that rewrites the entire response body.
type ResponseBodyGeneratorConfig struct {
	Enabled     bool              `yaml:"enabled"`
	Template    string            `yaml:"template"`      // Go text/template string
	ContentType string            `yaml:"content_type"`  // default "application/json"
	Variables   map[string]string `yaml:"variables"`     // custom static variables available in templates as .Variables
}

// ParamForwardingConfig defines zero-trust parameter forwarding control.
type ParamForwardingConfig struct {
	Enabled     bool     `yaml:"enabled"`
	Headers     []string `yaml:"headers"`       // allowed request headers (case-insensitive)
	QueryParams []string `yaml:"query_params"`  // allowed query parameter names
	Cookies     []string `yaml:"cookies"`       // allowed cookie names
}

// ContentNegotiationConfig defines content negotiation settings.
type ContentNegotiationConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Supported []string `yaml:"supported"` // "json", "xml", "yaml"
	Default   string   `yaml:"default"`   // default "json"
}

// CDNCacheConfig defines CDN cache header injection settings.
type CDNCacheConfig struct {
	Enabled              bool     `yaml:"enabled"`
	CacheControl         string   `yaml:"cache_control"`         // e.g. "public, max-age=3600, s-maxage=86400"
	Vary                 []string `yaml:"vary"`                  // e.g. ["Accept", "Accept-Encoding"]
	SurrogateControl     string   `yaml:"surrogate_control"`     // e.g. "max-age=86400"
	SurrogateKey         string   `yaml:"surrogate_key"`         // e.g. "product-listing"
	Expires              string   `yaml:"expires"`               // duration (e.g. "1h") or HTTP-date
	StaleWhileRevalidate int      `yaml:"stale_while_revalidate"` // seconds (appended to Cache-Control)
	StaleIfError         int      `yaml:"stale_if_error"`         // seconds (appended to Cache-Control)
	Override             *bool    `yaml:"override"`               // override backend Cache-Control (default true)
}

// EdgeCacheRulesConfig defines conditional cache-control rules based on response properties.
type EdgeCacheRulesConfig struct {
	Enabled bool            `yaml:"enabled"`
	Rules   []EdgeCacheRule `yaml:"rules"`
}

// EdgeCacheRule defines a single conditional cache-control rule.
type EdgeCacheRule struct {
	Match        EdgeCacheMatch `yaml:"match"`
	CacheControl string         `yaml:"cache_control"` // raw Cache-Control value (overrides other fields)
	SMaxAge      int            `yaml:"s_maxage"`       // shared cache TTL in seconds (CDN)
	MaxAge       int            `yaml:"max_age"`        // private cache TTL in seconds (browser)
	Vary         []string       `yaml:"vary"`
	NoStore      bool           `yaml:"no_store"`  // force Cache-Control: no-store
	Private      bool           `yaml:"private"`   // force Cache-Control: private
	Override     *bool          `yaml:"override"`  // override backend headers (default true)
}

// EdgeCacheMatch defines the conditions under which an edge cache rule applies.
type EdgeCacheMatch struct {
	StatusCodes  []int    `yaml:"status_codes"`  // e.g., [200, 301]
	ContentTypes []string `yaml:"content_types"` // e.g., ["text/html", "application/json"]
	PathPatterns []string `yaml:"path_patterns"` // glob patterns, e.g., ["/api/*", "/static/*"]
}

// BackendEncodingConfig defines backend response format decoding to JSON.
type BackendEncodingConfig struct {
	Encoding string `yaml:"encoding"` // "xml", "yaml", "safejson", "rss", "string", "fast-json" — backend response format to decode to JSON
}

// SSRFProtectionConfig defines SSRF protection for outbound proxy connections.
type SSRFProtectionConfig struct {
	Enabled        bool     `yaml:"enabled"`
	AllowCIDRs     []string `yaml:"allow_cidrs"`       // exempt specific private CIDRs
	BlockLinkLocal *bool    `yaml:"block_link_local"`   // default true
}

// RequestDedupConfig defines per-route request deduplication settings.
type RequestDedupConfig struct {
	Enabled        bool          `yaml:"enabled"`
	TTL            time.Duration `yaml:"ttl"`              // default 60s
	IncludeHeaders []string      `yaml:"include_headers"`
	IncludeBody    *bool         `yaml:"include_body"`     // default true
	MaxBodySize    int64         `yaml:"max_body_size"`    // default 1MB
	Mode           string        `yaml:"mode"`             // "local" or "distributed"
}

// IPBlocklistConfig defines dynamic IP blocklist settings.
type IPBlocklistConfig struct {
	Enabled bool              `yaml:"enabled"`
	Feeds   []IPBlocklistFeed `yaml:"feeds"`
	Static  []string          `yaml:"static"`   // always-blocked IPs/CIDRs
	Action  string            `yaml:"action"`   // "block" (default) or "log"
}

// IPBlocklistFeed defines a single IP blocklist feed source.
type IPBlocklistFeed struct {
	URL             string        `yaml:"url"`
	RefreshInterval time.Duration `yaml:"refresh_interval"` // default 5m
	Format          string        `yaml:"format"`           // "text" or "json"
}

// BaggageConfig defines baggage propagation settings for a route.
type BaggageConfig struct {
	Enabled        bool            `yaml:"enabled"`
	PropagateTrace bool            `yaml:"propagate_trace"` // inject traceparent/tracestate to backends
	W3CBaggage     bool            `yaml:"w3c_baggage"`     // emit W3C baggage header from tag values
	Tags           []BaggageTagDef `yaml:"tags"`
}

// BaggageTagDef defines a single baggage tag to extract and propagate.
type BaggageTagDef struct {
	Name       string `yaml:"name"`        // logical name for the tag (used in variable context)
	Source     string `yaml:"source"`      // extraction source: header:<name>, jwt_claim:<name>, query:<name>, cookie:<name>, static:<value>
	Header     string `yaml:"header"`      // backend header name to propagate as
	BaggageKey string `yaml:"baggage_key"` // W3C baggage key; defaults to Name
}

// BackpressureConfig defines backend backpressure detection settings.
type BackpressureConfig struct {
	Enabled       bool          `yaml:"enabled"`
	StatusCodes   []int         `yaml:"status_codes"`    // default [429, 503]
	MaxRetryAfter time.Duration `yaml:"max_retry_after"` // cap on Retry-After, default 60s
	DefaultDelay  time.Duration `yaml:"default_delay"`   // delay when no Retry-After header, default 5s
}

// LoadSheddingConfig defines system-level load shedding settings.
type LoadSheddingConfig struct {
	Enabled          bool          `yaml:"enabled"`
	CPUThreshold     float64       `yaml:"cpu_threshold"`     // CPU percent (0-100), default 90
	MemoryThreshold  float64       `yaml:"memory_threshold"`  // heap/sys percent (0-100), default 85
	GoroutineLimit   int           `yaml:"goroutine_limit"`   // max goroutines (0 = disabled)
	SampleInterval   time.Duration `yaml:"sample_interval"`   // default 1s
	CooldownDuration time.Duration `yaml:"cooldown_duration"` // stay in shedding mode for this long after thresholds drop, default 5s
	RetryAfter       int           `yaml:"retry_after"`       // Retry-After header value in seconds, default 5
}

// AuditLogConfig defines audit logging settings (global + per-route merge).
type AuditLogConfig struct {
	Enabled       bool              `yaml:"enabled"`
	WebhookURL    string            `yaml:"webhook_url"`
	Headers       map[string]string `yaml:"headers"`
	SampleRate    float64           `yaml:"sample_rate"`     // 0.0-1.0, default 1.0
	IncludeBody   bool              `yaml:"include_body"`
	MaxBodySize   int               `yaml:"max_body_size"`   // default 64KB
	BufferSize    int               `yaml:"buffer_size"`     // channel size, default 1000
	BatchSize     int               `yaml:"batch_size"`      // entries per webhook call, default 10
	FlushInterval time.Duration     `yaml:"flush_interval"`  // default 5s
	Methods       []string          `yaml:"methods"`         // filter (empty=all)
	StatusCodes   []int             `yaml:"status_codes"`    // filter (empty=all)
}

// DefaultConfig returns a configuration with sensible defaults
// ModifierConfig defines a single request/response modifier.
type ModifierConfig struct {
	Type      string            `yaml:"type"`       // "header_copy", "header_set", "cookie", "query", "stash", "port"
	From      string            `yaml:"from"`       // source header (header_copy)
	To        string            `yaml:"to"`         // destination header (header_copy)
	Name      string            `yaml:"name"`       // header/cookie name
	Value     string            `yaml:"value"`      // header/cookie value
	Domain    string            `yaml:"domain"`     // cookie domain
	Path      string            `yaml:"path"`       // cookie path
	MaxAge    int               `yaml:"max_age"`    // cookie max age
	Secure    bool              `yaml:"secure"`     // cookie secure flag
	HttpOnly  bool              `yaml:"http_only"`  // cookie httponly flag
	SameSite  string            `yaml:"same_site"`  // cookie SameSite (lax, strict, none)
	Params    map[string]string `yaml:"params"`     // query params to add/override
	Port      int               `yaml:"port"`       // port override
	Scope     string            `yaml:"scope"`      // "request", "response", "both" (default "both")
	Priority  int               `yaml:"priority"`   // execution priority (higher first, default 0)
	Condition *ConditionConfig  `yaml:"condition"`  // optional condition for conditional execution
	Else      *ModifierConfig   `yaml:"else"`       // modifier to apply when condition is false
}

// ConditionConfig defines a condition for conditional modifier execution.
type ConditionConfig struct {
	Type  string `yaml:"type"`  // "header", "cookie", "query", "path_regex"
	Name  string `yaml:"name"`  // header/cookie/query param name
	Value string `yaml:"value"` // optional regex pattern to match
}

// FieldReplacerConfig defines field-level content replacement on response bodies.
type FieldReplacerConfig struct {
	Enabled    bool                    `yaml:"enabled"`
	Operations []FieldReplacerOperation `yaml:"operations"`
}

// FieldReplacerOperation defines a single field replacement operation.
type FieldReplacerOperation struct {
	Field   string `yaml:"field"`   // gjson path to field
	Type    string `yaml:"type"`    // "regexp", "literal", "upper", "lower", "trim"
	Find    string `yaml:"find"`    // pattern to find (regexp/literal/trim chars)
	Replace string `yaml:"replace"` // replacement string (regexp/literal)
}

// JMESPathConfig defines JMESPath query filtering on response bodies.
type JMESPathConfig struct {
	Enabled         bool   `yaml:"enabled"`
	Expression      string `yaml:"expression"`
	WrapCollections bool   `yaml:"wrap_collections"` // wrap array results in {"collection": [...]}
}

// BackendResponseConfig controls backend response handling.
type BackendResponseConfig struct {
	IsCollection  bool   `yaml:"is_collection"`  // wrap array responses as object
	CollectionKey string `yaml:"collection_key"` // key to use (default "collection")
}

// ErrorHandlingConfig controls error response format.
type ErrorHandlingConfig struct {
	Mode string `yaml:"mode"` // "default", "pass_status", "detailed", "message"
}

// LuaConfig defines Lua scripting for a route.
type LuaConfig struct {
	Enabled        bool   `yaml:"enabled"`
	RequestScript  string `yaml:"request_script"`  // Lua code for request phase
	ResponseScript string `yaml:"response_script"` // Lua code for response phase
}

// WasmConfig defines global WASM plugin runtime settings.
type WasmConfig struct {
	RuntimeMode    string `yaml:"runtime_mode"`     // "compiler" (default, AOT) or "interpreter"
	MaxMemoryPages int    `yaml:"max_memory_pages"` // per-instance memory limit (pages × 64KB); default 256 = 16MB
}

// WasmPluginConfig defines a single WASM plugin for a route.
type WasmPluginConfig struct {
	Enabled  bool              `yaml:"enabled"`
	Name     string            `yaml:"name"`      // human-readable name for metrics/admin
	Path     string            `yaml:"path"`      // path to .wasm file
	Phase    string            `yaml:"phase"`     // "request", "response", or "both" (default)
	Config   map[string]string `yaml:"config"`    // arbitrary k/v passed to guest via host_get_property("config.key")
	Timeout  time.Duration     `yaml:"timeout"`   // per-invocation execution timeout
	PoolSize int               `yaml:"pool_size"` // pre-instantiated module pool size
}

// LambdaConfig defines AWS Lambda backend settings.
type LambdaConfig struct {
	Enabled      bool   `yaml:"enabled"`
	FunctionName string `yaml:"function_name"`
	Region       string `yaml:"region"`
	MaxRetries   int    `yaml:"max_retries"` // default 2
}

// AMQPConfig defines AMQP/RabbitMQ backend settings.
type AMQPConfig struct {
	Enabled  bool             `yaml:"enabled"`
	URL      string           `yaml:"url"`
	Consumer AMQPConsumerConfig `yaml:"consumer"`
	Producer AMQPProducerConfig `yaml:"producer"`
}

// AMQPConsumerConfig defines AMQP consumer settings.
type AMQPConsumerConfig struct {
	Queue   string `yaml:"queue"`
	AutoAck bool   `yaml:"auto_ack"`
}

// AMQPProducerConfig defines AMQP producer settings.
type AMQPProducerConfig struct {
	Exchange   string `yaml:"exchange"`
	RoutingKey string `yaml:"routing_key"`
}

// PubSubConfig defines Go CDK Pub/Sub backend settings.
type PubSubConfig struct {
	Enabled         bool   `yaml:"enabled"`
	SubscriptionURL string `yaml:"subscription_url"` // Go CDK subscription URL
	PublishURL      string `yaml:"publish_url"`       // Go CDK publish URL
}

// AggregateResponseTransformConfig allows post-merge body transforms on aggregated responses.
type AggregateResponseTransformConfig struct {
	BodyTransformConfig `yaml:",inline"`
}

// GraphQLProtocolConfig configures REST-to-GraphQL protocol translation.
type GraphQLProtocolConfig struct {
	URL           string            `yaml:"url" json:"url"`
	Type          string            `yaml:"type" json:"type"` // "query" or "mutation"
	Query         string            `yaml:"query" json:"query"`
	Variables     map[string]string `yaml:"variables" json:"variables"`
	OperationName string            `yaml:"operation_name" json:"operation_name"`
}

// SOAPProtocolConfig configures REST-to-SOAP protocol translation.
type SOAPProtocolConfig struct {
	URL         string `yaml:"url" json:"url"`
	Template    string `yaml:"template" json:"template"`
	ContentType string `yaml:"content_type" json:"content_type"`
}

// GRPCWebTranslateConfig configures gRPC-Web proxy translation.
// Unlike http_to_grpc, this passes protobuf bytes through unchanged,
// only transforming the framing layer (gRPC-Web wire format to native gRPC).
type GRPCWebTranslateConfig struct {
	Timeout        time.Duration     `yaml:"timeout" json:"timeout"`                 // per-call timeout (default 30s)
	MaxMessageSize int               `yaml:"max_message_size" json:"max_message_size"` // max message size in bytes (default 4MB)
	TextMode       bool              `yaml:"text_mode" json:"text_mode"`              // accept grpc-web-text base64 encoding (default true)
	TLS            ProtocolTLSConfig `yaml:"tls" json:"tls"`
}

func DefaultConfig() *Config {
	return &Config{
		Listeners: []ListenerConfig{{
			ID:       "default-http",
			Address:  ":8080",
			Protocol: ProtocolHTTP,
			HTTP: HTTPListenerConfig{
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 30 * time.Second,
				IdleTimeout:  60 * time.Second,
			},
		}},
		Registry: RegistryConfig{
			Type: "memory",
			Consul: ConsulConfig{
				Address:    "localhost:8500",
				Scheme:     "http",
				Datacenter: "dc1",
			},
			Etcd: EtcdConfig{
				Endpoints: []string{"localhost:2379"},
			},
			Kubernetes: KubernetesConfig{
				Namespace: "default",
				InCluster: true,
			},
			Memory: MemoryConfig{
				APIEnabled: true,
				APIPort:    8082,
			},
			DNSSRV: DNSSRVConfig{
				Protocol:     "tcp",
				PollInterval: 30 * time.Second,
			},
		},
		Authentication: AuthenticationConfig{
			APIKey: APIKeyConfig{
				Header: "X-API-Key",
			},
			JWT: JWTConfig{
				Algorithm: "HS256",
			},
		},
		Logging: LoggingConfig{
			Format: `$remote_addr - [$time_iso8601] "$request_method $request_uri" $status $body_bytes_sent "$http_user_agent" $response_time`,
			Level:  "info",
			Output: "stdout",
			Rotation: LogRotationConfig{
				MaxSize:    100,
				MaxBackups: 3,
				MaxAge:     28,
				Compress:   true,
			},
		},
		Admin: AdminConfig{
			Enabled: true,
			Port:    8081,
		},
	}
}
