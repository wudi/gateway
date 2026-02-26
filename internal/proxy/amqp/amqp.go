package amqp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// Handler handles AMQP publish/consume operations as HTTP backend.
type Handler struct {
	url      string
	consumer config.AMQPConsumerConfig
	producer config.AMQPProducerConfig
	conn     *amqp091.Connection
	ch       *amqp091.Channel
	mu       sync.RWMutex

	totalRequests atomic.Int64
	totalErrors   atomic.Int64
	published     atomic.Int64
	consumed      atomic.Int64
}

// New creates an AMQP handler from config.
func New(cfg config.AMQPConfig) (*Handler, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("amqp: url is required")
	}

	conn, err := amqp091.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("amqp: connect failed: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("amqp: channel failed: %w", err)
	}

	return &Handler{
		url:      cfg.URL,
		consumer: cfg.Consumer,
		producer: cfg.Producer,
		conn:     conn,
		ch:       ch,
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.totalRequests.Add(1)

	switch r.Method {
	case http.MethodPost, http.MethodPut:
		h.handlePublish(w, r)
	default:
		h.handleConsume(w, r)
	}
}

func (h *Handler) handlePublish(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "amqp: read body failed", http.StatusBadGateway)
		return
	}

	h.mu.RLock()
	ch := h.ch
	h.mu.RUnlock()

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	err = ch.PublishWithContext(ctx,
		h.producer.Exchange,
		h.producer.RoutingKey,
		false, false,
		amqp091.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "amqp: publish failed", http.StatusBadGateway)
		return
	}

	h.published.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "published",
		"exchange":    h.producer.Exchange,
		"routing_key": h.producer.RoutingKey,
	})
}

func (h *Handler) handleConsume(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	ch := h.ch
	h.mu.RUnlock()

	msg, ok, err := ch.Get(h.consumer.Queue, h.consumer.AutoAck)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "amqp: consume failed", http.StatusBadGateway)
		return
	}

	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.consumed.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(msg.Body)
}

// Close shuts down the AMQP connection.
func (h *Handler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ch != nil {
		h.ch.Close()
	}
	if h.conn != nil {
		return h.conn.Close()
	}
	return nil
}

// Stats returns handler stats.
func (h *Handler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"url":            h.url,
		"total_requests": h.totalRequests.Load(),
		"total_errors":   h.totalErrors.Load(),
		"published":      h.published.Load(),
		"consumed":       h.consumed.Load(),
	}
}

// AMQPByRoute manages per-route AMQP handlers.
type AMQPByRoute struct {
	byroute.Manager[*Handler]
}

func NewAMQPByRoute() *AMQPByRoute {
	return &AMQPByRoute{}
}

func (m *AMQPByRoute) AddRoute(routeID string, cfg config.AMQPConfig) error {
	h, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, h)
	return nil
}

func (m *AMQPByRoute) GetHandler(routeID string) *Handler {
	v, _ := m.Get(routeID)
	return v
}

func (m *AMQPByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *Handler) interface{} { return h.Stats() })
}
