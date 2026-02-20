package soap

import (
	"net/http"
	"sync"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/proxy/protocol"
)

func init() {
	protocol.Register("rest_to_soap", func() protocol.Translator {
		return &translator{
			handlers: make(map[string]*Handler),
		}
	})
}

// translator adapts SOAP handlers to the protocol.Translator interface.
type translator struct {
	handlers map[string]*Handler
	mu       sync.RWMutex
}

func (t *translator) Name() string { return "rest_to_soap" }

func (t *translator) Handler(routeID string, _ loadbalancer.Balancer, cfg config.ProtocolConfig) (http.Handler, error) {
	h, err := New(cfg.SOAP, http.DefaultTransport)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	t.handlers[routeID] = h
	t.mu.Unlock()

	return h, nil
}

func (t *translator) Close(routeID string) error {
	t.mu.Lock()
	delete(t.handlers, routeID)
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
		ProtocolType: "rest_to_soap",
	}
}
