package trafficshape

import (
	"container/heap"
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/variables"
)

// PriorityAdmitter implements priority-based admission control with a shared concurrency semaphore.
type PriorityAdmitter struct {
	maxConcurrent int
	active        atomic.Int64
	mu            sync.Mutex
	queue         waitQueue

	totalAdmitted atomic.Int64
	totalRejected atomic.Int64
}

// NewPriorityAdmitter creates a new PriorityAdmitter.
func NewPriorityAdmitter(maxConcurrent int) *PriorityAdmitter {
	pa := &PriorityAdmitter{
		maxConcurrent: maxConcurrent,
	}
	heap.Init(&pa.queue)
	return pa
}

// Admit tries to acquire a concurrency slot. If all slots are taken,
// the caller is queued by priority level and waits. Returns a release
// function on success, or an error if the context expires.
func (pa *PriorityAdmitter) Admit(ctx context.Context, level int) (release func(), err error) {
	pa.mu.Lock()

	if int(pa.active.Load()) < pa.maxConcurrent {
		pa.active.Add(1)
		pa.mu.Unlock()
		pa.totalAdmitted.Add(1)
		return pa.makeRelease(), nil
	}

	// Queue the waiter
	entry := &waitEntry{
		level: level,
		ch:    make(chan struct{}),
		index: -1,
	}
	heap.Push(&pa.queue, entry)
	pa.mu.Unlock()

	// Wait for slot or context cancellation
	select {
	case <-entry.ch:
		pa.totalAdmitted.Add(1)
		return pa.makeRelease(), nil
	case <-ctx.Done():
		pa.mu.Lock()
		if entry.index >= 0 {
			heap.Remove(&pa.queue, entry.index)
		}
		pa.mu.Unlock()
		pa.totalRejected.Add(1)
		return nil, ctx.Err()
	}
}

func (pa *PriorityAdmitter) makeRelease() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			pa.mu.Lock()
			defer pa.mu.Unlock()

			if pa.queue.Len() > 0 {
				// Hand slot directly to highest-priority waiter
				next := heap.Pop(&pa.queue).(*waitEntry)
				close(next.ch)
			} else {
				pa.active.Add(-1)
			}
		})
	}
}

// Snapshot returns a point-in-time metrics snapshot.
func (pa *PriorityAdmitter) Snapshot() PrioritySnapshot {
	pa.mu.Lock()
	queueDepth := pa.queue.Len()
	pa.mu.Unlock()

	return PrioritySnapshot{
		MaxConcurrent: pa.maxConcurrent,
		Active:        int(pa.active.Load()),
		QueueDepth:    queueDepth,
		TotalAdmitted: pa.totalAdmitted.Load(),
		TotalRejected: pa.totalRejected.Load(),
	}
}

// DetermineLevel checks configured priority levels and returns the first matching
// level, or the default level if nothing matches.
func DetermineLevel(r *http.Request, identity *variables.Identity, cfg config.PriorityConfig) int {
	defaultLevel := cfg.DefaultLevel
	if defaultLevel == 0 {
		defaultLevel = 5
	}

	for _, lvl := range cfg.Levels {
		if matchesPriorityLevel(r, identity, lvl) {
			return lvl.Level
		}
	}
	return defaultLevel
}

func matchesPriorityLevel(r *http.Request, identity *variables.Identity, lvl config.PriorityLevelConfig) bool {
	// Check headers
	if len(lvl.Headers) > 0 {
		allMatch := true
		for name, value := range lvl.Headers {
			if r.Header.Get(name) != value {
				allMatch = false
				break
			}
		}
		if allMatch {
			return true
		}
	}

	// Check client IDs
	if len(lvl.ClientIDs) > 0 && identity != nil && identity.ClientID != "" {
		for _, cid := range lvl.ClientIDs {
			if cid == identity.ClientID {
				return true
			}
		}
	}

	return false
}

// waitEntry is an element in the priority wait queue.
type waitEntry struct {
	level int
	ch    chan struct{}
	index int
}

// waitQueue implements heap.Interface ordered by priority level (lower = higher priority).
type waitQueue []*waitEntry

func (q waitQueue) Len() int           { return len(q) }
func (q waitQueue) Less(i, j int) bool { return q[i].level < q[j].level }
func (q waitQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}

func (q *waitQueue) Push(x any) {
	entry := x.(*waitEntry)
	entry.index = len(*q)
	*q = append(*q, entry)
}

func (q *waitQueue) Pop() any {
	old := *q
	n := len(old)
	entry := old[n-1]
	old[n-1] = nil
	entry.index = -1
	*q = old[:n-1]
	return entry
}
