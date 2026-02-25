package gateway

import (
	"fmt"
	"net/http"
	"time"

	igw "github.com/wudi/gateway/internal/gateway"
)

// GatewayBuilder constructs a gateway Server with custom features and middleware.
type GatewayBuilder struct {
	cfg              *Config
	configPath       string
	features         []Feature
	middlewareSlots  []MiddlewareSlot
	globalSlots      []GlobalMiddlewareSlot
	useDefaults      bool
}

// New creates a new GatewayBuilder for the given configuration.
func New(cfg *Config) *GatewayBuilder {
	return &GatewayBuilder{
		cfg: cfg,
	}
}

// WithConfigPath sets the path to the YAML config file (used for reload).
func (b *GatewayBuilder) WithConfigPath(path string) *GatewayBuilder {
	b.configPath = path
	return b
}

// WithDefaults registers all built-in features and middleware.
// This is what cmd/gateway uses. Without this call, the gateway starts
// with no built-in features — only custom ones you register.
func (b *GatewayBuilder) WithDefaults() *GatewayBuilder {
	b.useDefaults = true
	return b
}

// WithFeatures registers multiple custom features at once.
func (b *GatewayBuilder) WithFeatures(ff ...Feature) *GatewayBuilder {
	b.features = append(b.features, ff...)
	return b
}

// AddFeature registers a single custom feature. If the feature implements
// MiddlewareProvider, its middleware slots are also registered.
func (b *GatewayBuilder) AddFeature(f Feature) *GatewayBuilder {
	b.features = append(b.features, f)
	return b
}

// AddMiddleware registers a custom per-route middleware at the specified
// position in the chain.
func (b *GatewayBuilder) AddMiddleware(slot MiddlewareSlot) *GatewayBuilder {
	b.middlewareSlots = append(b.middlewareSlots, slot)
	return b
}

// AddGlobalMiddleware registers a custom global middleware at the specified
// position in the global handler chain.
func (b *GatewayBuilder) AddGlobalMiddleware(slot GlobalMiddlewareSlot) *GatewayBuilder {
	b.globalSlots = append(b.globalSlots, slot)
	return b
}

// Build validates the configuration and constructs a ready-to-run Server.
func (b *GatewayBuilder) Build() (*Server, error) {
	// Collect middleware slots from features that implement MiddlewareProvider
	allMWSlots := make([]MiddlewareSlot, len(b.middlewareSlots))
	copy(allMWSlots, b.middlewareSlots)
	allGlobalSlots := make([]GlobalMiddlewareSlot, len(b.globalSlots))
	copy(allGlobalSlots, b.globalSlots)

	for _, f := range b.features {
		if mp, ok := f.(MiddlewareProvider); ok {
			allMWSlots = append(allMWSlots, mp.MiddlewareSlots()...)
		}
		if gmp, ok := f.(GlobalMiddlewareProvider); ok {
			allGlobalSlots = append(allGlobalSlots, gmp.GlobalMiddlewareSlots()...)
		}
	}

	// Validate custom feature configs
	for _, f := range b.features {
		if cv, ok := f.(ConfigValidator); ok {
			if err := cv.ValidateConfig(b.cfg); err != nil {
				return nil, fmt.Errorf("feature %q config validation: %w", f.Name(), err)
			}
		}
	}

	// Convert public types to internal options
	opts := igw.ExternalOptions{
		UseDefaults:     b.useDefaults,
		CustomSlots:     toInternalSlots(allMWSlots),
		CustomGlobal:    toInternalGlobalSlots(allGlobalSlots),
		ExternalFeatures: toInternalFeatures(b.features),
	}

	srv, err := igw.NewServer(b.cfg, b.configPath, opts)
	if err != nil {
		return nil, err
	}

	return &Server{internal: srv}, nil
}

// Server wraps the internal gateway server with a public API.
type Server struct {
	internal *igw.Server
}

// Run starts the server and blocks until shutdown.
func (s *Server) Run() error {
	return s.internal.Run()
}

// Start starts the server without blocking.
func (s *Server) Start() error {
	return s.internal.Start()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(timeout time.Duration) error {
	return s.internal.Shutdown(timeout)
}

// Handler returns the server's root http.Handler, useful for testing
// or embedding in other servers.
func (s *Server) Handler() http.Handler {
	return s.internal.Handler()
}

// Drain initiates connection draining.
func (s *Server) Drain() {
	s.internal.Drain()
}

// IsDraining returns true if the server is in drain mode.
func (s *Server) IsDraining() bool {
	return s.internal.IsDraining()
}

// ReloadConfig triggers a hot config reload from the config file.
func (s *Server) ReloadConfig() igw.ReloadResult {
	return s.internal.ReloadConfig()
}

// Reload performs a hot config reload using the provided Config object.
// This is used by the ingress controller to push K8s-derived configs.
func (s *Server) Reload(cfg *Config) igw.ReloadResult {
	return s.internal.ReloadWithConfig(cfg)
}

// --- internal type conversions ---

func toInternalSlots(slots []MiddlewareSlot) []igw.CustomSlot {
	out := make([]igw.CustomSlot, len(slots))
	for i, s := range slots {
		out[i] = igw.CustomSlot{
			Name:   s.Name,
			After:  s.After,
			Before: s.Before,
			Build:  s.Build,
		}
	}
	return out
}

func toInternalGlobalSlots(slots []GlobalMiddlewareSlot) []igw.CustomGlobalSlot {
	out := make([]igw.CustomGlobalSlot, len(slots))
	for i, s := range slots {
		out[i] = igw.CustomGlobalSlot{
			Name:   s.Name,
			After:  s.After,
			Before: s.Before,
			Build:  s.Build,
		}
	}
	return out
}

func toInternalFeatures(features []Feature) []igw.ExternalFeature {
	out := make([]igw.ExternalFeature, len(features))
	for i, f := range features {
		out[i] = igw.ExternalFeature{
			Feature: f,
		}
	}
	return out
}
