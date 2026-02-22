package trafficreplay

import (
	"bytes"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/config"
)

// RecordedRequest holds a snapshot of an HTTP request.
type RecordedRequest struct {
	Method    string      `json:"method"`
	URL       string      `json:"url"`
	Headers   http.Header `json:"headers"`
	Body      []byte      `json:"body,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// Recorder captures incoming requests into a ring buffer for later replay.
type Recorder struct {
	mu          sync.Mutex
	buffer      []RecordedRequest
	writeIdx    int
	count       int
	maxSize     int
	maxBodySize int64
	percentage  int
	conditions  *Conditions
	recording   atomic.Bool
	replay      *replayState
}

// New creates a new Recorder from config.
func New(cfg config.TrafficReplayConfig) (*Recorder, error) {
	maxRecordings := cfg.MaxRecordings
	if maxRecordings <= 0 {
		maxRecordings = 10000
	}
	maxBodySize := cfg.MaxBodySize
	if maxBodySize <= 0 {
		maxBodySize = 65536
	}
	percentage := cfg.Percentage
	if percentage <= 0 {
		percentage = 100
	}

	var cond *Conditions
	if len(cfg.Conditions.Methods) > 0 || cfg.Conditions.PathRegex != "" {
		var err error
		cond, err = NewConditions(cfg.Conditions)
		if err != nil {
			return nil, err
		}
	}

	return &Recorder{
		buffer:      make([]RecordedRequest, maxRecordings),
		maxSize:     maxRecordings,
		maxBodySize: maxBodySize,
		percentage:  percentage,
		conditions:  cond,
	}, nil
}

// StartRecording enables request recording.
func (rec *Recorder) StartRecording() {
	rec.recording.Store(true)
}

// StopRecording disables request recording.
func (rec *Recorder) StopRecording() {
	rec.recording.Store(false)
}

// IsRecording returns true if recording is active.
func (rec *Recorder) IsRecording() bool {
	return rec.recording.Load()
}

// ClearRecordings resets the ring buffer.
func (rec *Recorder) ClearRecordings() {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.buffer = make([]RecordedRequest, rec.maxSize)
	rec.writeIdx = 0
	rec.count = 0
}

// GetRecordings returns a snapshot copy of all recorded requests.
func (rec *Recorder) GetRecordings() []RecordedRequest {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	n := rec.count
	if n > rec.maxSize {
		n = rec.maxSize
	}

	result := make([]RecordedRequest, n)
	if rec.count <= rec.maxSize {
		copy(result, rec.buffer[:n])
	} else {
		// Ring buffer has wrapped; read from writeIdx to end, then start to writeIdx
		start := rec.writeIdx
		firstPart := rec.maxSize - start
		copy(result, rec.buffer[start:])
		copy(result[firstPart:], rec.buffer[:start])
	}
	return result
}

// Snapshot returns the current recording state and stats.
func (rec *Recorder) Snapshot() map[string]interface{} {
	rec.mu.Lock()
	n := rec.count
	if n > rec.maxSize {
		n = rec.maxSize
	}
	rec.mu.Unlock()

	stats := map[string]interface{}{
		"recording":   rec.recording.Load(),
		"buffer_size": rec.maxSize,
		"buffer_used": n,
		"total_count": rec.count,
	}

	if rs := rec.getReplayState(); rs != nil {
		rs.mu.Lock()
		stats["replay"] = map[string]interface{}{
			"started":   rs.stats.Started,
			"total":     rs.stats.Total,
			"sent":      atomic.LoadInt64(&rs.stats.Sent),
			"errors":    atomic.LoadInt64(&rs.stats.Errors),
			"completed": rs.stats.Completed,
		}
		rs.mu.Unlock()
	}

	return stats
}

// RecordingMiddleware returns middleware that records matching requests.
func (rec *Recorder) RecordingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rec.recording.Load() && rec.shouldRecord(r) {
				rec.recordRequest(r)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (rec *Recorder) shouldRecord(r *http.Request) bool {
	if rec.percentage < 100 {
		if rand.IntN(100) >= rec.percentage {
			return false
		}
	}
	if rec.conditions != nil && !rec.conditions.Match(r) {
		return false
	}
	return true
}

func (rec *Recorder) recordRequest(r *http.Request) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, rec.maxBodySize))
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	headers := make(http.Header, len(r.Header))
	for k, v := range r.Header {
		headers[k] = append([]string(nil), v...)
	}

	recorded := RecordedRequest{
		Method:    r.Method,
		URL:       r.URL.RequestURI(),
		Headers:   headers,
		Body:      body,
		Timestamp: time.Now(),
	}

	rec.mu.Lock()
	rec.buffer[rec.writeIdx] = recorded
	rec.writeIdx = (rec.writeIdx + 1) % rec.maxSize
	rec.count++
	rec.mu.Unlock()
}

// StartReplay snapshots the ring buffer and launches a replay against the target.
func (rec *Recorder) StartReplay(cfg ReplayConfig) error {
	recordings := rec.GetRecordings()
	if len(recordings) == 0 {
		return nil
	}

	// Cancel any existing replay
	if rs := rec.getReplayState(); rs != nil {
		rs.cancel()
	}

	rs := startReplay(recordings, cfg)

	rec.mu.Lock()
	rec.replay = rs
	rec.mu.Unlock()

	return nil
}

// CancelReplay cancels an active replay.
func (rec *Recorder) CancelReplay() {
	if rs := rec.getReplayState(); rs != nil {
		rs.cancel()
	}
}

// GetReplayStats returns the current replay stats, or nil if no replay is active.
func (rec *Recorder) GetReplayStats() *ReplayStats {
	rs := rec.getReplayState()
	if rs == nil {
		return nil
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	snapshot := rs.stats
	snapshot.Sent = atomic.LoadInt64(&rs.stats.Sent)
	snapshot.Errors = atomic.LoadInt64(&rs.stats.Errors)
	return &snapshot
}

func (rec *Recorder) getReplayState() *replayState {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.replay
}

// ReplayByRoute manages Recorders per route.
type ReplayByRoute struct {
	mu        sync.RWMutex
	recorders map[string]*Recorder
}

// NewReplayByRoute creates a new ReplayByRoute manager.
func NewReplayByRoute() *ReplayByRoute {
	return &ReplayByRoute{
		recorders: make(map[string]*Recorder),
	}
}

// AddRoute creates and stores a Recorder for the given route.
func (m *ReplayByRoute) AddRoute(id string, cfg config.TrafficReplayConfig) error {
	rec, err := New(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.recorders[id] = rec
	m.mu.Unlock()
	return nil
}

// GetRecorder returns the Recorder for the given route, or nil.
func (m *ReplayByRoute) GetRecorder(id string) *Recorder {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.recorders[id]
}

// RouteIDs returns all route IDs with traffic replay configured.
func (m *ReplayByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.recorders))
	for id := range m.recorders {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns snapshot stats for all routes.
func (m *ReplayByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]interface{}, len(m.recorders))
	for id, rec := range m.recorders {
		result[id] = rec.Snapshot()
	}
	return result
}
