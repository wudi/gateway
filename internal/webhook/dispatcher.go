package webhook

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/example/gateway/internal/config"
)

// Dispatcher manages webhook event delivery to configured endpoints.
type Dispatcher struct {
	endpoints []config.WebhookEndpoint
	queue     chan *Event
	client    *http.Client
	retryCfg  config.WebhookRetryConfig
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	metrics   *Metrics
	mu        sync.RWMutex
	history   []Event
	queueSize int
}

// NewDispatcher creates a new webhook dispatcher and starts worker goroutines.
func NewDispatcher(cfg config.WebhooksConfig) *Dispatcher {
	workers := cfg.Workers
	if workers <= 0 {
		workers = 4
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = 1000
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	retryCfg := cfg.Retry
	if retryCfg.MaxRetries <= 0 {
		retryCfg.MaxRetries = 3
	}
	if retryCfg.Backoff <= 0 {
		retryCfg.Backoff = 1 * time.Second
	}
	if retryCfg.MaxBackoff <= 0 {
		retryCfg.MaxBackoff = 30 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	d := &Dispatcher{
		endpoints: cfg.Endpoints,
		queue:     make(chan *Event, queueSize),
		client: &http.Client{
			Timeout: timeout,
		},
		retryCfg:  retryCfg,
		ctx:       ctx,
		cancel:    cancel,
		metrics:   &Metrics{},
		queueSize: queueSize,
	}

	for i := 0; i < workers; i++ {
		d.wg.Add(1)
		go d.worker()
	}

	return d
}

// Emit sends an event to the dispatch queue. Non-blocking: if the queue is full,
// the event is dropped and the dropped counter incremented.
func (d *Dispatcher) Emit(event *Event) {
	d.metrics.TotalEmitted.Add(1)
	select {
	case d.queue <- event:
	default:
		d.metrics.TotalDropped.Add(1)
	}
}

// UpdateEndpoints replaces the endpoint list at runtime (e.g., on config reload).
func (d *Dispatcher) UpdateEndpoints(eps []config.WebhookEndpoint) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.endpoints = eps
}

// Close cancels the dispatcher context and waits for all workers to drain.
func (d *Dispatcher) Close() {
	d.cancel()
	d.wg.Wait()
}

// Stats returns a snapshot of dispatcher state and metrics.
func (d *Dispatcher) Stats() DispatcherStats {
	d.mu.RLock()
	endpoints := len(d.endpoints)
	historyCopy := make([]Event, len(d.history))
	copy(historyCopy, d.history)
	d.mu.RUnlock()

	return DispatcherStats{
		Enabled:      true,
		Endpoints:    endpoints,
		QueueSize:    d.queueSize,
		QueueUsed:    len(d.queue),
		Metrics:      d.metrics.Snapshot(),
		RecentEvents: historyCopy,
	}
}

// worker processes events from the queue.
func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for {
		select {
		case <-d.ctx.Done():
			return
		case event, ok := <-d.queue:
			if !ok {
				return
			}
			d.dispatch(event)
		}
	}
}

// dispatch delivers an event to all matching endpoints.
func (d *Dispatcher) dispatch(event *Event) {
	// Record in recent history
	d.mu.Lock()
	d.history = append(d.history, *event)
	if len(d.history) > 100 {
		d.history = d.history[len(d.history)-100:]
	}
	d.mu.Unlock()

	d.mu.RLock()
	endpoints := make([]config.WebhookEndpoint, len(d.endpoints))
	copy(endpoints, d.endpoints)
	d.mu.RUnlock()

	for _, ep := range endpoints {
		if !d.eventMatchesEndpoint(event, ep) {
			continue
		}
		d.deliverWithRetry(ep, event)
	}
}

// eventMatchesEndpoint checks if an event matches an endpoint's event and route filters.
func (d *Dispatcher) eventMatchesEndpoint(event *Event, ep config.WebhookEndpoint) bool {
	// Check event type filter
	matched := false
	for _, pattern := range ep.Events {
		if matchesPattern(event.Type, pattern) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}

	// Check route filter (empty = all routes)
	if len(ep.Routes) > 0 && event.RouteID != "" {
		routeMatched := false
		for _, r := range ep.Routes {
			if r == event.RouteID {
				routeMatched = true
				break
			}
		}
		if !routeMatched {
			return false
		}
	}

	return true
}

// deliverWithRetry attempts delivery with exponential backoff retries.
func (d *Dispatcher) deliverWithRetry(ep config.WebhookEndpoint, event *Event) {
	var err error
	for attempt := 0; attempt <= d.retryCfg.MaxRetries; attempt++ {
		if attempt > 0 {
			d.metrics.TotalRetries.Add(1)
			backoff := d.retryCfg.Backoff
			for i := 1; i < attempt; i++ {
				backoff *= 2
				if backoff > d.retryCfg.MaxBackoff {
					backoff = d.retryCfg.MaxBackoff
					break
				}
			}
			select {
			case <-d.ctx.Done():
				d.metrics.TotalFailed.Add(1)
				return
			case <-time.After(backoff):
			}
		}

		err = d.deliver(ep, event)
		if err == nil {
			d.metrics.TotalDelivered.Add(1)
			return
		}
	}

	d.metrics.TotalFailed.Add(1)
}
