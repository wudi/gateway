package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"gocloud.dev/pubsub"
	_ "gocloud.dev/pubsub/mempubsub" // in-memory for testing

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// Handler handles Pub/Sub publish/subscribe operations as HTTP backend.
type Handler struct {
	publishURL      string
	subscriptionURL string
	publishTopic    *pubsub.Topic
	subscription    *pubsub.Subscription

	totalRequests atomic.Int64
	totalErrors   atomic.Int64
	published     atomic.Int64
	consumed      atomic.Int64
}

// New creates a PubSub handler from config.
func New(cfg config.PubSubConfig) (*Handler, error) {
	h := &Handler{
		publishURL:      cfg.PublishURL,
		subscriptionURL: cfg.SubscriptionURL,
	}

	ctx := context.Background()

	if cfg.PublishURL != "" {
		topic, err := pubsub.OpenTopic(ctx, cfg.PublishURL)
		if err != nil {
			return nil, fmt.Errorf("pubsub: open topic %s: %w", cfg.PublishURL, err)
		}
		h.publishTopic = topic
	}

	if cfg.SubscriptionURL != "" {
		sub, err := pubsub.OpenSubscription(ctx, cfg.SubscriptionURL)
		if err != nil {
			if h.publishTopic != nil {
				h.publishTopic.Shutdown(ctx)
			}
			return nil, fmt.Errorf("pubsub: open subscription %s: %w", cfg.SubscriptionURL, err)
		}
		h.subscription = sub
	}

	if h.publishTopic == nil && h.subscription == nil {
		return nil, fmt.Errorf("pubsub: at least one of publish_url or subscription_url is required")
	}

	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.totalRequests.Add(1)

	switch r.Method {
	case http.MethodPost, http.MethodPut:
		h.handlePublish(w, r)
	default:
		h.handleSubscribe(w, r)
	}
}

func (h *Handler) handlePublish(w http.ResponseWriter, r *http.Request) {
	if h.publishTopic == nil {
		h.totalErrors.Add(1)
		http.Error(w, "pubsub: publishing not configured", http.StatusBadGateway)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "pubsub: read body failed", http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	err = h.publishTopic.Send(ctx, &pubsub.Message{Body: body})
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "pubsub: publish failed", http.StatusBadGateway)
		return
	}

	h.published.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "published",
	})
}

func (h *Handler) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if h.subscription == nil {
		h.totalErrors.Add(1)
		http.Error(w, "pubsub: subscription not configured", http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	msg, err := h.subscription.Receive(ctx)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "pubsub: receive failed", http.StatusBadGateway)
		return
	}
	msg.Ack()

	h.consumed.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(msg.Body)
}

// Close shuts down the handler.
func (h *Handler) Close() error {
	ctx := context.Background()
	if h.publishTopic != nil {
		h.publishTopic.Shutdown(ctx)
	}
	if h.subscription != nil {
		h.subscription.Shutdown(ctx)
	}
	return nil
}

// Stats returns handler stats.
func (h *Handler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"publish_url":      h.publishURL,
		"subscription_url": h.subscriptionURL,
		"total_requests":   h.totalRequests.Load(),
		"total_errors":     h.totalErrors.Load(),
		"published":        h.published.Load(),
		"consumed":         h.consumed.Load(),
	}
}

// PubSubByRoute manages per-route PubSub handlers.
type PubSubByRoute = byroute.Factory[*Handler, config.PubSubConfig]

func NewPubSubByRoute() *PubSubByRoute {
	return byroute.NewFactory(New, func(h *Handler) any { return h.Stats() })
}
