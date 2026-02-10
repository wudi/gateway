package thrift

import (
	"errors"
	"net/http"
	"testing"

	athrift "github.com/apache/thrift/lib/go/thrift"
)

func TestThriftExceptionToHTTP(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{
			name:     "unknown_method",
			err:      athrift.NewTApplicationException(athrift.UNKNOWN_METHOD, "method not found"),
			expected: http.StatusNotFound,
		},
		{
			name:     "invalid_message_type",
			err:      athrift.NewTApplicationException(athrift.INVALID_MESSAGE_TYPE_EXCEPTION, "bad message"),
			expected: http.StatusBadRequest,
		},
		{
			name:     "protocol_error",
			err:      athrift.NewTApplicationException(athrift.PROTOCOL_ERROR, "protocol error"),
			expected: http.StatusBadRequest,
		},
		{
			name:     "wrong_method_name",
			err:      athrift.NewTApplicationException(athrift.WRONG_METHOD_NAME, "wrong method"),
			expected: http.StatusBadRequest,
		},
		{
			name:     "internal_error",
			err:      athrift.NewTApplicationException(athrift.INTERNAL_ERROR, "internal error"),
			expected: http.StatusInternalServerError,
		},
		{
			name:     "missing_result",
			err:      athrift.NewTApplicationException(athrift.MISSING_RESULT, "missing result"),
			expected: http.StatusInternalServerError,
		},
		{
			name:     "transport_exception",
			err:      athrift.NewTTransportException(athrift.NOT_OPEN, "connection closed"),
			expected: http.StatusBadGateway,
		},
		{
			name:     "idl_declared_exception",
			err:      errors.New("thrift exception not_found: user not found"),
			expected: http.StatusUnprocessableEntity,
		},
		{
			name:     "generic_error",
			err:      errors.New("something went wrong"),
			expected: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := ThriftExceptionToHTTP(tt.err)
			if status != tt.expected {
				t.Errorf("got %d, want %d", status, tt.expected)
			}
		})
	}
}
