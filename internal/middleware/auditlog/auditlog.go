package auditlog

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/variables"
)

// AuditEntry represents a single audit log event.
type AuditEntry struct {
	Timestamp    string        `json:"timestamp"`
	RequestID    string        `json:"request_id"`
	RouteID      string        `json:"route_id"`
	Method       string        `json:"method"`
	Path         string        `json:"path"`
	Query        string        `json:"query,omitempty"`
	ClientIP     string        `json:"client_ip"`
	StatusCode   int           `json:"status_code"`
	Duration     time.Duration `json:"duration_ns"`
	DurationMS   float64       `json:"duration_ms"`
	RequestBody  string        `json:"request_body,omitempty"`
	ResponseBody string        `json:"response_body,omitempty"`
}

// AuditLogger manages async audit log delivery for a single route.
type AuditLogger struct {
	cfg        config.AuditLogConfig
	routeID    string
	queue      chan *AuditEntry
	httpClient *http.Client
	methodSet  map[string]struct{}
	statusSet  map[int]struct{}

	// Stats counters.
	enqueued atomic.Int64
	dropped  atomic.Int64
	flushed  atomic.Int64
	errors   atomic.Int64

	stopCh chan struct{}
	doneCh chan struct{}
}

// New creates a new AuditLogger and starts the background flush goroutine.
func New(routeID string, cfg config.AuditLogConfig) *AuditLogger {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 1000
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 5 * time.Second
	}
	if cfg.MaxBodySize <= 0 {
		cfg.MaxBodySize = 65536
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 1.0
	}
	if cfg.SampleRate > 1.0 {
		cfg.SampleRate = 1.0
	}

	al := &AuditLogger{
		cfg:        cfg,
		routeID:    routeID,
		queue:      make(chan *AuditEntry, cfg.BufferSize),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}

	// Build method filter set.
	if len(cfg.Methods) > 0 {
		al.methodSet = make(map[string]struct{}, len(cfg.Methods))
		for _, m := range cfg.Methods {
			al.methodSet[m] = struct{}{}
		}
	}

	// Build status code filter set.
	if len(cfg.StatusCodes) > 0 {
		al.statusSet = make(map[int]struct{}, len(cfg.StatusCodes))
		for _, s := range cfg.StatusCodes {
			al.statusSet[s] = struct{}{}
		}
	}

	go al.flushLoop()
	return al
}

// Enqueue adds an entry to the queue. It is non-blocking; if the queue
// is full the entry is dropped and the drop counter is incremented.
func (al *AuditLogger) Enqueue(entry *AuditEntry) {
	select {
	case al.queue <- entry:
		al.enqueued.Add(1)
	default:
		al.dropped.Add(1)
	}
}

// Close signals the background goroutine to drain remaining entries and stop.
func (al *AuditLogger) Close() {
	close(al.stopCh)
	<-al.doneCh
}

// Stats returns snapshot counters for this logger.
func (al *AuditLogger) Stats() map[string]interface{} {
	return map[string]interface{}{
		"route_id":    al.routeID,
		"webhook_url": al.cfg.WebhookURL,
		"enqueued":    al.enqueued.Load(),
		"dropped":     al.dropped.Load(),
		"flushed":     al.flushed.Load(),
		"errors":      al.errors.Load(),
		"queue_len":   len(al.queue),
	}
}

// Middleware returns a middleware.Middleware that creates audit entries.
func (al *AuditLogger) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Method filter.
			if al.methodSet != nil {
				if _, ok := al.methodSet[r.Method]; !ok {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Sampling.
			if al.cfg.SampleRate < 1.0 {
				if rand.Float64() >= al.cfg.SampleRate { //nolint:gosec
					next.ServeHTTP(w, r)
					return
				}
			}

			start := time.Now()

			// Optionally capture request body.
			var reqBody string
			if al.cfg.IncludeBody && r.Body != nil {
				limited := io.LimitReader(r.Body, int64(al.cfg.MaxBodySize))
				bodyBytes, err := io.ReadAll(limited)
				if err == nil && len(bodyBytes) > 0 {
					reqBody = string(bodyBytes)
				}
				// Reassemble the body so downstream handlers can still read it.
				r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(bodyBytes), r.Body))
			}

			// Wrap ResponseWriter to capture status and optional body.
			aw := acquireAuditWriter(w, al.cfg.IncludeBody, al.cfg.MaxBodySize)
			defer releaseAuditWriter(aw)

			next.ServeHTTP(aw, r)

			// Status code filter (post-execution).
			if al.statusSet != nil {
				if _, ok := al.statusSet[aw.statusCode]; !ok {
					return
				}
			}

			duration := time.Since(start)

			// Extract request ID and client IP.
			requestID := middleware.RequestIDFromContext(r.Context())
			if requestID == "" {
				requestID = middleware.GetRequestID(r)
			}
			clientIP := variables.ExtractClientIP(r)

			entry := &AuditEntry{
				Timestamp:  start.UTC().Format(time.RFC3339Nano),
				RequestID:  requestID,
				RouteID:    al.routeID,
				Method:     r.Method,
				Path:       r.URL.Path,
				Query:      r.URL.RawQuery,
				ClientIP:   clientIP,
				StatusCode: aw.statusCode,
				Duration:   duration,
				DurationMS: float64(duration.Nanoseconds()) / 1e6,
				RequestBody: reqBody,
			}

			if al.cfg.IncludeBody {
				entry.ResponseBody = aw.body.String()
			}

			al.Enqueue(entry)
		})
	}
}

