package slo

import (
	"sync"
	"time"
)

const windowBuckets = 60

type bucket struct {
	total  int64
	errors int64
}

// SlidingWindow tracks request outcomes in a ring buffer of fixed-size buckets.
type SlidingWindow struct {
	windowDur time.Duration
	bucketDur time.Duration

	mu      sync.Mutex
	buckets [windowBuckets]bucket
	idx     int
	lastAdv time.Time
}

// NewSlidingWindow creates a sliding window with the given duration.
func NewSlidingWindow(window time.Duration) *SlidingWindow {
	if window <= 0 {
		window = time.Hour
	}
	return &SlidingWindow{
		windowDur: window,
		bucketDur: window / windowBuckets,
		lastAdv:   time.Now(),
	}
}

// Record records a single request outcome.
func (w *SlidingWindow) Record(isError bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.advance()
	w.buckets[w.idx].total++
	if isError {
		w.buckets[w.idx].errors++
	}
}

// Snapshot returns the aggregate total and error counts across the window.
func (w *SlidingWindow) Snapshot() (total, errors int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.advance()
	for i := 0; i < windowBuckets; i++ {
		total += w.buckets[i].total
		errors += w.buckets[i].errors
	}
	return
}

// advance moves the window forward, zeroing expired buckets.
func (w *SlidingWindow) advance() {
	now := time.Now()
	elapsed := now.Sub(w.lastAdv)
	if elapsed < w.bucketDur {
		return
	}
	steps := int(elapsed / w.bucketDur)
	if steps > windowBuckets {
		steps = windowBuckets
	}
	for i := 0; i < steps; i++ {
		w.idx = (w.idx + 1) % windowBuckets
		w.buckets[w.idx] = bucket{}
	}
	w.lastAdv = now
}
