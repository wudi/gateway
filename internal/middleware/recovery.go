package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/internal/logging"
	"go.uber.org/zap"
)

// RecoveryConfig configures the recovery middleware
type RecoveryConfig struct {
	// PrintStack prints the stack trace when a panic occurs
	PrintStack bool
	// LogFunc is called when a panic occurs
	LogFunc func(err interface{}, stack []byte)
}

// DefaultRecoveryConfig provides default recovery settings
var DefaultRecoveryConfig = RecoveryConfig{
	PrintStack: true,
	LogFunc:    defaultLogFunc,
}

func defaultLogFunc(err interface{}, stack []byte) {
	logging.Error("Panic recovered",
		zap.Any("error", err),
		zap.ByteString("stack", stack),
	)
}

// Recovery creates a panic recovery middleware
func Recovery() Middleware {
	return RecoveryWithConfig(DefaultRecoveryConfig)
}

// RecoveryWithConfig creates a recovery middleware with custom config
func RecoveryWithConfig(cfg RecoveryConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					// Get stack trace
					var stack []byte
					if cfg.PrintStack {
						stack = debug.Stack()
					}

					// Log the panic
					if cfg.LogFunc != nil {
						cfg.LogFunc(err, stack)
					}

					// Return 500 error
					runwayErr := errors.ErrInternalServer.WithDetails(fmt.Sprintf("panic: %v", err))

					// Try to get request ID from header
					if reqID := w.Header().Get("X-Request-ID"); reqID != "" {
						runwayErr = runwayErr.WithRequestID(reqID)
					}

					runwayErr.WriteJSON(w)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// RecoveryWithWriter creates a recovery middleware that writes to a custom writer
func RecoveryWithWriter(logFunc func(format string, args ...interface{})) Middleware {
	return RecoveryWithConfig(RecoveryConfig{
		PrintStack: true,
		LogFunc: func(err interface{}, stack []byte) {
			logFunc("[PANIC] %v\n%s", err, stack)
		},
	})
}
