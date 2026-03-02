package protocol

import (
	"google.golang.org/grpc/codes"
)

// GRPCStatusToHTTP maps gRPC status codes to HTTP status codes.
// Reference: https://github.com/grpc/grpc/blob/master/doc/http-grpc-status-mapping.md
func GRPCStatusToHTTP(code codes.Code) int {
	switch code {
	case codes.OK:
		return 200
	case codes.Canceled:
		return 499 // Client Closed Request
	case codes.Unknown:
		return 500
	case codes.InvalidArgument:
		return 400
	case codes.DeadlineExceeded:
		return 504
	case codes.NotFound:
		return 404
	case codes.AlreadyExists:
		return 409
	case codes.PermissionDenied:
		return 403
	case codes.ResourceExhausted:
		return 429
	case codes.FailedPrecondition:
		return 400
	case codes.Aborted:
		return 409
	case codes.OutOfRange:
		return 400
	case codes.Unimplemented:
		return 501
	case codes.Internal:
		return 500
	case codes.Unavailable:
		return 503
	case codes.DataLoss:
		return 500
	case codes.Unauthenticated:
		return 401
	default:
		return 500
	}
}

// HTTPToGRPCStatus maps HTTP status codes to gRPC status codes.
func HTTPToGRPCStatus(httpCode int) codes.Code {
	switch httpCode {
	case 200, 201, 202, 204:
		return codes.OK
	case 400:
		return codes.InvalidArgument
	case 401:
		return codes.Unauthenticated
	case 403:
		return codes.PermissionDenied
	case 404:
		return codes.NotFound
	case 409:
		return codes.AlreadyExists
	case 429:
		return codes.ResourceExhausted
	case 499:
		return codes.Canceled
	case 500:
		return codes.Internal
	case 501:
		return codes.Unimplemented
	case 503:
		return codes.Unavailable
	case 504:
		return codes.DeadlineExceeded
	default:
		if httpCode >= 400 && httpCode < 500 {
			return codes.InvalidArgument
		}
		return codes.Unknown
	}
}
