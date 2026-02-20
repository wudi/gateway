package retry

import (
	"sync"
	"sync/atomic"
	"time"
)

const budgetBuckets = 10

type atomicBucket struct {
	requests atomic.Int64
	retries  atomic.Int64
}

// Budget tracks the ratio of retries to total requests over a sliding window,
// preventing cascading retry storms under load.
type Budget struct {
	ratio          float64
	minRetriesPerS int
	window         time.Duration
	bucketDurNano  int64

	buckets [budgetBuckets]atomicBucket

	// epoch is the current bucket index; updated only during advance.
	epoch atomic.Int64

	// lastAdvNano is the UnixNano timestamp of the last advance.
	// Used as a fast-path check to avoid locking on every call.
	lastAdvNano atomic.Int64

	// advMu protects bucket rotation (rare — once per bucketDur).
	advMu sync.Mutex
}

// NewBudget creates a retry budget.
// ratio: max fraction of requests that can be retries (0.0-1.0).
// minRetries: always allow at least this many retries per second regardless of ratio.
// window: sliding window duration (default 10s).
func NewBudget(ratio float64, minRetries int, window time.Duration) *Budget {
	if window <= 0 {
		window = 10 * time.Second
	}
	b := &Budget{
		ratio:          ratio,
		minRetriesPerS: minRetries,
		window:         window,
		bucketDurNano:  int64(window / budgetBuckets),
	}
	b.lastAdvNano.Store(time.Now().UnixNano())
	return b
}

// RecordRequest records an incoming request.
func (b *Budget) RecordRequest() {
	b.maybeAdvance()
	idx := b.epoch.Load() % budgetBuckets
	b.buckets[idx].requests.Add(1)
}

// AllowRetry returns true if the budget permits another retry.
func (b *Budget) AllowRetry() bool {
	b.maybeAdvance()

	var totalReqs, totalRetries int64
	for i := 0; i < budgetBuckets; i++ {
		totalReqs += b.buckets[i].requests.Load()
		totalRetries += b.buckets[i].retries.Load()
	}

	// Always allow if below minimum retries per second
	windowSec := b.window.Seconds()
	if windowSec > 0 && float64(totalRetries)/windowSec < float64(b.minRetriesPerS) {
		return true
	}

	// Allow if ratio is within budget
	if totalReqs == 0 {
		return true
	}
	return float64(totalRetries)/float64(totalReqs) < b.ratio
}

// RecordRetry records that a retry was attempted.
func (b *Budget) RecordRetry() {
	b.maybeAdvance()
	idx := b.epoch.Load() % budgetBuckets
	b.buckets[idx].retries.Add(1)
}

// BudgetStats holds a point-in-time snapshot of budget state.
type BudgetStats struct {
	Ratio        float64 `json:"ratio"`
	MinRetries   int     `json:"min_retries_per_sec"`
	Window       string  `json:"window"`
	TotalReqs    int64   `json:"total_requests"`
	TotalRetries int64   `json:"total_retries"`
	Utilization  float64 `json:"utilization"`
}

// Stats returns a point-in-time snapshot of the budget.
func (b *Budget) Stats() BudgetStats {
	b.maybeAdvance()
	var totalReqs, totalRetries int64
	for i := 0; i < budgetBuckets; i++ {
		totalReqs += b.buckets[i].requests.Load()
		totalRetries += b.buckets[i].retries.Load()
	}
	var utilization float64
	if totalReqs > 0 {
		utilization = float64(totalRetries) / float64(totalReqs)
	}
	return BudgetStats{
		Ratio:        b.ratio,
		MinRetries:   b.minRetriesPerS,
		Window:       b.window.String(),
		TotalReqs:    totalReqs,
		TotalRetries: totalRetries,
		Utilization:  utilization,
	}
}

// maybeAdvance checks whether the window needs rotating. The fast path
// (no rotation needed) is lock-free — only an atomic load + comparison.
func (b *Budget) maybeAdvance() {
	now := time.Now().UnixNano()
	last := b.lastAdvNano.Load()
	if now-last < b.bucketDurNano {
		return
	}

	// Slow path: acquire mutex and rotate buckets.
	b.advMu.Lock()
	defer b.advMu.Unlock()

	// Re-check under lock (another goroutine may have advanced).
	last = b.lastAdvNano.Load()
	elapsed := now - last
	if elapsed < b.bucketDurNano {
		return
	}

	steps := int(elapsed / b.bucketDurNano)
	if steps > budgetBuckets {
		steps = budgetBuckets
	}
	cur := b.epoch.Load()
	for i := 0; i < steps; i++ {
		cur = (cur + 1) % budgetBuckets
		b.buckets[cur].requests.Store(0)
		b.buckets[cur].retries.Store(0)
	}
	b.epoch.Store(cur)
	b.lastAdvNano.Store(now)
}
