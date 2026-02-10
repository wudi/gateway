package thrift

import (
	"net/http"
	"strings"

	"github.com/apache/thrift/lib/go/thrift"
)

// ThriftExceptionToHTTP maps Thrift exception types to HTTP status codes.
func ThriftExceptionToHTTP(err error) int {
	// Check for TApplicationException.
	if ae, ok := err.(thrift.TApplicationException); ok {
		switch ae.TypeId() {
		case thrift.UNKNOWN_METHOD:
			return http.StatusNotFound
		case thrift.INVALID_MESSAGE_TYPE_EXCEPTION, thrift.PROTOCOL_ERROR:
			return http.StatusBadRequest
		case thrift.WRONG_METHOD_NAME, thrift.BAD_SEQUENCE_ID:
			return http.StatusBadRequest
		case thrift.INTERNAL_ERROR, thrift.MISSING_RESULT:
			return http.StatusInternalServerError
		default:
			return http.StatusInternalServerError
		}
	}

	// Check for TTransportException.
	if _, ok := err.(thrift.TTransportException); ok {
		return http.StatusBadGateway
	}

	// IDL-declared exceptions are wrapped with "thrift exception " prefix.
	if strings.HasPrefix(err.Error(), "thrift exception ") {
		return http.StatusUnprocessableEntity
	}

	return http.StatusInternalServerError
}
