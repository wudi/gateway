# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /gateway ./cmd/gateway

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -g '' appuser

# Copy binary from builder
COPY --from=builder /gateway /app/gateway

# Copy default config
COPY configs/gateway.yaml /app/configs/gateway.yaml

# Set ownership
RUN chown -R appuser:appuser /app

USER appuser

# Expose ports
# 8080 - Main gateway
# 8081 - Admin API
# 8082 - Registry API (if memory registry with API enabled)
EXPOSE 8080 8081 8082

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8081/health || exit 1

ENTRYPOINT ["/app/gateway"]
CMD ["-config", "/app/configs/gateway.yaml"]
