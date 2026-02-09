# Build stage — native compilation with Go cross-compile
FROM --platform=$BUILDPLATFORM golang:1.25.5-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG BUILD_TIME

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-w -s -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
    -o /gateway ./cmd/gateway

# Runtime stage
FROM alpine:3.21

LABEL org.opencontainers.image.title="Gateway" \
      org.opencontainers.image.description="High-performance API gateway" \
      org.opencontainers.image.source="https://github.com/wudi/gateway" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 -g '' appuser \
    && mkdir -p /app/configs /app/certs /app/specs /app/geoip \
    && chown -R appuser:appuser /app

COPY --from=builder --chown=appuser:appuser /gateway /app/gateway

USER appuser

EXPOSE 8080 8081 8082

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8081/health || exit 1

ENTRYPOINT ["/app/gateway"]
CMD ["-config", "/app/configs/gateway.yaml"]
