package logging

import (
	"io"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	globalLogger *zap.Logger
	globalMu     sync.RWMutex
)

func init() {
	// Default to a production logger until SetGlobal is called
	globalLogger, _ = zap.NewProduction()
}

// Config holds parameters for creating a logger.
type Config struct {
	Level      string // "debug", "info", "warn", "error"
	Output     string // "stdout", "stderr", or file path
	MaxSize    int    // max megabytes before rotation
	MaxBackups int    // old rotated files to keep
	MaxAge     int    // days to retain old files
	Compress   bool   // gzip rotated files
	LocalTime  bool   // use local time in backup filenames
}

// New creates a new zap logger from a Config.
// When Output is a file path, the returned io.Closer must be closed on shutdown
// to flush and close the underlying log file. For stdout/stderr the closer is nil.
func New(cfg Config) (*zap.Logger, io.Closer, error) {
	var lvl zapcore.Level
	switch cfg.Level {
	case "debug":
		lvl = zapcore.DebugLevel
	case "warn":
		lvl = zapcore.WarnLevel
	case "error":
		lvl = zapcore.ErrorLevel
	default:
		lvl = zapcore.InfoLevel
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "time"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoder := zapcore.NewJSONEncoder(encCfg)

	var ws zapcore.WriteSyncer
	var closer io.Closer

	switch cfg.Output {
	case "", "stdout":
		ws = zapcore.AddSync(os.Stdout)
	case "stderr":
		ws = zapcore.AddSync(os.Stderr)
	default:
		lj := &lumberjack.Logger{
			Filename:   cfg.Output,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
			LocalTime:  cfg.LocalTime,
		}
		ws = zapcore.AddSync(lj)
		closer = lj
	}

	core := zapcore.NewCore(encoder, ws, lvl)
	logger := zap.New(core,
		zap.AddCaller(),
		zap.AddCallerSkip(1),
	)

	return logger, closer, nil
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
