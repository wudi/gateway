package mirror

import (
	"testing"
	"time"
)

func TestMirrorMetricsRecordSuccess(t *testing.T) {
	m := NewMirrorMetrics()
	m.RecordSuccess(100 * time.Millisecond)
	m.RecordSuccess(200 * time.Millisecond)
	m.RecordSuccess(50 * time.Millisecond)

	snap := m.Snapshot()
	if snap.TotalMirrored != 3 {
		t.Errorf("expected TotalMirrored=3, got %d", snap.TotalMirrored)
	}
	if snap.TotalErrors != 0 {
		t.Errorf("expected TotalErrors=0, got %d", snap.TotalErrors)
	}
	if snap.LatencyP50 == 0 {
		t.Error("expected non-zero P50 latency")
	}
}

func TestMirrorMetricsRecordError(t *testing.T) {
	m := NewMirrorMetrics()
	m.RecordError()
	m.RecordError()
	m.RecordSuccess(10 * time.Millisecond)

	snap := m.Snapshot()
	if snap.TotalMirrored != 3 {
		t.Errorf("expected TotalMirrored=3, got %d", snap.TotalMirrored)
	}
	if snap.TotalErrors != 2 {
		t.Errorf("expected TotalErrors=2, got %d", snap.TotalErrors)
	}
}

func TestMirrorMetricsRecordComparison(t *testing.T) {
	m := NewMirrorMetrics()

	m.RecordComparison(CompareResult{StatusMatch: true, BodyMatch: true})
	snap := m.Snapshot()
	if snap.TotalCompared != 1 {
		t.Errorf("expected TotalCompared=1, got %d", snap.TotalCompared)
	}
	if snap.TotalMismatches != 0 {
		t.Errorf("expected TotalMismatches=0, got %d", snap.TotalMismatches)
	}

	m.RecordComparison(CompareResult{StatusMatch: false, BodyMatch: true})
	snap = m.Snapshot()
	if snap.TotalMismatches != 1 {
		t.Errorf("expected TotalMismatches=1, got %d", snap.TotalMismatches)
	}

	m.RecordComparison(CompareResult{StatusMatch: true, BodyMatch: false})
	snap = m.Snapshot()
	if snap.TotalMismatches != 2 {
		t.Errorf("expected TotalMismatches=2, got %d", snap.TotalMismatches)
	}
}

func TestMirrorMetricsPercentiles(t *testing.T) {
	m := NewMirrorMetrics()

	// Add 100 samples from 1ms to 100ms
	for i := 1; i <= 100; i++ {
		m.RecordSuccess(time.Duration(i) * time.Millisecond)
	}

	snap := m.Snapshot()

	// P50 should be around 50ms
	if snap.LatencyP50 < 45*time.Millisecond || snap.LatencyP50 > 55*time.Millisecond {
		t.Errorf("P50 %v out of expected range", snap.LatencyP50)
	}

	// P95 should be around 95ms
	if snap.LatencyP95 < 90*time.Millisecond || snap.LatencyP95 > 100*time.Millisecond {
		t.Errorf("P95 %v out of expected range", snap.LatencyP95)
	}

	// P99 should be around 99ms
	if snap.LatencyP99 < 95*time.Millisecond || snap.LatencyP99 > 100*time.Millisecond {
		t.Errorf("P99 %v out of expected range", snap.LatencyP99)
	}
}

func TestMirrorMetricsEmptySnapshot(t *testing.T) {
	m := NewMirrorMetrics()
	snap := m.Snapshot()
	if snap.TotalMirrored != 0 || snap.TotalErrors != 0 || snap.LatencyP50 != 0 {
		t.Error("empty metrics should have zero values")
	}
}

func TestMirrorMetricsRingBufferWrap(t *testing.T) {
	m := NewMirrorMetrics()

	// Fill past ring buffer size
	for i := 0; i < latencyRingSize+500; i++ {
		m.RecordSuccess(time.Duration(i+1) * time.Microsecond)
	}

	snap := m.Snapshot()
	if snap.TotalMirrored != int64(latencyRingSize+500) {
		t.Errorf("expected TotalMirrored=%d, got %d", latencyRingSize+500, snap.TotalMirrored)
	}
	// Should still compute percentiles without panic
	if snap.LatencyP50 == 0 {
		t.Error("expected non-zero P50")
	}
}
