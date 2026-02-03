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
	Server         ServerConfig         `yaml:"server"`          // Backward compat
	Listeners      []ListenerConfig     `yaml:"listeners"`       // New multi-listener support
	Registry       RegistryConfig       `yaml:"registry"`
	Authentication AuthenticationConfig `yaml:"authentication"`
	Routes         []RouteConfig        `yaml:"routes"`
	TCPRoutes      []TCPRouteConfig     `yaml:"tcp_routes"`      // TCP L4 routes
	UDPRoutes      []UDPRouteConfig     `yaml:"udp_routes"`      // UDP L4 routes
	Logging        LoggingConfig        `yaml:"logging"`
	Admin          AdminConfig          `yaml:"admin"`
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

// ServerConfig defines HTTP server settings
type ServerConfig struct {
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	IdleTimeout  time.Duration `yaml:"idle_timeout"`
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
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
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
	Key      string `yaml:"key"`
	ClientID string `yaml:"client_id"`
	Name     string `yaml:"name"`
}

// JWTConfig defines JWT authentication settings
type JWTConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Secret    string   `yaml:"secret"`
	PublicKey string   `yaml:"public_key"`
	Issuer    string   `yaml:"issuer"`
	Audience  []string `yaml:"audience"`
	Algorithm string   `yaml:"algorithm"` // HS256, RS256
}

// RouteConfig defines a single route
type RouteConfig struct {
	ID          string           `yaml:"id"`
	Path        string           `yaml:"path"`
	PathPrefix  bool             `yaml:"path_prefix"`
	Methods     []string         `yaml:"methods"`
	Backends    []BackendConfig  `yaml:"backends"`
	Service     ServiceConfig    `yaml:"service"`
	Auth        RouteAuthConfig  `yaml:"auth"`
	RateLimit   RateLimitConfig  `yaml:"rate_limit"`
	Transform   TransformConfig  `yaml:"transform"`
	Timeout     time.Duration    `yaml:"timeout"`
	Retries     int              `yaml:"retries"`
	StripPrefix bool             `yaml:"strip_prefix"`
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
	Methods  []string `yaml:"methods"` // jwt, api_key
}

// RateLimitConfig defines rate limiting settings
type RateLimitConfig struct {
	Enabled bool          `yaml:"enabled"`
	Rate    int           `yaml:"rate"`
	Period  time.Duration `yaml:"period"`
	Burst   int           `yaml:"burst"`
	PerIP   bool          `yaml:"per_ip"`
}

// TransformConfig defines request/response transformations
type TransformConfig struct {
	Request  RequestTransform  `yaml:"request"`
	Response ResponseTransform `yaml:"response"`
}

// RequestTransform defines request transformations
type RequestTransform struct {
	Headers HeaderTransform `yaml:"headers"`
}

// ResponseTransform defines response transformations
type ResponseTransform struct {
	Headers HeaderTransform `yaml:"headers"`
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
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
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
