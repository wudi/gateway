package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/gateway"
	"github.com/example/gateway/internal/logging"
	"go.uber.org/zap"

	// Protocol translators (auto-register)
	_ "github.com/example/gateway/internal/proxy/protocol/grpc"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "configs/gateway.yaml", "Path to configuration file")
	showVersion := flag.Bool("version", false, "Show version information")
	validateOnly := flag.Bool("validate", false, "Validate configuration and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("API Gateway %s (built %s)\n", version, buildTime)
		os.Exit(0)
	}

	// Load configuration
	loader := config.NewLoader()
	cfg, err := loader.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	if *validateOnly {
		fmt.Println("Configuration is valid")
		os.Exit(0)
	}

	// Initialize structured logger
	logger, err := logging.New(cfg.Logging.Level)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	logging.SetGlobal(logger)

	// Print startup banner
	logging.Info("Starting API Gateway",
		zap.String("version", version),
		zap.String("config", *configPath),
		zap.String("registry", cfg.Registry.Type),
		zap.Int("routes", len(cfg.Routes)),
	)

	// Create and start the server
	server, err := gateway.NewServer(cfg)
	if err != nil {
		logging.Error("Failed to create gateway", zap.Error(err))
		os.Exit(1)
	}

	// Run the server
	if err := server.Run(); err != nil {
		logging.Error("Server error", zap.Error(err))
		os.Exit(1)
	}
}
