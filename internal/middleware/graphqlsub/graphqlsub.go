package graphqlsub

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware"
)

// SubscriptionHandler enforces connection limits and tracks stats
// for GraphQL subscription (WebSocket) upgrades. The actual WebSocket
// proxying is handled by the downstream websocket middleware.
type SubscriptionHandler struct {
	protocol       string
	pingInterval   time.Duration
	maxConnections int
	activeConns    atomic.Int64
	totalConns     atomic.Int64
	totalMessages  atomic.Int64
}

// New creates a SubscriptionHandler from config.
func New(cfg config.GraphQLSubscriptionConfig) *SubscriptionHandler {
	protocol := cfg.Protocol
	if protocol == "" {
		protocol = "graphql-transport-ws"
	}
	pingInterval := cfg.PingInterval
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	return &SubscriptionHandler{
		protocol:       protocol,
		pingInterval:   pingInterval,
		maxConnections: cfg.MaxConnections,
	}
}

// Middleware returns a middleware that enforces GraphQL subscription
// connection limits and tracks active/total connection counts.
// Non-subscription requests pass through unchanged.
func (h *SubscriptionHandler) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isGraphQLSubscription(r) {
				next.ServeHTTP(w, r)
				return
			}
			if h.maxConnections > 0 && h.activeConns.Load() >= int64(h.maxConnections) {
				http.Error(w, "too many subscription connections", http.StatusServiceUnavailable)
				return
			}
			h.activeConns.Add(1)
			h.totalConns.Add(1)
			defer h.activeConns.Add(-1)
			next.ServeHTTP(w, r)
		})
	}
}

// isGraphQLSubscription returns true if the request is a WebSocket
// upgrade carrying one of the standard GraphQL subscription subprotocols.
func isGraphQLSubscription(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	proto := r.Header.Get("Sec-WebSocket-Protocol")
	return strings.Contains(proto, "graphql-ws") || strings.Contains(proto, "graphql-transport-ws")
}

// ActiveConns returns the current number of active subscription connections.
func (h *SubscriptionHandler) ActiveConns() int64 {
	return h.activeConns.Load()
}

// TotalConns returns the total number of subscription connections served.
func (h *SubscriptionHandler) TotalConns() int64 {
	return h.totalConns.Load()
}

// TotalMessages returns the total number of subscription messages relayed.
func (h *SubscriptionHandler) TotalMessages() int64 {
	return h.totalMessages.Load()
}

// Protocol returns the configured subscription protocol.
func (h *SubscriptionHandler) Protocol() string {
	return h.protocol
}

// SubscriptionByRoute manages per-route subscription handlers.
type SubscriptionByRoute struct {
	byroute.Manager[*SubscriptionHandler]
}

// NewSubscriptionByRoute creates a new per-route subscription handler manager.
func NewSubscriptionByRoute() *SubscriptionByRoute {
	return &SubscriptionByRoute{}
}

// AddRoute adds a subscription handler for a route.
func (m *SubscriptionByRoute) AddRoute(routeID string, cfg config.GraphQLSubscriptionConfig) {
	m.Add(routeID, New(cfg))
}

// GetHandler returns the subscription handler for a route.
func (m *SubscriptionByRoute) GetHandler(routeID string) *SubscriptionHandler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route subscription stats.
func (m *SubscriptionByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *SubscriptionHandler) interface{} {
		return map[string]interface{}{
			"protocol":        h.Protocol(),
			"active_conns":    h.ActiveConns(),
			"total_conns":     h.TotalConns(),
			"total_messages":  h.TotalMessages(),
			"max_connections": h.maxConnections,
		}
	})
}
