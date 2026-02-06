package retry

import (
	"sync"
	"time"
)

const budgetBuckets = 10

type bucketData struct {
	requests int64
	retries  int64
}

// Budget tracks the ratio of retries to total requests over a sliding window,
// preventing cascading retry storms under load.
type Budget struct {
	ratio          float64
	minRetriesPerS int
	window         time.Duration
	bucketDur      time.Duration

	mu      sync.Mutex
	buckets [budgetBuckets]bucketData
	idx     int
	lastAdv time.Time
}

// NewBudget creates a retry budget.
// ratio: max fraction of requests that can be retries (0.0-1.0).
// minRetries: always allow at least this many retries per second regardless of ratio.
// window: sliding window duration (default 10s).
func NewBudget(ratio float64, minRetries int, window time.Duration) *Budget {
	if window <= 0 {
		window = 10 * time.Second
	}
	return &Budget{
		ratio:          ratio,
		minRetriesPerS: minRetries,
		window:         window,
		bucketDur:      window / budgetBuckets,
		lastAdv:        time.Now(),
	}
}

// RecordRequest records an incoming request.
func (b *Budget) RecordRequest() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	b.buckets[b.idx].requests++
}

// AllowRetry returns true if the budget permits another retry.
func (b *Budget) AllowRetry() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()

	var totalReqs, totalRetries int64
	for i := 0; i < budgetBuckets; i++ {
		totalReqs += b.buckets[i].requests
		totalRetries += b.buckets[i].retries
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
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	b.buckets[b.idx].retries++
}

// advance moves the window forward, zeroing expired buckets.
func (b *Budget) advance() {
	now := time.Now()
	elapsed := now.Sub(b.lastAdv)
	if elapsed < b.bucketDur {
		return
	}

	steps := int(elapsed / b.bucketDur)
	if steps > budgetBuckets {
		steps = budgetBuckets
	}
	for i := 0; i < steps; i++ {
		b.idx = (b.idx + 1) % budgetBuckets
		b.buckets[b.idx] = bucketData{}
	}
	b.lastAdv = now
}
