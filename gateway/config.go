package gateway

import (
	"github.com/wudi/gateway/config"
)

// Config is the top-level gateway configuration.
type Config = config.Config

// RouteConfig is the per-route configuration.
type RouteConfig = config.RouteConfig

// ListenerConfig is the listener configuration.
type ListenerConfig = config.ListenerConfig

// AdminConfig is the admin API configuration.
type AdminConfig = config.AdminConfig

// Protocol type alias.
const ProtocolHTTP = config.ProtocolHTTP

// LoadConfig loads and validates a gateway configuration from a YAML file.
func LoadConfig(path string) (*Config, error) {
	return config.NewLoader().Load(path)
}

// ParseConfig parses and validates a gateway configuration from YAML bytes.
func ParseConfig(data []byte) (*Config, error) {
	return config.NewLoader().Parse(data)
}
