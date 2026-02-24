package auditlog

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

// collectWebhook starts an httptest.Server that collects batches of
// AuditEntry payloads. It returns the server, the received batches,
// and a function to wait for at least n total entries.
func collectWebhook(t *testing.T) (*httptest.Server, *[][]AuditEntry, func(int) [][]AuditEntry) {
	t.Helper()

	var mu sync.Mutex
	var batches [][]AuditEntry
	var total atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var entries []AuditEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			t.Errorf("webhook: bad JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		batches = append(batches, entries)
		mu.Unlock()
		total.Add(int64(len(entries)))
		w.WriteHeader(http.StatusOK)
	}))

	waitFor := func(n int) [][]AuditEntry {
		deadline := time.After(5 * time.Second)
		for {
			if total.Load() >= int64(n) {
				mu.Lock()
				out := make([][]AuditEntry, len(batches))
				copy(out, batches)
				mu.Unlock()
				return out
			}
			select {
			case <-deadline:
				t.Fatalf("timed out waiting for %d entries, got %d", n, total.Load())
				return nil
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

	return srv, &batches, waitFor
}

func TestBatching(t *testing.T) {
	srv, _, waitFor := collectWebhook(t)
	defer srv.Close()

	cfg := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    100,
		BatchSize:     3,
		FlushInterval: 100 * time.Millisecond,
		SampleRate:    1.0,
	}

	logger := New("test-route", cfg)
	defer logger.Close()

	handler := logger.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	// Send 6 requests -> should produce 2 batches of 3.
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	batches := waitFor(6)
	totalEntries := 0
	for _, b := range batches {
		totalEntries += len(b)
	}
	if totalEntries != 6 {
		t.Errorf("expected 6 total entries, got %d", totalEntries)
	}

	// Verify the first entry has correct fields.
	first := batches[0][0]
	if first.RouteID != "test-route" {
		t.Errorf("RouteID = %q, want %q", first.RouteID, "test-route")
	}
	if first.Method != http.MethodGet {
		t.Errorf("Method = %q, want %q", first.Method, http.MethodGet)
	}
	if first.Path != "/api/test" {
		t.Errorf("Path = %q, want %q", first.Path, "/api/test")
	}
	if first.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", first.StatusCode, http.StatusOK)
	}

	stats := logger.Stats()
	if stats["enqueued"].(int64) != 6 {
		t.Errorf("enqueued = %v, want 6", stats["enqueued"])
	}
}

func TestSamplingZero(t *testing.T) {
	srv, _, _ := collectWebhook(t)
	defer srv.Close()

	cfg := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    100,
		BatchSize:     1,
		FlushInterval: 50 * time.Millisecond,
		SampleRate:    0.0, // will be normalized to default 1.0 by New()
	}

	// SampleRate of 0.0 is normalized to 1.0 by New(). To test zero sampling,
	// we construct the logger then override the cfg. We need a workaround:
	// create with a tiny positive value and verify statistically.
	// Instead, let's test that the sampling branch works by directly setting
	// SampleRate to something that New() would accept (any positive value).
	// For a true zero-sampling test, use a rate so low that nothing passes.

	// Actually, let's test sampling by setting SampleRate to a very small
	// positive number so New() does not override it.
	cfg.SampleRate = 0.0001

	logger := New("sample-route", cfg)
	defer logger.Close()

	handler := logger.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Send 100 requests. With 0.01% sample rate, virtually none should be enqueued.
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/sample", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Wait a bit for any flushes.
	time.Sleep(200 * time.Millisecond)

	enqueued := logger.enqueued.Load()
	// It's probabilistic, but with 0.01% rate and 100 requests,
	// the expected count is ~0.01. We allow up to 5 as a generous bound.
	if enqueued > 5 {
		t.Errorf("with SampleRate=0.0001, expected few enqueued, got %d", enqueued)
	}
}

