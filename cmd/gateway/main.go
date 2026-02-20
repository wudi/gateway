package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/gateway"
	"github.com/wudi/gateway/internal/logging"
	"go.uber.org/zap"

	// Protocol translators (auto-register)
	_ "github.com/wudi/gateway/internal/proxy/protocol/grpc"
	_ "github.com/wudi/gateway/internal/proxy/protocol/rest"
	_ "github.com/wudi/gateway/internal/proxy/protocol/thrift"
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
	logger, logCloser, err := logging.New(logging.Config{
		Level:      cfg.Logging.Level,
		Output:     cfg.Logging.Output,
		MaxSize:    cfg.Logging.Rotation.MaxSize,
		MaxBackups: cfg.Logging.Rotation.MaxBackups,
		MaxAge:     cfg.Logging.Rotation.MaxAge,
		Compress:   cfg.Logging.Rotation.Compress,
		LocalTime:  cfg.Logging.Rotation.LocalTime,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	if logCloser != nil {
		defer logCloser.Close()
	}
	logging.SetGlobal(logger)

	// Print startup banner
	logging.Info("Starting API Gateway",
		zap.String("version", version),
		zap.String("config", *configPath),
		zap.String("registry", cfg.Registry.Type),
		zap.Int("routes", len(cfg.Routes)),
	)

	// Create and start the server
	server, err := gateway.NewServer(cfg, *configPath)
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
