package trafficreplay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func TestRecorder_BasicRecording(t *testing.T) {
	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.StartRecording()

	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/test?q=1", nil)
	req.Header.Set("X-Custom", "value")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	recordings := rec.GetRecordings()
	if len(recordings) != 1 {
		t.Fatalf("expected 1 recording, got %d", len(recordings))
	}
	if recordings[0].Method != "GET" {
		t.Errorf("expected method GET, got %s", recordings[0].Method)
	}
	if recordings[0].URL != "/api/test?q=1" {
		t.Errorf("expected URL /api/test?q=1, got %s", recordings[0].URL)
	}
	if recordings[0].Headers.Get("X-Custom") != "value" {
		t.Errorf("expected X-Custom header, got %s", recordings[0].Headers.Get("X-Custom"))
	}
}

func TestRecorder_BodyCapture(t *testing.T) {
	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
		MaxBodySize:   10, // limit to 10 bytes
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.StartRecording()

	bodyContent := "this is a longer body that should be truncated"
	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler should still be able to read the body (replaced via NopCloser)
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("handler should still be able to read body")
		}
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("POST", "/api/data", strings.NewReader(bodyContent))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	recordings := rec.GetRecordings()
	if len(recordings) != 1 {
		t.Fatalf("expected 1 recording, got %d", len(recordings))
	}
	if len(recordings[0].Body) > 10 {
		t.Errorf("expected body truncated to 10 bytes, got %d", len(recordings[0].Body))
	}
}

func TestRecorder_RingBufferWrap(t *testing.T) {
	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.StartRecording()

	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Record 5 requests (buffer size 3)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", fmt.Sprintf("/req/%d", i), nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	recordings := rec.GetRecordings()
	if len(recordings) != 3 {
		t.Fatalf("expected 3 recordings (buffer size), got %d", len(recordings))
	}
	// Should have the last 3 requests: /req/2, /req/3, /req/4
	if recordings[0].URL != "/req/2" {
		t.Errorf("expected first recording /req/2, got %s", recordings[0].URL)
	}
	if recordings[2].URL != "/req/4" {
		t.Errorf("expected last recording /req/4, got %s", recordings[2].URL)
	}
}

func TestRecorder_Conditions(t *testing.T) {
	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
		Conditions: config.TrafficReplayConditions{
			Methods:   []string{"POST", "PUT"},
			PathRegex: "^/api/",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.StartRecording()

	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Should NOT record: GET method
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/test", nil))
	// Should NOT record: wrong path
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/other/test", nil))
	// Should record: POST + /api/ path
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/test", nil))
	// Should record: PUT + /api/ path
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/api/data", nil))

	recordings := rec.GetRecordings()
	if len(recordings) != 2 {
		t.Fatalf("expected 2 recordings, got %d", len(recordings))
	}
}

func TestRecorder_StartStopRecording(t *testing.T) {
	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Recording off by default
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/test1", nil))
	if len(rec.GetRecordings()) != 0 {
		t.Error("should not record when recording is off")
	}

	// Start recording
	rec.StartRecording()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/test2", nil))
	if len(rec.GetRecordings()) != 1 {
		t.Error("should record when recording is on")
	}

	// Stop recording
	rec.StopRecording()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/test3", nil))
	if len(rec.GetRecordings()) != 1 {
		t.Error("should not record when recording is stopped")
	}
}

func TestRecorder_ClearRecordings(t *testing.T) {
	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.StartRecording()

	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/test", nil))
	if len(rec.GetRecordings()) != 1 {
		t.Fatal("expected 1 recording")
	}

	rec.ClearRecordings()
	if len(rec.GetRecordings()) != 0 {
		t.Error("expected 0 recordings after clear")
	}
}

func TestRecorder_Replay(t *testing.T) {
	var received int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&received, 1)
		w.WriteHeader(200)
	}))
	defer target.Close()

	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.StartRecording()

	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Record 5 requests
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/%d", i), nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	// Replay
	err = rec.StartReplay(ReplayConfig{
		Target:      target.URL,
		Concurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for replay to complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stats := rec.GetReplayStats()
		if stats != nil && stats.Completed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	stats := rec.GetReplayStats()
	if stats == nil {
		t.Fatal("expected replay stats")
	}
	if !stats.Completed {
		t.Error("replay should be completed")
	}
	if stats.Total != 5 {
		t.Errorf("expected total 5, got %d", stats.Total)
	}
	if atomic.LoadInt64(&received) != 5 {
		t.Errorf("expected 5 requests received by target, got %d", atomic.LoadInt64(&received))
	}
}

func TestRecorder_ReplayWithRateLimit(t *testing.T) {
	var received int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&received, 1)
		w.WriteHeader(200)
	}))
	defer target.Close()

	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.StartRecording()

	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	for i := 0; i < 3; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/test", nil))
	}

	err = rec.StartReplay(ReplayConfig{
		Target:      target.URL,
		Concurrency: 1,
		RatePerSec:  1000, // high rate to finish fast
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stats := rec.GetReplayStats()
		if stats != nil && stats.Completed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if atomic.LoadInt64(&received) != 3 {
		t.Errorf("expected 3 requests, got %d", atomic.LoadInt64(&received))
	}
}

func TestRecorder_CancelReplay(t *testing.T) {
	// Slow target to allow cancel
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer target.Close()

	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.StartRecording()

	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	for i := 0; i < 50; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/test", nil))
	}

	err = rec.StartReplay(ReplayConfig{
		Target:      target.URL,
		Concurrency: 1,
		RatePerSec:  10,
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	rec.CancelReplay()

	stats := rec.GetReplayStats()
	if stats == nil {
		t.Fatal("expected replay stats after cancel")
	}
	// Should not have sent all 50
	if stats.Sent >= 50 {
		t.Errorf("expected fewer than 50 sent after cancel, got %d", stats.Sent)
	}
}

func TestRecorder_ConcurrentSafety(t *testing.T) {
	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.StartRecording()

	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", fmt.Sprintf("/test/%d", i), nil)
			handler.ServeHTTP(httptest.NewRecorder(), req)
		}(i)
	}
	wg.Wait()

	recordings := rec.GetRecordings()
	if len(recordings) != 50 {
		t.Errorf("expected 50 recordings, got %d", len(recordings))
	}
}

