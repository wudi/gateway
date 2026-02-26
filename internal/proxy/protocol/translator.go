// Package protocol provides extensible HTTP-to-backend protocol translation.
package protocol

import (
	"net/http"
	"sync/atomic"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
)

// Translator translates HTTP requests to a backend protocol.
// Implementations: gRPC, (future) Thrift, Dubbo, Kafka
type Translator interface {
	// Name returns the protocol type identifier (e.g., "http_to_grpc").
	Name() string

	// Handler returns an http.Handler for the given route that translates
	// HTTP requests to the backend protocol.
	Handler(routeID string, balancer loadbalancer.Balancer, cfg config.ProtocolConfig) (http.Handler, error)

	// Close releases resources for the specified route.
	Close(routeID string) error

	// Metrics returns metrics for the specified route.
	Metrics(routeID string) *TranslatorMetrics
}

// TranslatorMetrics contains statistics for a protocol translator.
type TranslatorMetrics struct {
	Requests     int64   `json:"requests"`
	Successes    int64   `json:"successes"`
	Failures     int64   `json:"failures"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	ProtocolType string  `json:"protocol_type"`
}

// RouteMetrics provides atomic counters for per-route metrics.
type RouteMetrics struct {
	Requests       atomic.Int64
	Successes      atomic.Int64
	Failures       atomic.Int64
	TotalLatencyNs atomic.Int64
}

// Snapshot returns a copy of the current metrics.
func (m *RouteMetrics) Snapshot(protocolType string) *TranslatorMetrics {
	requests := m.Requests.Load()
	avgLatencyMs := float64(0)
	if requests > 0 {
		avgLatencyMs = float64(m.TotalLatencyNs.Load()) / float64(requests) / 1e6
	}
	return &TranslatorMetrics{
		Requests:     requests,
		Successes:    m.Successes.Load(),
		Failures:     m.Failures.Load(),
		AvgLatencyMs: avgLatencyMs,
		ProtocolType: protocolType,
	}
}
