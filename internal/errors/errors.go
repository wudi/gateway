package errors

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// RunwayError represents an error that can be returned to clients
type RunwayError struct {
	Code       int    `json:"code"`
	Message    string `json:"message"`
	Details    string `json:"details,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	underlying error
}

func (e *RunwayError) Error() string {
	if e.underlying != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.underlying)
	}
	return e.Message
}

func (e *RunwayError) Unwrap() error {
	return e.underlying
}

// WriteJSON writes the error as JSON to the response.
// For base errors (no details/requestID), uses pre-serialized JSON to avoid allocations.
func (e *RunwayError) WriteJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Code)
	if pre, ok := preSerialized[e]; ok {
		w.Write(pre)
		return
	}
	json.NewEncoder(w).Encode(e)
}

// Common errors
var (
	ErrNotFound = &RunwayError{
		Code:    http.StatusNotFound,
		Message: "Not Found",
	}

	ErrMethodNotAllowed = &RunwayError{
		Code:    http.StatusMethodNotAllowed,
		Message: "Method Not Allowed",
	}

	ErrUnauthorized = &RunwayError{
		Code:    http.StatusUnauthorized,
		Message: "Unauthorized",
	}

	ErrForbidden = &RunwayError{
		Code:    http.StatusForbidden,
		Message: "Forbidden",
	}

	ErrTooManyRequests = &RunwayError{
		Code:    http.StatusTooManyRequests,
		Message: "Too Many Requests",
	}

	ErrBadGateway = &RunwayError{
		Code:    http.StatusBadGateway,
		Message: "Bad Gateway",
	}

	ErrServiceUnavailable = &RunwayError{
		Code:    http.StatusServiceUnavailable,
		Message: "Service Unavailable",
	}

	ErrGatewayTimeout = &RunwayError{
		Code:    http.StatusGatewayTimeout,
		Message: "Gateway Timeout",
	}

	ErrBadRequest = &RunwayError{
		Code:    http.StatusBadRequest,
		Message: "Bad Request",
	}

	ErrInternalServer = &RunwayError{
		Code:    http.StatusInternalServerError,
		Message: "Internal Server Error",
	}

	ErrRequestEntityTooLarge = &RunwayError{
		Code:    http.StatusRequestEntityTooLarge,
		Message: "Request Entity Too Large",
	}
)

// preSerialized holds JSON-encoded bytes for base error singletons.
var preSerialized map[*RunwayError][]byte

func init() {
	bases := []*RunwayError{
		ErrNotFound, ErrMethodNotAllowed, ErrUnauthorized, ErrForbidden,
		ErrTooManyRequests, ErrBadGateway, ErrServiceUnavailable,
		ErrGatewayTimeout, ErrBadRequest, ErrInternalServer,
		ErrRequestEntityTooLarge,
	}
	preSerialized = make(map[*RunwayError][]byte, len(bases))
	for _, e := range bases {
		b, _ := json.Marshal(e)
		b = append(b, '\n') // match json.Encoder behavior
		preSerialized[e] = b
	}
}

// New creates a new RunwayError
func New(code int, message string) *RunwayError {
	return &RunwayError{
		Code:    code,
		Message: message,
	}
}

// Wrap wraps an error with additional context
func Wrap(err error, code int, message string) *RunwayError {
	return &RunwayError{
		Code:       code,
		Message:    message,
		underlying: err,
	}
}

// WithDetails adds details to the error
func (e *RunwayError) WithDetails(details string) *RunwayError {
	return &RunwayError{
		Code:       e.Code,
		Message:    e.Message,
		Details:    details,
		RequestID:  e.RequestID,
		underlying: e.underlying,
	}
}

// WithRequestID adds a request ID to the error
func (e *RunwayError) WithRequestID(requestID string) *RunwayError {
	return &RunwayError{
		Code:       e.Code,
		Message:    e.Message,
		Details:    e.Details,
		RequestID:  requestID,
		underlying: e.underlying,
	}
}

// IsRunwayError checks if an error is a RunwayError
func IsRunwayError(err error) (*RunwayError, bool) {
	if ge, ok := err.(*RunwayError); ok {
		return ge, true
	}
	return nil, false
}
