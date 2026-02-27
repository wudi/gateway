package slo

import (
	"bufio"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/logging"
	"github.com/wudi/runway/internal/middleware"
	"go.uber.org/zap"
)

// Tracker tracks SLI metrics and enforces SLO error budgets for a single route.
type Tracker struct {
	window          *SlidingWindow
	target          float64
	errorCodeSet    map[int]bool
	actionLog       bool
	actionHeader    bool
	actionShedLoad  bool
	shedLoadPercent float64

	shedCount atomic.Int64
}

// NewTracker creates a new SLO tracker from config.
func NewTracker(cfg config.SLOConfig) *Tracker {
	errorCodes := make(map[int]bool, len(cfg.ErrorCodes))
	if len(cfg.ErrorCodes) == 0 {
		// Default: 500-599
		for i := 500; i <= 599; i++ {
			errorCodes[i] = true
		}
	} else {
		for _, code := range cfg.ErrorCodes {
			errorCodes[code] = true
		}
	}

	shedPct := cfg.ShedLoadPercent
	if shedPct <= 0 {
		shedPct = 10
	}

	t := &Tracker{
		window:          NewSlidingWindow(cfg.Window),
		target:          cfg.Target,
		errorCodeSet:    errorCodes,
		shedLoadPercent: shedPct,
	}

	for _, action := range cfg.Actions {
		switch action {
		case "log_warning":
			t.actionLog = true
		case "add_header":
			t.actionHeader = true
		case "shed_load":
			t.actionShedLoad = true
		}
	}

	return t
}

// BudgetRemaining returns the remaining error budget as a value 0.0-1.0.
// A value <= 0 means the budget is exhausted.
func (t *Tracker) BudgetRemaining() float64 {
	total, errors := t.window.Snapshot()
	if total == 0 {
		return 1.0
	}
	allowedErrorRate := 1.0 - t.target
	if allowedErrorRate <= 0 {
		return 1.0
	}
	actualErrorRate := float64(errors) / float64(total)
	return 1.0 - (actualErrorRate / allowedErrorRate)
}

// Middleware returns the SLO enforcement middleware.
func (t *Tracker) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Pre-request: load shedding
			if t.actionShedLoad && t.BudgetRemaining() <= 0 {
				// Probabilistic rejection
				if rand.Float64()*100 < t.shedLoadPercent {
					t.shedCount.Add(1)
					w.Header().Set("Retry-After", "5")
					http.Error(w, "service unavailable: SLO error budget exhausted", http.StatusServiceUnavailable)
					return
				}
			}

			// Add budget header before response
			if t.actionHeader {
				budget := t.BudgetRemaining()
				w.Header().Set("X-SLO-Budget-Remaining", fmt.Sprintf("%.4f", budget))
			}

			// Wrap response writer to capture status
			sw := &sloWriter{ResponseWriter: w, statusCode: 200}
			next.ServeHTTP(sw, r)

			// Post-request: record outcome
			isErr := t.errorCodeSet[sw.statusCode]
			t.window.Record(isErr)

			// Log warning if budget exhausted
			if t.actionLog && t.BudgetRemaining() <= 0 {
				logging.Warn("SLO error budget exhausted",
					zap.String("path", r.URL.Path),
					zap.Float64("target", t.target),
					zap.Int("status", sw.statusCode),
				)
			}
		})
	}
}

// Snapshot returns a point-in-time snapshot of the tracker state.
func (t *Tracker) Snapshot() map[string]interface{} {
	total, errors := t.window.Snapshot()
	var errorRate float64
	if total > 0 {
		errorRate = float64(errors) / float64(total)
	}
	return map[string]interface{}{
		"target":           t.target,
		"total":            total,
		"errors":           errors,
		"error_rate":       errorRate,
		"budget_remaining": t.BudgetRemaining(),
		"shed_count":       t.shedCount.Load(),
	}
}

// sloWriter wraps ResponseWriter to capture status code.
type sloWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *sloWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *sloWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush implements http.Flusher.
func (w *sloWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker.
func (w *sloWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// SLOByRoute manages per-route SLO trackers.
type SLOByRoute = byroute.Factory[*Tracker, config.SLOConfig]

// NewSLOByRoute creates a new per-route SLO manager.
func NewSLOByRoute() *SLOByRoute {
	return byroute.SimpleFactory(NewTracker, func(t *Tracker) any { return t.Snapshot() })
}
