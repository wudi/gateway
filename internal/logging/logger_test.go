package logging

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNew(t *testing.T) {
	tests := []struct {
		level    string
		wantLvl  zapcore.Level
	}{
		{"debug", zapcore.DebugLevel},
		{"info", zapcore.InfoLevel},
		{"warn", zapcore.WarnLevel},
		{"error", zapcore.ErrorLevel},
		{"", zapcore.InfoLevel},       // default
		{"unknown", zapcore.InfoLevel}, // default
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			l, err := New(tt.level)
			if err != nil {
				t.Fatalf("New(%q) returned error: %v", tt.level, err)
			}
			if l == nil {
				t.Fatalf("New(%q) returned nil logger", tt.level)
			}
		})
	}
}

func TestGlobalSetGlobal(t *testing.T) {
	original := Global()
	if original == nil {
		t.Fatal("Global() returned nil before SetGlobal")
	}

	core, obs := observer.New(zapcore.InfoLevel)
	testLogger := zap.New(core)

	SetGlobal(testLogger)
	defer SetGlobal(original)

	Info("test message", zap.String("key", "value"))

	entries := obs.All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}
	if entries[0].Message != "test message" {
		t.Errorf("expected message %q, got %q", "test message", entries[0].Message)
	}
}

func TestLogLevels(t *testing.T) {
	original := Global()
	core, obs := observer.New(zapcore.DebugLevel)
	SetGlobal(zap.New(core))
	defer SetGlobal(original)

	Debug("debug msg")
	Info("info msg")
	Warn("warn msg")
	Error("error msg")

	entries := obs.All()
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	expected := []struct {
		msg   string
		level zapcore.Level
	}{
		{"debug msg", zapcore.DebugLevel},
		{"info msg", zapcore.InfoLevel},
		{"warn msg", zapcore.WarnLevel},
		{"error msg", zapcore.ErrorLevel},
	}

	for i, e := range expected {
		if entries[i].Message != e.msg {
			t.Errorf("entry %d: expected message %q, got %q", i, e.msg, entries[i].Message)
		}
		if entries[i].Level != e.level {
			t.Errorf("entry %d: expected level %v, got %v", i, e.level, entries[i].Level)
		}
	}
}

func TestWith(t *testing.T) {
	original := Global()
	core, obs := observer.New(zapcore.InfoLevel)
	SetGlobal(zap.New(core))
	defer SetGlobal(original)

	child := With(zap.String("component", "test"))
	child.Info("child message")

	entries := obs.All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	found := false
	for _, f := range entries[0].ContextMap() {
		if f == "test" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'component' field in log entry context")
	}
}

func TestLevelFiltering(t *testing.T) {
	original := Global()
	core, obs := observer.New(zapcore.WarnLevel)
	SetGlobal(zap.New(core))
	defer SetGlobal(original)

	Debug("should not appear")
	Info("should not appear")
	Warn("should appear")
	Error("should appear")

	entries := obs.All()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries at warn level, got %d", len(entries))
	}
}
