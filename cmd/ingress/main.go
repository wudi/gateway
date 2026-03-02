package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	gw "github.com/wudi/runway/runway"
	"github.com/wudi/runway/internal/ingress"
	"github.com/wudi/runway/internal/logging"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	// Protocol translators (auto-register)
	_ "github.com/wudi/runway/internal/proxy/protocol/grpc"
	_ "github.com/wudi/runway/internal/proxy/protocol/grpcjson"
	_ "github.com/wudi/runway/internal/proxy/protocol/grpcweb"
	_ "github.com/wudi/runway/internal/proxy/protocol/rest"
	_ "github.com/wudi/runway/internal/proxy/protocol/thrift"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	// Flags
	showVersion := flag.Bool("version", false, "Show version information")
	ingressClass := flag.String("ingress-class", "runway", "IngressClass name to watch")
	controllerName := flag.String("controller-name", "runway.wudi.io/ingress-controller", "Controller name for GatewayClass")
	watchNamespaces := flag.String("watch-namespaces", "", "Comma-separated namespaces to watch (empty = all)")
	watchWithoutClass := flag.Bool("watch-ingress-without-class", false, "Claim Ingress resources without an ingress class")
	publishService := flag.String("publish-service", "", "Service namespace/name for status IP (e.g. runway-system/runway)")
	publishAddress := flag.String("publish-status-address", "", "Explicit IP/hostname for Ingress status")
	httpPort := flag.Int("http-port", 8080, "HTTP listener port")
	httpsPort := flag.Int("https-port", 8443, "HTTPS listener port")
	adminPort := flag.Int("admin-port", 8081, "Admin API port")
	metricsPort := flag.Int("metrics-port", 9090, "Controller-runtime metrics port")
	baseConfigPath := flag.String("base-config", "", "Path to base YAML config for global settings")
	debounceDelay := flag.Duration("debounce-delay", 100*time.Millisecond, "Debounce delay for config rebuild")
	leaderElectionNS := flag.String("leader-election-namespace", "default", "Namespace for leader election resources")
	enableGatewayAPI := flag.Bool("enable-gateway-api", true, "Watch Gateway API resources")
	enableIngress := flag.Bool("enable-ingress", true, "Watch Ingress v1 resources")
	flag.Parse()

	if *showVersion {
		fmt.Printf("Runway Ingress Controller %s (built %s)\n", version, buildTime)
		os.Exit(0)
	}

	// Initialize logger
	logger, logCloser, err := logging.New(logging.Config{Level: "info"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	if logCloser != nil {
		defer logCloser.Close()
	}
	logging.SetGlobal(logger)

	logging.Info("Starting Runway Ingress Controller",
		zap.String("version", version),
		zap.String("ingress-class", *ingressClass),
		zap.String("controller-name", *controllerName),
	)

	// Load base config (if provided)
	var baseCfg *gw.Config
	if *baseConfigPath != "" {
		baseCfg, err = gw.LoadConfig(*baseConfigPath)
		if err != nil {
			logging.Error("Failed to load base config", zap.Error(err))
			os.Exit(1)
		}
	}

	// Build a minimal runway config for initial startup
	startupCfg := buildStartupConfig(baseCfg, *httpPort, *adminPort)

	// Build the runway server
	server, err := gw.New(startupCfg).
		WithDefaults().
		Build()
	if err != nil {
		logging.Error("Failed to create runway server", zap.Error(err))
		os.Exit(1)
	}

	// Parse watch namespaces
	var namespaces []string
	if *watchNamespaces != "" {
		namespaces = strings.Split(*watchNamespaces, ",")
	}

	// Determine publish address
	addr := *publishAddress
	_ = *publishService // TODO: resolve Service to LoadBalancer IP

	// Create the ingress controller
	controllerCfg := ingress.ControllerConfig{
		IngressClass:            *ingressClass,
		ControllerName:          *controllerName,
		WatchNamespaces:         namespaces,
		WatchWithoutClass:       *watchWithoutClass,
		EnableIngress:           *enableIngress,
		EnableGatewayAPI:        *enableGatewayAPI,
		DebounceDelay:           *debounceDelay,
		MetricsAddr:             fmt.Sprintf(":%d", *metricsPort),
		BaseConfig:              baseCfg,
		PublishAddress:          addr,
		DefaultHTTPPort:         *httpPort,
		DefaultHTTPSPort:        *httpsPort,
		LeaderElectionNamespace: *leaderElectionNS,
		ReloadFn: func(cfg *gw.Config) {
			result := server.Reload(cfg)
			if result.Success {
				logging.Info("Config reloaded successfully",
					zap.Strings("changes", result.Changes),
				)
			} else {
				logging.Error("Config reload failed", zap.String("error", result.Error))
			}
		},
	}

	ctrl, err := ingress.NewController(controllerCfg)
	if err != nil {
		logging.Error("Failed to create ingress controller", zap.Error(err))
		os.Exit(1)
	}

	// Context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Run both the runway server and the controller-runtime manager
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logging.Info("Starting runway server",
			zap.Int("http-port", *httpPort),
			zap.Int("admin-port", *adminPort),
		)
		if err := server.Start(); err != nil {
			return fmt.Errorf("runway server: %w", err)
		}
		<-gctx.Done()
		logging.Info("Shutting down runway server")
		return server.Shutdown(30 * time.Second)
	})

	g.Go(func() error {
		logging.Info("Starting controller-runtime manager")
		return ctrl.Start(gctx)
	})

	if err := g.Wait(); err != nil {
		logging.Error("Shutdown error", zap.Error(err))
		os.Exit(1)
	}

	logging.Info("Runway Ingress Controller stopped")
}

// buildStartupConfig creates a minimal config for initial runway startup
// before any K8s resources are discovered.
func buildStartupConfig(baseCfg *gw.Config, httpPort, adminPort int) *gw.Config {
	if baseCfg != nil {
		cfg := *baseCfg
		// Ensure at least one listener exists
		if len(cfg.Listeners) == 0 {
			cfg.Listeners = []gw.ListenerConfig{
				{
					ID:       "ingress-http",
					Address:  fmt.Sprintf(":%d", httpPort),
					Protocol: gw.ProtocolHTTP,
				},
			}
		}
		// Ensure admin is configured
		cfg.Admin.Enabled = true
		if cfg.Admin.Port == 0 {
			cfg.Admin.Port = adminPort
		}
		return &cfg
	}

	return &gw.Config{
		Listeners: []gw.ListenerConfig{
			{
				ID:       "ingress-http",
				Address:  fmt.Sprintf(":%d", httpPort),
				Protocol: gw.ProtocolHTTP,
			},
		},
		Admin: gw.AdminConfig{
			Enabled: true,
			Port:    adminPort,
		},
	}
}
