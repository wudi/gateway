package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/gateway"
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
		log.Fatalf("Failed to load configuration: %v", err)
	}

	if *validateOnly {
		fmt.Println("Configuration is valid")
		os.Exit(0)
	}

	// Print startup banner
	log.Printf("Starting API Gateway %s", version)
	log.Printf("Configuration loaded from %s", *configPath)
	log.Printf("Registry type: %s", cfg.Registry.Type)
	log.Printf("Routes configured: %d", len(cfg.Routes))

	// Create and start the server
	server, err := gateway.NewServer(cfg)
	if err != nil {
		log.Fatalf("Failed to create gateway: %v", err)
	}

	// Run the server
	if err := server.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
