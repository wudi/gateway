package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNew(t *testing.T) {
	tests := []struct {
		level   string
		wantLvl zapcore.Level
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
			l, closer, err := New(Config{Level: tt.level})
			if err != nil {
				t.Fatalf("New(%q) returned error: %v", tt.level, err)
			}
			if l == nil {
				t.Fatalf("New(%q) returned nil logger", tt.level)
			}
			if closer != nil {
				t.Fatalf("New(%q) returned non-nil closer for stdout", tt.level)
			}
		})
	}
}

func TestNewFileOutput(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "test.log")

	l, closer, err := New(Config{
		Level:      "info",
		Output:     logFile,
		MaxSize:    1,
		MaxBackups: 1,
		MaxAge:     1,
	})
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	if closer == nil {
		t.Fatal("expected non-nil closer for file output")
	}
	defer closer.Close()

	// Write a log entry through the logger (skip AddCallerSkip by using the logger directly)
	l.WithOptions(zap.AddCallerSkip(-1)).Info("hello file")
	l.Sync()

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("log file is empty")
	}
	if got := string(data); !strings.Contains(got, "hello file") {
		t.Errorf("log file does not contain expected message, got: %s", got)
	}
}

func TestNewStderrOutput(t *testing.T) {
	l, closer, err := New(Config{Level: "info", Output: "stderr"})
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	if l == nil {
		t.Fatal("returned nil logger")
	}
	if closer != nil {
		t.Fatal("expected nil closer for stderr output")
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

