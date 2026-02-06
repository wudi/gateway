package logging

import (
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	globalLogger *zap.Logger
	globalMu     sync.RWMutex
)

func init() {
	// Default to a production logger until SetGlobal is called
	globalLogger, _ = zap.NewProduction()
}

// New creates a new zap logger from a level string.
func New(level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.TimeKey = "timestamp"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	var lvl zapcore.Level
	switch level {
	case "debug":
		lvl = zapcore.DebugLevel
	case "warn":
		lvl = zapcore.WarnLevel
	case "error":
		lvl = zapcore.ErrorLevel
	default:
		lvl = zapcore.InfoLevel
	}
	cfg.Level = zap.NewAtomicLevelAt(lvl)

	return cfg.Build(
		zap.AddCallerSkip(1), // Skip one level to account for our wrapper functions
	)
}

// Global returns the global logger.
func Global() *zap.Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalLogger
}

// SetGlobal sets the global logger.
func SetGlobal(l *zap.Logger) {
	globalMu.Lock()
	globalLogger = l
	globalMu.Unlock()
}

// Info logs at info level using the global logger.
func Info(msg string, fields ...zap.Field) {
	Global().Info(msg, fields...)
}

// Warn logs at warn level using the global logger.
func Warn(msg string, fields ...zap.Field) {
	Global().Warn(msg, fields...)
}

// Error logs at error level using the global logger.
func Error(msg string, fields ...zap.Field) {
	Global().Error(msg, fields...)
}

// Debug logs at debug level using the global logger.
func Debug(msg string, fields ...zap.Field) {
	Global().Debug(msg, fields...)
}

// With creates a child logger with additional fields.
func With(fields ...zap.Field) *zap.Logger {
	return Global().With(fields...)
}

// Sync flushes any buffered log entries.
func Sync() {
	Global().Sync()
}