// flushLoop runs in a background goroutine, batching entries and flushing
// to the webhook URL.
func (al *AuditLogger) flushLoop() {
	defer close(al.doneCh)

	ticker := time.NewTicker(al.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([]*AuditEntry, 0, al.cfg.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		al.send(batch)
		al.flushed.Add(int64(len(batch)))
		batch = make([]*AuditEntry, 0, al.cfg.BatchSize)
	}

	for {
		select {
		case entry := <-al.queue:
			batch = append(batch, entry)
			if len(batch) >= al.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-al.stopCh:
			// Drain remaining entries from the queue.
			for {
				select {
				case entry := <-al.queue:
					batch = append(batch, entry)
					if len(batch) >= al.cfg.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// send POSTs the batch as a JSON array to the webhook URL with retries.
func (al *AuditLogger) send(batch []*AuditEntry) {
	body, err := json.Marshal(batch)
	if err != nil {
		al.errors.Add(1)
		return
	}

	backoff := time.Second
	const maxRetries = 3

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff)
			backoff *= 2
		}

		req, err := http.NewRequest(http.MethodPost, al.cfg.WebhookURL, bytes.NewReader(body))
		if err != nil {
			al.errors.Add(1)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range al.cfg.Headers {
			req.Header.Set(k, v)
		}

		resp, err := al.httpClient.Do(req)
		if err != nil {
			if attempt == maxRetries {
				al.errors.Add(1)
			}
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return // success
		}
		if resp.StatusCode >= 500 {
			// Retryable server error.
			if attempt == maxRetries {
				al.errors.Add(1)
			}
			continue
		}
		// Non-retryable client error (4xx).
		al.errors.Add(1)
		return
	}
}

// ---------------------------------------------------------------------------
// auditWriter captures status code and optionally response body.
// ---------------------------------------------------------------------------

type auditWriter struct {
	http.ResponseWriter
	statusCode  int
	captureBody bool
	maxBody     int
	body        bytes.Buffer
	wroteHeader bool
}

var auditWriterPool = sync.Pool{
	New: func() any { return &auditWriter{} },
}

func acquireAuditWriter(w http.ResponseWriter, captureBody bool, maxBody int) *auditWriter {
	aw := auditWriterPool.Get().(*auditWriter)
	aw.ResponseWriter = w
	aw.statusCode = http.StatusOK
	aw.captureBody = captureBody
	aw.maxBody = maxBody
	aw.body.Reset()
	aw.wroteHeader = false
	return aw
}

func releaseAuditWriter(aw *auditWriter) {
	aw.ResponseWriter = nil
	aw.body.Reset()
	auditWriterPool.Put(aw)
}

func (aw *auditWriter) WriteHeader(code int) {
	if !aw.wroteHeader {
		aw.statusCode = code
		aw.wroteHeader = true
	}
	aw.ResponseWriter.WriteHeader(code)
}

func (aw *auditWriter) Write(b []byte) (int, error) {
	if !aw.wroteHeader {
		aw.wroteHeader = true
	}
	if aw.captureBody {
		remaining := aw.maxBody - aw.body.Len()
		if remaining > 0 {
			toWrite := b
			if len(toWrite) > remaining {
				toWrite = toWrite[:remaining]
			}
			aw.body.Write(toWrite)
		}
	}
	return aw.ResponseWriter.Write(b)
}

// StatusCode returns the captured HTTP status code.
func (aw *auditWriter) StatusCode() int {
	return aw.statusCode
}

// Flush implements http.Flusher.
func (aw *auditWriter) Flush() {
	if f, ok := aw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter for http.ResponseController.
func (aw *auditWriter) Unwrap() http.ResponseWriter {
	return aw.ResponseWriter
}

// ---------------------------------------------------------------------------
// MergeAuditLogConfig merges per-route config over global defaults.
// ---------------------------------------------------------------------------

// MergeAuditLogConfig merges route-level audit log config over global defaults.
// Route fields override when non-zero; WebhookURL from route wins if set.
func MergeAuditLogConfig(route, global config.AuditLogConfig) config.AuditLogConfig {
	merged := config.MergeNonZero(global, route)
	merged.Enabled = true
	return merged
}

// ---------------------------------------------------------------------------
// AuditLogByRoute manages per-route AuditLoggers.
// ---------------------------------------------------------------------------

// AuditLogByRoute manages per-route audit loggers.
type AuditLogByRoute struct {
	byroute.Manager[*AuditLogger]
}

// NewAuditLogByRoute creates a new per-route audit log manager.
func NewAuditLogByRoute() *AuditLogByRoute {
	return &AuditLogByRoute{}
}

// AddRoute creates and registers an AuditLogger for the given route.
func (m *AuditLogByRoute) AddRoute(routeID string, cfg config.AuditLogConfig) error {
	logger := New(routeID, cfg)
	m.Add(routeID, logger)
	return nil
}

// GetLogger returns the AuditLogger for the given route, or nil.
func (m *AuditLogByRoute) GetLogger(routeID string) *AuditLogger {
	v, _ := m.Get(routeID)
	return v
}

// CloseAll closes all registered loggers, draining their queues.
func (m *AuditLogByRoute) CloseAll() {
	m.Range(func(_ string, al *AuditLogger) bool {
		al.Close()
		return true
	})
}

// Stats returns per-route audit log statistics.
func (m *AuditLogByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(al *AuditLogger) interface{} {
		return al.Stats()
	})
}
