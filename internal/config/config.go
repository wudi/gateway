package config

import (
	"net"
	"time"
)

// Protocol defines the listener protocol type
type Protocol string

const (
	ProtocolHTTP Protocol = "http"
	ProtocolTCP  Protocol = "tcp"
	ProtocolUDP  Protocol = "udp"
)

// Config represents the complete gateway configuration
type Config struct {
	Listeners      []ListenerConfig     `yaml:"listeners"`
	Registry       RegistryConfig       `yaml:"registry"`
	Authentication AuthenticationConfig `yaml:"authentication"`
	Routes         []RouteConfig        `yaml:"routes"`
	TCPRoutes      []TCPRouteConfig     `yaml:"tcp_routes"`      // TCP L4 routes
	UDPRoutes      []UDPRouteConfig     `yaml:"udp_routes"`      // UDP L4 routes
	Logging        LoggingConfig        `yaml:"logging"`
	Admin          AdminConfig          `yaml:"admin"`
	Tracing        TracingConfig        `yaml:"tracing"`         // Feature 9: Distributed tracing
	IPFilter       IPFilterConfig       `yaml:"ip_filter"`       // Feature 2: Global IP filter
	Rules          RulesConfig          `yaml:"rules"`           // Global rules engine
	TrafficShaping TrafficShapingConfig `yaml:"traffic_shaping"` // Global traffic shaping
	Redis          RedisConfig          `yaml:"redis"`           // Redis for distributed features
	WAF            WAFConfig            `yaml:"waf"`             // Global WAF settings
	DNSResolver    DNSResolverConfig    `yaml:"dns_resolver"`    // Custom DNS resolver for backends
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
	Type       string           `yaml:"type"` // consul, etcd, kubernetes, memory
	Consul     ConsulConfig     `yaml:"consul"`
	Etcd       EtcdConfig       `yaml:"etcd"`
	Kubernetes KubernetesConfig `yaml:"kubernetes"`
	Memory     MemoryConfig     `yaml:"memory"`
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
type TLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	CAFile     string `yaml:"ca_file"`
	ClientAuth string `yaml:"client_auth"` // Feature 11: mTLS - none, request, require, verify
	ClientCAFile string `yaml:"client_ca_file"` // Feature 11: mTLS
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

// APIKeyConfig defines API key authentication settings
type APIKeyConfig struct {
	Enabled    bool         `yaml:"enabled"`
	Header     string       `yaml:"header"`
	QueryParam string       `yaml:"query_param"`
	Keys       []APIKeyEntry `yaml:"keys"`
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
	GraphQL        GraphQLConfig        `yaml:"graphql"`         // GraphQL query analysis and protection
	Coalesce       CoalesceConfig       `yaml:"coalesce"`        // Request coalescing (singleflight)
}

// StickyConfig defines sticky session settings for consistent traffic group assignment.
type StickyConfig struct {
	Enabled    bool          `yaml:"enabled"`
	Mode       string        `yaml:"mode"`        // "cookie", "header", "hash"
	CookieName string        `yaml:"cookie_name"` // default "X-Traffic-Group"
	HashKey    string        `yaml:"hash_key"`    // header name for header/hash mode
	TTL        time.Duration `yaml:"ttl"`         // cookie TTL, default 24h
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
	Request time.Duration `yaml:"request"`
	Idle    time.Duration `yaml:"idle"`
}

// CircuitBreakerConfig defines circuit breaker settings
type CircuitBreakerConfig struct {
	Enabled          bool          `yaml:"enabled"`
	FailureThreshold int           `yaml:"failure_threshold"`
	MaxRequests      int           `yaml:"max_requests"`
	Timeout          time.Duration `yaml:"timeout"`
}

// CacheConfig defines request caching settings
type CacheConfig struct {
	Enabled     bool          `yaml:"enabled"`
	TTL         time.Duration `yaml:"ttl"`
	MaxSize     int           `yaml:"max_size"`
	MaxBodySize int64         `yaml:"max_body_size"`
	KeyHeaders  []string      `yaml:"key_headers"`
	Methods     []string      `yaml:"methods"`
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
	Level        int      `yaml:"level"`         // 1-9, default 6
	MinSize      int      `yaml:"min_size"`      // default 1024 bytes
	ContentTypes []string `yaml:"content_types"` // MIME types to compress
}

// MetricsConfig defines Prometheus metrics settings (Feature 5)
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"` // default "/metrics"
}

// TrafficSplitConfig defines canary/weighted traffic split settings (Feature 6)
type TrafficSplitConfig struct {
	Name         string          `yaml:"name"`
	Weight       int             `yaml:"weight"`        // percentage 0-100
	Backends     []BackendConfig `yaml:"backends"`
	MatchHeaders map[string]string `yaml:"match_headers"` // header-based override
}