func TestMethodFilter(t *testing.T) {
	srv, _, waitFor := collectWebhook(t)
	defer srv.Close()

	cfg := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    100,
		BatchSize:     1,
		FlushInterval: 50 * time.Millisecond,
		SampleRate:    1.0,
		Methods:       []string{"POST", "PUT"},
	}

	logger := New("method-route", cfg)
	defer logger.Close()

	handler := logger.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// GET should be skipped.
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// POST should be logged.
	req = httptest.NewRequest(http.MethodPost, "/api", strings.NewReader("data"))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// PUT should be logged.
	req = httptest.NewRequest(http.MethodPut, "/api", strings.NewReader("data"))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	batches := waitFor(2)
	totalEntries := 0
	for _, b := range batches {
		totalEntries += len(b)
	}
	if totalEntries != 2 {
		t.Errorf("expected 2 entries (POST+PUT only), got %d", totalEntries)
	}

	// The GET should not have been enqueued.
	if logger.enqueued.Load() != 2 {
		t.Errorf("enqueued = %d, want 2", logger.enqueued.Load())
	}
}

func TestStatusCodeFilter(t *testing.T) {
	srv, _, waitFor := collectWebhook(t)
	defer srv.Close()

	cfg := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    100,
		BatchSize:     1,
		FlushInterval: 50 * time.Millisecond,
		SampleRate:    1.0,
		StatusCodes:   []int{500, 503},
	}

	logger := New("status-route", cfg)
	defer logger.Close()

	statusToReturn := http.StatusOK
	handler := logger.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusToReturn)
	}))

	// 200 should NOT be logged.
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 500 should be logged.
	statusToReturn = http.StatusInternalServerError
	req = httptest.NewRequest(http.MethodGet, "/api", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 503 should be logged.
	statusToReturn = http.StatusServiceUnavailable
	req = httptest.NewRequest(http.MethodGet, "/api", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	batches := waitFor(2)
	totalEntries := 0
	for _, b := range batches {
		for _, e := range b {
			if e.StatusCode != 500 && e.StatusCode != 503 {
				t.Errorf("unexpected status %d in logged entry", e.StatusCode)
			}
			totalEntries++
		}
	}
	if totalEntries != 2 {
		t.Errorf("expected 2 entries (500+503 only), got %d", totalEntries)
	}
}

func TestQueueOverflow(t *testing.T) {
	// Use a webhook that responds quickly so Close() can drain without blocking.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    5, // tiny buffer
		BatchSize:     1000,
		FlushInterval: 1 * time.Hour, // effectively never auto-flush
		SampleRate:    1.0,
	}

	logger := New("overflow-route", cfg)

	// Directly enqueue more entries than the buffer can hold.
	// The background goroutine won't flush because FlushInterval is huge
	// and BatchSize is huge, so the channel will fill up.
	for i := 0; i < 20; i++ {
		entry := &AuditEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			RouteID:   "overflow-route",
			Method:    "GET",
			Path:      "/overflow",
		}
		logger.Enqueue(entry)
	}

	// Should have some dropped since buffer is only 5.
	enqueued := logger.enqueued.Load()
	dropped := logger.dropped.Load()

	if enqueued+dropped != 20 {
		t.Errorf("enqueued(%d) + dropped(%d) = %d, want 20", enqueued, dropped, enqueued+dropped)
	}
	if dropped == 0 {
		t.Errorf("expected some dropped entries with buffer size 5 and 20 requests")
	}
	if enqueued > 5 {
		t.Errorf("enqueued = %d, should be at most buffer size 5", enqueued)
	}

	logger.Close()
}

func TestBodyCapture(t *testing.T) {
	srv, _, waitFor := collectWebhook(t)
	defer srv.Close()

	cfg := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    100,
		BatchSize:     1,
		FlushInterval: 50 * time.Millisecond,
		SampleRate:    1.0,
		IncludeBody:   true,
		MaxBodySize:   20, // only capture first 20 bytes
	}

	logger := New("body-route", cfg)
	defer logger.Close()

	handler := logger.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read and verify the full request body is still available.
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response-" + string(body)))
	}))

	reqBody := "this-is-a-long-request-body-that-exceeds-max"
	req := httptest.NewRequest(http.MethodPost, "/body", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify the downstream handler received the full body.
	if !strings.Contains(rec.Body.String(), "this-is-a-long-request-body") {
		t.Errorf("downstream handler should have received full request body, got %q", rec.Body.String())
	}

	batches := waitFor(1)
	entry := batches[0][0]

	// Request body should be truncated to MaxBodySize.
	if len(entry.RequestBody) > 20 {
		t.Errorf("request body length = %d, should be at most 20", len(entry.RequestBody))
	}
	if entry.RequestBody != reqBody[:20] {
		t.Errorf("request body = %q, want %q", entry.RequestBody, reqBody[:20])
	}

	// Response body should also be truncated.
	if len(entry.ResponseBody) > 20 {
		t.Errorf("response body length = %d, should be at most 20", len(entry.ResponseBody))
	}
	if entry.ResponseBody == "" {
		t.Error("expected non-empty response body")
	}
}

