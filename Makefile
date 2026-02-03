.PHONY: all build run test clean deps lint fmt vet

# Build variables
BINARY_NAME=gateway
BUILD_DIR=build
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"

# Go commands
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
GOVET=$(GOCMD) vet

all: deps build

# Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/gateway

# Build for multiple platforms
build-all: build-linux build-darwin build-windows

build-linux:
	@echo "Building for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/gateway

build-darwin:
	@echo "Building for macOS..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/gateway
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/gateway

build-windows:
	@echo "Building for Windows..."
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/gateway

# Run the gateway
run: build
	@echo "Starting gateway..."
	./$(BUILD_DIR)/$(BINARY_NAME) -config configs/gateway.yaml

# Run with hot reload (requires air: go install github.com/cosmtrek/air@latest)
dev:
	@if command -v air > /dev/null; then \
		air; \
	else \
		echo "Air not installed. Run: go install github.com/cosmtrek/air@latest"; \
		exit 1; \
	fi

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race -cover ./...

# Run tests with coverage report
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Install dependencies
deps:
	@echo "Installing dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

# Update dependencies
deps-update:
	@echo "Updating dependencies..."
	$(GOGET) -u ./...
	$(GOMOD) tidy

# Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

# Run go vet
vet:
	@echo "Running go vet..."
	$(GOVET) ./...

# Run linter (requires golangci-lint)
lint:
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

# Validate configuration
validate:
	./$(BUILD_DIR)/$(BINARY_NAME) -config configs/gateway.yaml -validate

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# Docker build
docker-build:
	docker build -t $(BINARY_NAME):$(VERSION) .

# Docker run
docker-run:
	docker run -p 8080:8080 -p 8081:8081 -p 8082:8082 $(BINARY_NAME):$(VERSION)

# Start a mock backend server for testing
mock-backend:
	@echo "Starting mock backend on port 9001..."
	@go run -e 'package main; import ("fmt"; "net/http"); func main() { http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "{\"message\": \"Hello from backend\", \"path\": \"%s\"}", r.URL.Path) }); http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); w.Write([]byte("{\"status\": \"ok\"}")) }); fmt.Println("Mock backend running on :9001"); http.ListenAndServe(":9001", nil) }'

# Help
help:
	@echo "Available targets:"
	@echo "  all           - Install deps and build"
	@echo "  build         - Build the binary"
	@echo "  build-all     - Build for all platforms"
	@echo "  run           - Build and run the gateway"
	@echo "  dev           - Run with hot reload (requires air)"
	@echo "  test          - Run tests"
	@echo "  test-coverage - Run tests with coverage report"
	@echo "  deps          - Install dependencies"
	@echo "  deps-update   - Update dependencies"
	@echo "  fmt           - Format code"
	@echo "  vet           - Run go vet"
	@echo "  lint          - Run linter"
	@echo "  validate      - Validate configuration"
	@echo "  clean         - Clean build artifacts"
	@echo "  docker-build  - Build Docker image"
	@echo "  docker-run    - Run Docker container"
	@echo "  help          - Show this help"
