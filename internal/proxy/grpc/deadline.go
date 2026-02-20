package grpc

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ParseGRPCTimeout parses the grpc-timeout header value.
// Format: <amount><unit> where unit is H(ours), M(inutes), S(econds),
// m(illis), u(micros), n(anos).
func ParseGRPCTimeout(val string) (time.Duration, bool) {
	if val == "" {
		return 0, false
	}
	if len(val) < 2 {
		return 0, false
	}

	unit := val[len(val)-1]
	numStr := val[:len(val)-1]
	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil || num < 0 {
		return 0, false
	}

	switch unit {
	case 'H':
		return time.Duration(num) * time.Hour, true
	case 'M':
		return time.Duration(num) * time.Minute, true
	case 'S':
		return time.Duration(num) * time.Second, true
	case 'm':
		return time.Duration(num) * time.Millisecond, true
	case 'u':
		return time.Duration(num) * time.Microsecond, true
	case 'n':
		return time.Duration(num) * time.Nanosecond, true
	default:
		return 0, false
	}
}

// PropagateDeadline reads the grpc-timeout header and sets a context deadline.
// Returns the new request with deadline context and a cancel func (caller must defer).
// If no timeout header or deadline propagation is disabled, returns the original request and a no-op cancel.
func PropagateDeadline(r *http.Request) (*http.Request, context.CancelFunc) {
	timeout, ok := ParseGRPCTimeout(r.Header.Get("grpc-timeout"))
	if !ok {
		return r, func() {}
	}

	// If context already has a deadline, use the shorter one
	if deadline, hasDeadline := r.Context().Deadline(); hasDeadline {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	return r.WithContext(ctx), cancel
}

// FormatGRPCTimeout formats a duration as a grpc-timeout header value.
func FormatGRPCTimeout(d time.Duration) string {
	if d <= 0 {
		return "0n"
	}
	// Use the largest unit that represents the duration without overflow
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "H"
	case d >= time.Minute && d%time.Minute == 0:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "M"
	case d >= time.Second && d%time.Second == 0:
		return strconv.FormatInt(int64(d/time.Second), 10) + "S"
	case d >= time.Millisecond && d%time.Millisecond == 0:
		return strconv.FormatInt(int64(d/time.Millisecond), 10) + "m"
	case d >= time.Microsecond && d%time.Microsecond == 0:
		return strconv.FormatInt(int64(d/time.Microsecond), 10) + "u"
	default:
		return strconv.FormatInt(int64(d/time.Nanosecond), 10) + "n"
	}
}

// SetRemainingTimeout updates the grpc-timeout header to reflect remaining context deadline.
func SetRemainingTimeout(r *http.Request) {
	deadline, ok := r.Context().Deadline()
	if !ok {
		return
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		remaining = 0
	}
	r.Header.Set("grpc-timeout", FormatGRPCTimeout(remaining))
}

// IsGRPCTimeoutHeader checks if a header key is the grpc-timeout header.
func IsGRPCTimeoutHeader(key string) bool {
	return strings.EqualFold(key, "grpc-timeout")
}
