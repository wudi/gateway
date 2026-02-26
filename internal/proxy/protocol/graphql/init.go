package graphql

import (
	"net/http"
	"sync"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/internal/proxy/protocol"
)

func init() {
	protocol.Register("rest_to_graphql", func() protocol.Translator {
		return &translator{
			handlers: make(map[string]*Handler),
			metrics:  make(map[string]*protocol.RouteMetrics),
		}
	})
}

// translator adapts GraphQL handlers to the protocol.Translator interface.
type translator struct {
	handlers map[string]*Handler
	metrics  map[string]*protocol.RouteMetrics
	mu       sync.RWMutex
}

func (t *translator) Name() string { return "rest_to_graphql" }

func (t *translator) Handler(routeID string, _ loadbalancer.Balancer, cfg config.ProtocolConfig) (http.Handler, error) {
	h, err := New(cfg.GraphQL, http.DefaultTransport)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	t.handlers[routeID] = h
	t.metrics[routeID] = &protocol.RouteMetrics{}
	t.mu.Unlock()

	return h, nil
}

func (t *translator) Close(routeID string) error {
	t.mu.Lock()
	delete(t.handlers, routeID)
	delete(t.metrics, routeID)
	t.mu.Unlock()
	return nil
}

func (t *translator) Metrics(routeID string) *protocol.TranslatorMetrics {
	t.mu.RLock()
	h, ok := t.handlers[routeID]
	t.mu.RUnlock()
	if !ok {
		return nil
	}
	stats := h.Stats()
	return &protocol.TranslatorMetrics{
		Requests:     stats["total_requests"].(int64),
		Failures:     stats["total_errors"].(int64),
		Successes:    stats["total_requests"].(int64) - stats["total_errors"].(int64),
		ProtocolType: "rest_to_graphql",
	}
}
