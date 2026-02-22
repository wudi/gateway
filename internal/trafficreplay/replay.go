package trafficreplay

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// ReplayConfig configures a replay operation.
type ReplayConfig struct {
	Target      string  `json:"target"`       // target URL base
	Concurrency int     `json:"concurrency"`  // worker count, default 10
	RatePerSec  float64 `json:"rate_per_sec"` // 0 = unlimited
}

// ReplayStats tracks the progress of an active replay.
type ReplayStats struct {
	Started   time.Time `json:"started"`
	Total     int       `json:"total"`
	Sent      int64     `json:"sent"`
	Errors    int64     `json:"errors"`
	Completed bool      `json:"completed"`
}

// replayState holds the mutable state of an active replay.
type replayState struct {
	stats  ReplayStats
	cancel context.CancelFunc
	mu     sync.Mutex
}

// startReplay launches a replay of the given recordings against a target backend.
func startReplay(recordings []RecordedRequest, cfg ReplayConfig) *replayState {
	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 10
	}

	ctx, cancel := context.WithCancel(context.Background())
	rs := &replayState{
		stats: ReplayStats{
			Started: time.Now(),
			Total:   len(recordings),
		},
		cancel: cancel,
	}

	var limiter *rate.Limiter
	if cfg.RatePerSec > 0 {
		limiter = rate.NewLimiter(rate.Limit(cfg.RatePerSec), 1)
	}

	ch := make(chan RecordedRequest, len(recordings))
	for _, rec := range recordings {
		ch <- rec
	}
	close(ch)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{Timeout: 30 * time.Second}
			for rec := range ch {
				if ctx.Err() != nil {
					return
				}
				if limiter != nil {
					if err := limiter.Wait(ctx); err != nil {
						return
					}
				}
				sendReplayRequest(ctx, client, cfg.Target, rec, rs)
			}
		}()
	}

	go func() {
		wg.Wait()
		rs.mu.Lock()
		rs.stats.Completed = true
		rs.mu.Unlock()
	}()

	return rs
}

func sendReplayRequest(ctx context.Context, client *http.Client, target string, rec RecordedRequest, rs *replayState) {
	var body *bytes.Reader
	if len(rec.Body) > 0 {
		body = bytes.NewReader(rec.Body)
	} else {
		body = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, rec.Method, target+rec.URL, body)
	if err != nil {
		atomic.AddInt64(&rs.stats.Errors, 1)
		return
	}

	for k, vals := range rec.Headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	// Override Host to target
	req.Host = req.URL.Host

	resp, err := client.Do(req)
	if err != nil {
		atomic.AddInt64(&rs.stats.Errors, 1)
		return
	}
	resp.Body.Close()
	atomic.AddInt64(&rs.stats.Sent, 1)
}