func TestRecorder_Snapshot(t *testing.T) {
	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	snap := rec.Snapshot()
	if snap["recording"] != false {
		t.Error("expected recording=false")
	}
	if snap["buffer_size"] != 100 {
		t.Errorf("expected buffer_size=100, got %v", snap["buffer_size"])
	}
	if snap["buffer_used"] != 0 {
		t.Errorf("expected buffer_used=0, got %v", snap["buffer_used"])
	}
}

func TestRecorder_ReplayBodyForwarded(t *testing.T) {
	var receivedBody []byte
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer target.Close()

	rec, err := New(config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.StartRecording()

	handler := rec.RecordingMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	body := bytes.NewReader([]byte(`{"key":"value"}`))
	req := httptest.NewRequest("POST", "/api/data", body)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	err = rec.StartReplay(ReplayConfig{
		Target:      target.URL,
		Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stats := rec.GetReplayStats()
		if stats != nil && stats.Completed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if string(receivedBody) != `{"key":"value"}` {
		t.Errorf("expected body forwarded, got %s", string(receivedBody))
	}
}

func TestReplayByRoute(t *testing.T) {
	mgr := NewReplayByRoute()

	err := mgr.AddRoute("route1", config.TrafficReplayConfig{
		Enabled:       true,
		MaxRecordings: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	if mgr.Lookup("route1") == nil {
		t.Error("expected recorder for route1")
	}
	if mgr.Lookup("route2") != nil {
		t.Error("expected nil for route2")
	}

	ids := mgr.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("expected [route1], got %v", ids)
	}

	stats := mgr.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}

func TestConditions(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.TrafficReplayConditions
		method  string
		path    string
		want    bool
	}{
		{"empty conditions match all", config.TrafficReplayConditions{}, "GET", "/foo", true},
		{"method match", config.TrafficReplayConditions{Methods: []string{"POST"}}, "POST", "/foo", true},
		{"method no match", config.TrafficReplayConditions{Methods: []string{"POST"}}, "GET", "/foo", false},
		{"path match", config.TrafficReplayConditions{PathRegex: "^/api/"}, "GET", "/api/test", true},
		{"path no match", config.TrafficReplayConditions{PathRegex: "^/api/"}, "GET", "/other", false},
		{"both match", config.TrafficReplayConditions{Methods: []string{"POST"}, PathRegex: "^/api/"}, "POST", "/api/test", true},
		{"method match path no", config.TrafficReplayConditions{Methods: []string{"POST"}, PathRegex: "^/api/"}, "POST", "/other", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond, err := NewConditions(tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tt.method, tt.path, nil)
			got := cond.Match(req)
			if got != tt.want {
				t.Errorf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}
