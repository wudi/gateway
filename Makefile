.PHONY: all build run test clean deps lint fmt vet \
	docker-build docker-buildx docker-buildx-local docker-push docker-run \
	compose-up compose-down compose-logs \
	compose-up-redis compose-up-otel compose-up-consul compose-up-etcd compose-up-all

# Build variables
BINARY_NAME=gateway
BUILD_DIR=build
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"

# Docker variables
IMAGE_REGISTRY?=ghcr.io
IMAGE_REPO?=wudi
IMAGE_NAME?=$(IMAGE_REGISTRY)/$(IMAGE_REPO)/$(BINARY_NAME)
IMAGE_TAG?=$(VERSION)
PLATFORMS?=linux/amd64,linux/arm64

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

# Docker build (single-arch, local)
docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		-t $(IMAGE_NAME):latest .

# Docker buildx (multi-arch, push to registry)
docker-buildx:
	docker buildx build \
		--platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		-t $(IMAGE_NAME):latest \
		--push .

# Docker buildx (single-arch, load locally)
docker-buildx-local:
	docker buildx build \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		-t $(IMAGE_NAME):latest \
		--load .

# Push pre-built image
docker-push:
	docker push $(IMAGE_NAME):$(IMAGE_TAG)
	docker push $(IMAGE_NAME):latest

# Docker run with config mount
docker-run:
	docker run -p 8080:8080 -p 8081:8081 -p 8082:8082 \
		-v $(CURDIR)/configs:/app/configs:ro \
		$(IMAGE_NAME):$(IMAGE_TAG)

# Compose targets
compose-up:
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) docker compose up -d --build

compose-down:
	docker compose --profile redis --profile otel --profile consul --profile etcd down

compose-logs:
	docker compose logs -f

compose-up-redis:
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) docker compose --profile redis up -d --build

compose-up-otel:
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) docker compose --profile otel up -d --build

compose-up-consul:
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) docker compose --profile consul up -d --build

compose-up-etcd:
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) docker compose --profile etcd up -d --build

compose-up-all:
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) docker compose \
		--profile redis --profile otel --profile consul --profile etcd up -d --build

# Start a mock backend server for testing
mock-backend:
	@echo "Starting mock backend on port 9001..."
	@go run -e 'package main; import ("fmt"; "net/http"); func main() { http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "{\"message\": \"Hello from backend\", \"path\": \"%s\"}", r.URL.Path) }); http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); w.Write([]byte("{\"status\": \"ok\"}")) }); fmt.Println("Mock backend running on :9001"); http.ListenAndServe(":9001", nil) }'

# Help
help:
	@echo "Available targets:"
	@echo "  all              - Install deps and build"
	@echo "  build            - Build the binary"
	@echo "  build-all        - Build for all platforms"
	@echo "  run              - Build and run the gateway"
	@echo "  dev              - Run with hot reload (requires air)"
	@echo "  test             - Run tests"
	@echo "  test-coverage    - Run tests with coverage report"
	@echo "  deps             - Install dependencies"
	@echo "  deps-update      - Update dependencies"
	@echo "  fmt              - Format code"
	@echo "  vet              - Run go vet"
	@echo "  lint             - Run linter"
	@echo "  validate         - Validate configuration"
	@echo "  clean            - Clean build artifacts"
	@echo "  docker-build     - Build Docker image (local)"
	@echo "  docker-buildx    - Build multi-arch image and push to registry"
	@echo "  docker-buildx-local - Build with buildx and load locally"
	@echo "  docker-push      - Push image to registry"
	@echo "  docker-run       - Run Docker container"
	@echo "  compose-up       - Start gateway + backends"
	@echo "  compose-down     - Stop all services (all profiles)"
	@echo "  compose-logs     - Follow compose logs"
	@echo "  compose-up-redis - Start with Redis"
	@echo "  compose-up-otel  - Start with OTEL collector"
	@echo "  compose-up-consul - Start with Consul"
	@echo "  compose-up-etcd  - Start with etcd"
	@echo "  compose-up-all   - Start with all infrastructure"
	@echo "  help             - Show this help"
