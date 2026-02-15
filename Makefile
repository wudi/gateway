.PHONY: all build run test clean deps lint fmt vet \
	docker-build docker-buildx docker-buildx-local docker-push docker-run \
	compose-up compose-down compose-logs \
	compose-up-redis compose-up-otel compose-up-consul compose-up-etcd compose-up-all \
	bench bench-save bench-compare \
	perf-stack-up perf-stack-down perf-smoke perf-load perf-stress perf-profile

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

# Benchmarks
BENCH_FLAGS?=-benchmem -count=1 -run=^$$ -bench=.
BENCH_DIR=perf/results

bench:
	@echo "Running benchmarks..."
	$(GOTEST) $(BENCH_FLAGS) ./...

bench-save:
	@echo "Running benchmarks and saving results..."
	@mkdir -p $(BENCH_DIR)
	$(GOTEST) $(BENCH_FLAGS) ./... | tee $(BENCH_DIR)/bench-$(shell date +%Y%m%d-%H%M%S).txt

bench-compare:
	@echo "Comparing last two benchmark results..."
	@if ! command -v benchstat > /dev/null; then \
		echo "benchstat not installed. Run: go install golang.org/x/perf/cmd/benchstat@latest"; \
		exit 1; \
	fi
	@OLD=$$(ls -t $(BENCH_DIR)/bench-*.txt 2>/dev/null | sed -n '2p'); \
	NEW=$$(ls -t $(BENCH_DIR)/bench-*.txt 2>/dev/null | sed -n '1p'); \
	if [ -z "$$OLD" ] || [ -z "$$NEW" ]; then \
		echo "Need at least 2 saved benchmark runs. Run 'make bench-save' twice."; \
		exit 1; \
	fi; \
	echo "Comparing $$OLD vs $$NEW"; \
	benchstat "$$OLD" "$$NEW"

# Performance testing stack
PERF_COMPOSE=docker compose -f perf/docker-compose.perf.yaml

perf-stack-up:
	@echo "Starting performance testing stack..."
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) $(PERF_COMPOSE) --profile monitoring up -d --build

perf-stack-down:
	$(PERF_COMPOSE) --profile monitoring --profile k6 down

perf-smoke:
	@echo "Running k6 smoke test..."
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) $(PERF_COMPOSE) --profile k6 run --rm \
		-e K6_OUT=experimental-prometheus-rw k6 run /scripts/smoke.js

perf-load:
	@echo "Running k6 load test..."
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) $(PERF_COMPOSE) --profile k6 run --rm \
		-e K6_OUT=experimental-prometheus-rw k6 run /scripts/load.js

perf-stress:
	@echo "Running k6 stress test..."
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) $(PERF_COMPOSE) --profile k6 run --rm \
		-e K6_OUT=experimental-prometheus-rw k6 run /scripts/stress.js

perf-profile:
	@echo "Capturing pprof profiles..."
	perf/scripts/capture-profiles.sh perf/results/profiles-$(shell date +%Y%m%d-%H%M%S)

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
	@echo ""
	@echo "Benchmarks & Performance:"
	@echo "  bench            - Run Go benchmarks"
	@echo "  bench-save       - Run benchmarks and save results"
	@echo "  bench-compare    - Compare last two benchmark runs (requires benchstat)"
	@echo "  perf-stack-up    - Start perf stack (gateway + Prometheus + Grafana)"
	@echo "  perf-stack-down  - Stop perf stack"
	@echo "  perf-smoke       - Run k6 smoke test"
	@echo "  perf-load        - Run k6 load test"
	@echo "  perf-stress      - Run k6 stress test"
	@echo "  perf-profile     - Capture pprof profiles from running gateway"
	@echo ""
	@echo "  help             - Show this help"
