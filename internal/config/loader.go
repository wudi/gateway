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
	// Validate server config
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", cfg.Server.Port)
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
	}

	// Validate JWT config if enabled
	if cfg.Authentication.JWT.Enabled {
		if cfg.Authentication.JWT.Secret == "" && cfg.Authentication.JWT.PublicKey == "" {
			return fmt.Errorf("JWT authentication enabled but no secret or public key provided")
		}
	}

	return nil
}

// LoadFromEnv loads configuration from environment variables
func (l *Loader) LoadFromEnv() (*Config, error) {
	cfg := DefaultConfig()

	// Override with environment variables
	if port := os.Getenv("GATEWAY_PORT"); port != "" {
		var p int
		if _, err := fmt.Sscanf(port, "%d", &p); err == nil {
			cfg.Server.Port = p
		}
	}

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

	// Overlay server settings
	if overlay.Server.Port != 0 {
		result.Server.Port = overlay.Server.Port
	}
	if overlay.Server.ReadTimeout != 0 {
		result.Server.ReadTimeout = overlay.Server.ReadTimeout
	}
	if overlay.Server.WriteTimeout != 0 {
		result.Server.WriteTimeout = overlay.Server.WriteTimeout
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