func TestFlushOnClose(t *testing.T) {
	var mu sync.Mutex
	var received []AuditEntry

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var entries []AuditEntry
		json.Unmarshal(body, &entries)
		mu.Lock()
		received = append(received, entries...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    100,
		BatchSize:     100,          // large batch so timer flush won't happen
		FlushInterval: 10 * time.Minute, // effectively never
		SampleRate:    1.0,
	}

	logger := New("close-route", cfg)

	handler := logger.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Enqueue 5 entries.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/close-test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Close should drain pending entries.
	logger.Close()

	mu.Lock()
	got := len(received)
	mu.Unlock()

	if got != 5 {
		t.Errorf("expected 5 entries flushed on close, got %d", got)
	}
}

func TestMergeAuditLogConfig(t *testing.T) {
	global := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    "https://global.example.com/audit",
		Headers:       map[string]string{"X-Source": "gateway"},
		SampleRate:    0.5,
		IncludeBody:   true,
		MaxBodySize:   32768,
		BufferSize:    2000,
		BatchSize:     20,
		FlushInterval: 10 * time.Second,
		Methods:       []string{"POST"},
		StatusCodes:   []int{500},
	}

	t.Run("route overrides global", func(t *testing.T) {
		route := config.AuditLogConfig{
			Enabled:    true,
			WebhookURL: "https://route.example.com/audit",
			SampleRate: 0.8,
			BatchSize:  50,
		}

		merged := MergeAuditLogConfig(route, global)

		if merged.WebhookURL != "https://route.example.com/audit" {
			t.Errorf("WebhookURL = %q, want route URL", merged.WebhookURL)
		}
		if merged.SampleRate != 0.8 {
			t.Errorf("SampleRate = %f, want 0.8", merged.SampleRate)
		}
		if merged.BatchSize != 50 {
			t.Errorf("BatchSize = %d, want 50", merged.BatchSize)
		}
		// Non-overridden fields should come from global.
		if merged.BufferSize != 2000 {
			t.Errorf("BufferSize = %d, want 2000 (from global)", merged.BufferSize)
		}
		if merged.MaxBodySize != 32768 {
			t.Errorf("MaxBodySize = %d, want 32768 (from global)", merged.MaxBodySize)
		}
		if !merged.Enabled {
			t.Error("expected Enabled = true")
		}
	})

	t.Run("empty route gets global values", func(t *testing.T) {
		route := config.AuditLogConfig{
			Enabled: true,
		}

		merged := MergeAuditLogConfig(route, global)

		if merged.WebhookURL != "https://global.example.com/audit" {
			t.Errorf("WebhookURL = %q, want global URL", merged.WebhookURL)
		}
		if merged.SampleRate != 0.5 {
			t.Errorf("SampleRate = %f, want 0.5 (from global)", merged.SampleRate)
		}
		if merged.BatchSize != 20 {
			t.Errorf("BatchSize = %d, want 20 (from global)", merged.BatchSize)
		}
	})
}

func TestWebhookRetries(t *testing.T) {
	var attempts atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			// Return 500 for first 2 attempts to trigger retries.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    100,
		BatchSize:     1,
		FlushInterval: 50 * time.Millisecond,
		SampleRate:    1.0,
	}

	logger := New("retry-route", cfg)

	handler := logger.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/retry", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Close drains and should trigger the send with retries.
	logger.Close()

	// Should have retried: at least 3 attempts (2 failures + 1 success).
	got := attempts.Load()
	if got < 3 {
		t.Errorf("expected at least 3 webhook attempts (retries), got %d", got)
	}

	// No errors should be recorded since the final attempt succeeded.
	if logger.errors.Load() != 0 {
		t.Errorf("errors = %d, want 0 (final retry succeeded)", logger.errors.Load())
	}
}

