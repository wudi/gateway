package outlier

import (
	"testing"
	"time"
)

func TestBackendStatsRecord(t *testing.T) {
	s := NewBackendStats(10 * time.Second)

	s.Record(200, 10*time.Millisecond)
	s.Record(200, 20*time.Millisecond)
	s.Record(500, 30*time.Millisecond)

	snap := s.Snapshot()
	if snap.TotalRequests != 3 {
		t.Errorf("expected 3 requests, got %d", snap.TotalRequests)
	}
	if snap.TotalErrors != 1 {
		t.Errorf("expected 1 error, got %d", snap.TotalErrors)
	}
	wantRate := 1.0 / 3.0
	if snap.ErrorRate < wantRate-0.01 || snap.ErrorRate > wantRate+0.01 {
		t.Errorf("expected error rate ~%.3f, got %.3f", wantRate, snap.ErrorRate)
	}
	if snap.P50 != 20*time.Millisecond {
		t.Errorf("expected p50=20ms, got %v", snap.P50)
	}
	if snap.P99 != 30*time.Millisecond {
		t.Errorf("expected p99=30ms, got %v", snap.P99)
	}
}

func TestBackendStatsEmpty(t *testing.T) {
	s := NewBackendStats(10 * time.Second)
	snap := s.Snapshot()
	if snap.TotalRequests != 0 || snap.TotalErrors != 0 || snap.ErrorRate != 0 {
		t.Errorf("expected empty snapshot, got %+v", snap)
	}
}

func TestBackendStatsOnlyErrors(t *testing.T) {
	s := NewBackendStats(10 * time.Second)
	s.Record(502, 5*time.Millisecond)
	s.Record(503, 15*time.Millisecond)

	snap := s.Snapshot()
	if snap.TotalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", snap.TotalRequests)
	}
	if snap.ErrorRate != 1.0 {
		t.Errorf("expected error rate 1.0, got %.3f", snap.ErrorRate)
	}
}

func TestPercentile(t *testing.T) {
	sorted := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
		5 * time.Millisecond,
	}

	p50 := percentile(sorted, 0.50)
	if p50 != 3*time.Millisecond {
		t.Errorf("expected p50=3ms, got %v", p50)
	}

	p99 := percentile(sorted, 0.99)
	if p99 != 5*time.Millisecond {
		t.Errorf("expected p99=5ms, got %v", p99)
	}

	p0 := percentile(nil, 0.5)
	if p0 != 0 {
		t.Errorf("expected 0 for empty, got %v", p0)
	}
}

func TestBackendStats499NotError(t *testing.T) {
	s := NewBackendStats(10 * time.Second)
	s.Record(499, 5*time.Millisecond)
	snap := s.Snapshot()
	if snap.TotalErrors != 0 {
		t.Errorf("expected 499 to not be an error, got %d errors", snap.TotalErrors)
	}
}
