package ingress

import (
	"fmt"
	"sort"

	"github.com/wudi/runway/config"
)

// Translator converts the Kubernetes resource store into a runway Config.
type Translator struct {
	store             *Store
	baseConfig        *config.Config // global settings from --base-config
	ingressClass      string         // filter Ingress by this class
	controllerName    string         // filter GatewayClass by this controller
	watchWithoutClass bool           // claim unclassed Ingress resources
	defaultHTTPPort   int            // default HTTP listener port
	defaultHTTPSPort  int            // default HTTPS listener port
}

// TranslatorConfig holds configuration for the Translator.
type TranslatorConfig struct {
	IngressClass      string
	ControllerName    string
	WatchWithoutClass bool
	DefaultHTTPPort   int
	DefaultHTTPSPort  int
}

// NewTranslator creates a new Translator.
func NewTranslator(store *Store, baseCfg *config.Config, tc TranslatorConfig) *Translator {
	httpPort := tc.DefaultHTTPPort
	if httpPort == 0 {
		httpPort = 8080
	}
	httpsPort := tc.DefaultHTTPSPort
	if httpsPort == 0 {
		httpsPort = 8443
	}
	return &Translator{
		store:             store,
		baseConfig:        baseCfg,
		ingressClass:      tc.IngressClass,
		controllerName:    tc.ControllerName,
		watchWithoutClass: tc.WatchWithoutClass,
		defaultHTTPPort:   httpPort,
		defaultHTTPSPort:  httpsPort,
	}
}

// Translate builds a complete config.Config from the current K8s resource store
// merged with the base config.
func (t *Translator) Translate() (*config.Config, []string) {
	cfg := t.cloneBase()
	var warnings []string

	// Translate Ingress resources
	ingRoutes, ingListeners, ingWarnings := t.translateIngresses()
	warnings = append(warnings, ingWarnings...)

	// Translate Gateway API resources
	gwRoutes, gwListeners, gwWarnings := t.translateGatewayAPI()
	warnings = append(warnings, gwWarnings...)

	// Merge routes: K8s-derived routes are added alongside (not replacing) base config routes.
	// Deduplicate by route ID — K8s-derived routes take precedence.
	routeMap := make(map[string]config.RouteConfig)
	for _, r := range cfg.Routes {
		routeMap[r.ID] = r
	}
	for _, r := range ingRoutes {
		routeMap[r.ID] = r
	}
	for _, r := range gwRoutes {
		routeMap[r.ID] = r
	}
	cfg.Routes = make([]config.RouteConfig, 0, len(routeMap))
	for _, r := range routeMap {
		cfg.Routes = append(cfg.Routes, r)
	}
	// Sort routes for deterministic output.
	sort.Slice(cfg.Routes, func(i, j int) bool {
		return cfg.Routes[i].ID < cfg.Routes[j].ID
	})

	// Merge listeners: K8s-derived listeners are additive to base config.
	listenerMap := make(map[string]config.ListenerConfig)
	for _, l := range cfg.Listeners {
		listenerMap[l.ID] = l
	}
	for _, l := range ingListeners {
		if existing, ok := listenerMap[l.ID]; ok {
			// Merge TLS certificates into existing listener
			existing.TLS.Certificates = append(existing.TLS.Certificates, l.TLS.Certificates...)
			if l.TLS.Enabled {
				existing.TLS.Enabled = true
			}
			listenerMap[l.ID] = existing
		} else {
			listenerMap[l.ID] = l
		}
	}
	for _, l := range gwListeners {
		if _, ok := listenerMap[l.ID]; ok {
			warnings = append(warnings, fmt.Sprintf("Gateway listener %s conflicts with existing listener", l.ID))
			continue
		}
		listenerMap[l.ID] = l
	}
	cfg.Listeners = make([]config.ListenerConfig, 0, len(listenerMap))
	for _, l := range listenerMap {
		cfg.Listeners = append(cfg.Listeners, l)
	}
	sort.Slice(cfg.Listeners, func(i, j int) bool {
		return cfg.Listeners[i].ID < cfg.Listeners[j].ID
	})

	// Ensure at least one listener exists.
	if len(cfg.Listeners) == 0 {
		cfg.Listeners = []config.ListenerConfig{
			{
				ID:       "ingress-http",
				Address:  fmt.Sprintf(":%d", t.defaultHTTPPort),
				Protocol: config.ProtocolHTTP,
			},
		}
	}

	return cfg, warnings
}

// cloneBase creates a shallow copy of the base config (or a default if nil).
func (t *Translator) cloneBase() *config.Config {
	if t.baseConfig == nil {
		return &config.Config{}
	}
	// Shallow copy the struct, then nil out Routes/Listeners so we rebuild them.
	cfg := *t.baseConfig
	cfg.Routes = make([]config.RouteConfig, len(t.baseConfig.Routes))
	copy(cfg.Routes, t.baseConfig.Routes)
	cfg.Listeners = make([]config.ListenerConfig, len(t.baseConfig.Listeners))
	copy(cfg.Listeners, t.baseConfig.Listeners)
	return &cfg
}