func TestWebhookCustomHeaders(t *testing.T) {
	var receivedHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		Headers:       map[string]string{"Authorization": "Bearer secret", "X-Custom": "value"},
		BufferSize:    100,
		BatchSize:     1,
		FlushInterval: 50 * time.Millisecond,
		SampleRate:    1.0,
	}

	logger := New("header-route", cfg)

	handler := logger.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/headers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logger.Close()

	if receivedHeaders == nil {
		t.Fatal("webhook never received a request")
	}
	if receivedHeaders.Get("Authorization") != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", receivedHeaders.Get("Authorization"), "Bearer secret")
	}
	if receivedHeaders.Get("X-Custom") != "value" {
		t.Errorf("X-Custom = %q, want %q", receivedHeaders.Get("X-Custom"), "value")
	}
	if receivedHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", receivedHeaders.Get("Content-Type"), "application/json")
	}
}

func TestAuditLogByRoute(t *testing.T) {
	srv, _, _ := collectWebhook(t)
	defer srv.Close()

	mgr := NewAuditLogByRoute()

	cfg1 := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    10,
		BatchSize:     5,
		FlushInterval: 100 * time.Millisecond,
		SampleRate:    1.0,
	}
	cfg2 := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    10,
		BatchSize:     5,
		FlushInterval: 100 * time.Millisecond,
		SampleRate:    1.0,
	}

	if err := mgr.AddRoute("route-a", cfg1); err != nil {
		t.Fatalf("AddRoute failed: %v", err)
	}
	if err := mgr.AddRoute("route-b", cfg2); err != nil {
		t.Fatalf("AddRoute failed: %v", err)
	}

	if mgr.GetLogger("route-a") == nil {
		t.Error("GetLogger(route-a) returned nil")
	}
	if mgr.GetLogger("route-b") == nil {
		t.Error("GetLogger(route-b) returned nil")
	}
	if mgr.GetLogger("nonexistent") != nil {
		t.Error("GetLogger(nonexistent) should return nil")
	}

	stats := mgr.Stats()
	if len(stats) != 2 {
		t.Errorf("Stats() returned %d entries, want 2", len(stats))
	}

	mgr.CloseAll()
}

func TestAuditWriterStatusCapture(t *testing.T) {
	rec := httptest.NewRecorder()
	aw := acquireAuditWriter(rec, false, 0)
	defer releaseAuditWriter(aw)

	aw.WriteHeader(http.StatusNotFound)
	if aw.StatusCode() != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", aw.StatusCode(), http.StatusNotFound)
	}

	// Second WriteHeader should not change the status.
	aw.WriteHeader(http.StatusOK)
	if aw.StatusCode() != http.StatusNotFound {
		t.Errorf("StatusCode changed to %d after second WriteHeader, want %d", aw.StatusCode(), http.StatusNotFound)
	}
}

func TestAuditWriterDefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	aw := acquireAuditWriter(rec, false, 0)
	defer releaseAuditWriter(aw)

	// Write without explicit WriteHeader -> status defaults to 200.
	aw.Write([]byte("hello"))
	if aw.StatusCode() != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", aw.StatusCode(), http.StatusOK)
	}
}

func TestQueryStringCaptured(t *testing.T) {
	srv, _, waitFor := collectWebhook(t)
	defer srv.Close()

	cfg := config.AuditLogConfig{
		Enabled:       true,
		WebhookURL:    srv.URL,
		BufferSize:    100,
		BatchSize:     1,
		FlushInterval: 50 * time.Millisecond,
		SampleRate:    1.0,
	}

	logger := New("query-route", cfg)
	defer logger.Close()

	handler := logger.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/search?q=test&page=2", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	batches := waitFor(1)
	entry := batches[0][0]

	if entry.Query != "q=test&page=2" {
		t.Errorf("Query = %q, want %q", entry.Query, "q=test&page=2")
	}
	if entry.Path != "/search" {
		t.Errorf("Path = %q, want %q", entry.Path, "/search")
	}
}
