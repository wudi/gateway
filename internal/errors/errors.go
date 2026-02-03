package errors

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// GatewayError represents an error that can be returned to clients
type GatewayError struct {
	Code       int    `json:"code"`
	Message    string `json:"message"`
	Details    string `json:"details,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	underlying error
}

func (e *GatewayError) Error() string {
	if e.underlying != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.underlying)
	}
	return e.Message
}

func (e *GatewayError) Unwrap() error {
	return e.underlying
}

// WriteJSON writes the error as JSON to the response
func (e *GatewayError) WriteJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Code)
	json.NewEncoder(w).Encode(e)
}

// Common errors
var (
	ErrNotFound = &GatewayError{
		Code:    http.StatusNotFound,
		Message: "Not Found",
	}

	ErrMethodNotAllowed = &GatewayError{
		Code:    http.StatusMethodNotAllowed,
		Message: "Method Not Allowed",
	}

	ErrUnauthorized = &GatewayError{
		Code:    http.StatusUnauthorized,
		Message: "Unauthorized",
	}

	ErrForbidden = &GatewayError{
		Code:    http.StatusForbidden,
		Message: "Forbidden",
	}

	ErrTooManyRequests = &GatewayError{
		Code:    http.StatusTooManyRequests,
		Message: "Too Many Requests",
	}

	ErrBadGateway = &GatewayError{
		Code:    http.StatusBadGateway,
		Message: "Bad Gateway",
	}

	ErrServiceUnavailable = &GatewayError{
		Code:    http.StatusServiceUnavailable,
		Message: "Service Unavailable",
	}

	ErrGatewayTimeout = &GatewayError{
		Code:    http.StatusGatewayTimeout,
		Message: "Gateway Timeout",
	}

	ErrBadRequest = &GatewayError{
		Code:    http.StatusBadRequest,
		Message: "Bad Request",
	}

	ErrInternalServer = &GatewayError{
		Code:    http.StatusInternalServerError,
		Message: "Internal Server Error",
	}
)

// New creates a new GatewayError
func New(code int, message string) *GatewayError {
	return &GatewayError{
		Code:    code,
		Message: message,
	}
}

// Wrap wraps an error with additional context
func Wrap(err error, code int, message string) *GatewayError {
	return &GatewayError{
		Code:       code,
		Message:    message,
		underlying: err,
	}
}

// WithDetails adds details to the error
func (e *GatewayError) WithDetails(details string) *GatewayError {
	return &GatewayError{
		Code:       e.Code,
		Message:    e.Message,
		Details:    details,
		RequestID:  e.RequestID,
		underlying: e.underlying,
	}
}

// WithRequestID adds a request ID to the error
func (e *GatewayError) WithRequestID(requestID string) *GatewayError {
	return &GatewayError{
		Code:       e.Code,
		Message:    e.Message,
		Details:    e.Details,
		RequestID:  requestID,
		underlying: e.underlying,
	}
}

// IsGatewayError checks if an error is a GatewayError
func IsGatewayError(err error) (*GatewayError, bool) {
	if ge, ok := err.(*GatewayError); ok {
		return ge, true
	}
	return nil, false
}
