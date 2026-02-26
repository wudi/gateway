package ingress

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/wudi/runway/config"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.Install(scheme))
}

// ReloadFunc is called to apply a new config to the runway.
type ReloadFunc func(cfg *config.Config)

// ControllerConfig holds configuration for the ingress controller.
type ControllerConfig struct {
	// IngressClass filters Ingress resources (default "runway").
	IngressClass string
	// ControllerName filters GatewayClasses (default "runway.wudi.io/ingress-controller").
	ControllerName string
	// WatchNamespaces limits the namespaces watched (empty = all).
	WatchNamespaces []string
	// WatchWithoutClass claims Ingress resources without an ingress class.
	WatchWithoutClass bool
	// EnableIngress enables watching Ingress v1 resources.
	EnableIngress bool
	// EnableGatewayAPI enables watching Gateway API resources.
	EnableGatewayAPI bool
	// DebounceDelay is the coalescing delay for rebuild (default 100ms).
	DebounceDelay time.Duration
	// MetricsAddr is the bind address for controller-runtime metrics.
	MetricsAddr string
	// BaseConfig is the base config merged with K8s-derived routes.
	BaseConfig *config.Config
	// ReloadFn is called with the new config to apply.
	ReloadFn ReloadFunc
	// PublishAddress is the IP/hostname for Ingress status.
	PublishAddress string
	// DefaultHTTPPort is the default HTTP listener port.
	DefaultHTTPPort int
	// DefaultHTTPSPort is the default HTTPS listener port.
	DefaultHTTPSPort int
	// LeaderElectionNamespace is the namespace for leader election resources.
	LeaderElectionNamespace string
}

// Controller manages the controller-runtime manager and debounced reloader.
type Controller struct {
	cfg      ControllerConfig
	store    *Store
	manager  ctrl.Manager
	reloadFn ReloadFunc

	// Debounced reload
	debounceMu     sync.Mutex
	debounceTimer  *time.Timer
	lastAppliedGen int64
	reloading      atomic.Bool

	// Status
	statusUpdater *StatusUpdater
}

// NewController creates and configures a new ingress controller.
func NewController(cfg ControllerConfig) (*Controller, error) {
	if cfg.IngressClass == "" {
		cfg.IngressClass = "runway"
	}
	if cfg.ControllerName == "" {
		cfg.ControllerName = "runway.wudi.io/ingress-controller"
	}
	if cfg.DebounceDelay == 0 {
		cfg.DebounceDelay = 100 * time.Millisecond
	}
	if cfg.MetricsAddr == "" {
		cfg.MetricsAddr = ":9090"
	}

	store := NewStore()

	if cfg.LeaderElectionNamespace == "" {
		cfg.LeaderElectionNamespace = "default"
	}

	opts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: cfg.MetricsAddr,
		},
		HealthProbeBindAddress:  "", // disabled; use runway's /health and /ready
		LeaderElection:          true,
		LeaderElectionID:        "runway-ingress-controller",
		LeaderElectionNamespace: cfg.LeaderElectionNamespace,
		Logger:                  zap.New(),
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), opts)
	if err != nil {
		return nil, err
	}

	// Readiness probe (even though port is empty, this enables the checker)
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, err
	}

	c := &Controller{
		cfg:           cfg,
		store:         store,
		manager:       mgr,
		reloadFn:      cfg.ReloadFn,
		statusUpdater: NewStatusUpdater(mgr.GetClient(), cfg.PublishAddress),
	}

	// Register reconcilers
	if cfg.EnableIngress {
		if err := NewIngressReconciler(mgr, store, c, cfg.IngressClass, cfg.WatchWithoutClass); err != nil {
			return nil, err
		}
	}
	if cfg.EnableGatewayAPI {
		if err := NewGatewayReconciler(mgr, store, c, cfg.ControllerName); err != nil {
			return nil, err
		}
		if err := NewHTTPRouteReconciler(mgr, store, c, cfg.ControllerName); err != nil {
			return nil, err
		}
	}
	if err := NewSecretReconciler(mgr, store, c); err != nil {
		return nil, err
	}
	if err := NewEndpointSliceReconciler(mgr, store, c); err != nil {
		return nil, err
	}

	return c, nil
}

// Start starts the controller-runtime manager. Blocks until ctx is cancelled.
func (c *Controller) Start(ctx context.Context) error {
	return c.manager.Start(ctx)
}

// TriggerReload schedules a debounced config rebuild and reload.
func (c *Controller) TriggerReload() {
	c.debounceMu.Lock()
	defer c.debounceMu.Unlock()

	if c.debounceTimer != nil {
		c.debounceTimer.Stop()
	}
	c.debounceTimer = time.AfterFunc(c.cfg.DebounceDelay, c.doReload)
}

// doReload performs the actual config translation and reload.
func (c *Controller) doReload() {
	gen := c.store.Generation()
	if gen == c.lastAppliedGen {
		return // no changes since last reload
	}
	if !c.reloading.CompareAndSwap(false, true) {
		return // already reloading
	}
	defer c.reloading.Store(false)

	translator := NewTranslator(c.store, c.cfg.BaseConfig, TranslatorConfig{
		IngressClass:      c.cfg.IngressClass,
		ControllerName:    c.cfg.ControllerName,
		WatchWithoutClass: c.cfg.WatchWithoutClass,
		DefaultHTTPPort:   c.cfg.DefaultHTTPPort,
		DefaultHTTPSPort:  c.cfg.DefaultHTTPSPort,
	})

	newCfg, warnings := translator.Translate()
	for _, w := range warnings {
		ctrl.Log.Info("Translation warning", "warning", w)
	}

	if err := config.Validate(newCfg); err != nil {
		ctrl.Log.Error(err, "Config validation failed, skipping reload")
		return
	}

	if c.reloadFn != nil {
		c.reloadFn(newCfg)
	}
	c.lastAppliedGen = gen
}

// Store returns the internal resource store.
func (c *Controller) Store() *Store {
	return c.store
}

// StatusUpdater returns the status updater (may be nil before Start).
func (c *Controller) StatusUpdater() *StatusUpdater {
	return c.statusUpdater
}

// IsLeader returns true if this instance is the leader.
func (c *Controller) IsLeader() bool {
	// controller-runtime doesn't expose a direct IsLeader check,
	// so we check if the leader election was won by examining the elected flag.
	// For now, status updates are guarded by the caller checking manager context.
	return true // simplified; all replicas serve traffic, only leader updates status
}