// ValidationConfig defines request validation settings (Feature 8)
type ValidationConfig struct {
	Enabled    bool   `yaml:"enabled"`
	SchemaFile string `yaml:"schema_file"` // path to JSON schema file
	Schema     string `yaml:"schema"`      // inline JSON schema
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
	Enabled       bool `yaml:"enabled"`
	LogMismatches bool `yaml:"log_mismatches"`
}

// GRPCConfig defines gRPC proxying settings (Feature 12)
type GRPCConfig struct {
	Enabled bool `yaml:"enabled"`
}

// ProtocolConfig defines protocol translation settings per route.
type ProtocolConfig struct {
	Type string              `yaml:"type"` // "http_to_grpc"
	GRPC GRPCTranslateConfig `yaml:"grpc"`
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
	Enabled         bool           `yaml:"enabled"`
	MaxDepth        int            `yaml:"max_depth"`        // 0 = unlimited
	MaxComplexity   int            `yaml:"max_complexity"`   // 0 = unlimited
	Introspection   bool           `yaml:"introspection"`    // allow introspection (default false)
	OperationLimits map[string]int `yaml:"operation_limits"` // e.g. {"query": 100, "mutation": 10} req/s
}

// CoalesceConfig defines request coalescing (singleflight) settings.
type CoalesceConfig struct {
	Enabled    bool          `yaml:"enabled"`
	Timeout    time.Duration `yaml:"timeout"`      // max wait for coalesced requests (default 30s)
	KeyHeaders []string      `yaml:"key_headers"`  // headers included in coalesce key
	Methods    []string      `yaml:"methods"`      // eligible methods (default GET+HEAD)
}

// BodyTransformConfig defines request/response body transformation settings (Feature 13)
type BodyTransformConfig struct {
	AddFields    map[string]string `yaml:"add_fields"`
	RemoveFields []string          `yaml:"remove_fields"`
	RenameFields map[string]string `yaml:"rename_fields"`
}

// MatchConfig defines route match criteria for domain/header/query matching
type MatchConfig struct {
	Domains []string            `yaml:"domains"`
	Headers []HeaderMatchConfig `yaml:"headers"`
	Query   []QueryMatchConfig  `yaml:"query"`
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

// BackendConfig defines a static backend
type BackendConfig struct {
	URL    string `yaml:"url"`
	Weight int    `yaml:"weight"`
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
	Enabled bool          `yaml:"enabled"`
	Rate    int           `yaml:"rate"`
	Period  time.Duration `yaml:"period"`
	Burst   int           `yaml:"burst"`
	PerIP   bool          `yaml:"per_ip"`
	Mode    string        `yaml:"mode"` // "local" (default) or "distributed"
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
	Format string `yaml:"format"`
	Level  string `yaml:"level"`
	Output string `yaml:"output"`
}

// AdminConfig defines admin API settings
type AdminConfig struct {
	Enabled   bool            `yaml:"enabled"`
	Port      int             `yaml:"port"`
	Metrics   MetricsConfig   `yaml:"metrics"`   // Feature 5: Prometheus metrics
	Readiness ReadinessConfig `yaml:"readiness"` // Readiness probe configuration
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
	Action      string               `yaml:"action"`        // block, custom_response, redirect, set_headers, rewrite, group, log
	StatusCode  int                  `yaml:"status_code"`
	Body        string               `yaml:"body"`
	RedirectURL string               `yaml:"redirect_url"`
	Headers     HeaderTransform      `yaml:"headers"`
	Description string               `yaml:"description"`
	Rewrite     *RewriteActionConfig `yaml:"rewrite"`
	Group       string               `yaml:"group"`       // traffic split group name
	LogMessage  string               `yaml:"log_message"` // optional custom log message
}

// RewriteActionConfig defines path/query/header rewriting for the rewrite action.
type RewriteActionConfig struct {
	Path    string          `yaml:"path"`
	Query   string          `yaml:"query"`
	Headers HeaderTransform `yaml:"headers"`
}

// TrafficShapingConfig defines traffic shaping settings.
type TrafficShapingConfig struct {
	Throttle       ThrottleConfig       `yaml:"throttle"`
	Bandwidth      BandwidthConfig      `yaml:"bandwidth"`
	Priority       PriorityConfig       `yaml:"priority"`
	FaultInjection FaultInjectionConfig `yaml:"fault_injection"`
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

// DefaultConfig returns a configuration with sensible defaults
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
		},
		Admin: AdminConfig{
			Enabled: true,
			Port:    8081,
		},
	}
}
