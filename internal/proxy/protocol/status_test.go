package protocol

import (
	"testing"

	"google.golang.org/grpc/codes"
)

func TestGRPCStatusToHTTP(t *testing.T) {
	tests := []struct {
		name     string
		code     codes.Code
		expected int
	}{
		{"OK", codes.OK, 200},
		{"Canceled", codes.Canceled, 499},
		{"Unknown", codes.Unknown, 500},
		{"InvalidArgument", codes.InvalidArgument, 400},
		{"DeadlineExceeded", codes.DeadlineExceeded, 504},
		{"NotFound", codes.NotFound, 404},
		{"AlreadyExists", codes.AlreadyExists, 409},
		{"PermissionDenied", codes.PermissionDenied, 403},
		{"ResourceExhausted", codes.ResourceExhausted, 429},
		{"FailedPrecondition", codes.FailedPrecondition, 400},
		{"Aborted", codes.Aborted, 409},
		{"OutOfRange", codes.OutOfRange, 400},
		{"Unimplemented", codes.Unimplemented, 501},
		{"Internal", codes.Internal, 500},
		{"Unavailable", codes.Unavailable, 503},
		{"DataLoss", codes.DataLoss, 500},
		{"Unauthenticated", codes.Unauthenticated, 401},
		{"unknown_code_99", codes.Code(99), 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GRPCStatusToHTTP(tt.code)
			if result != tt.expected {
				t.Errorf("GRPCStatusToHTTP(%v) = %d, want %d", tt.code, result, tt.expected)
			}
		})
	}
}

func TestHTTPToGRPCStatus(t *testing.T) {
	tests := []struct {
		name     string
		httpCode int
		expected codes.Code
	}{
		{"200", 200, codes.OK},
		{"201", 201, codes.OK},
		{"202", 202, codes.OK},
		{"204", 204, codes.OK},
		{"400", 400, codes.InvalidArgument},
		{"401", 401, codes.Unauthenticated},
		{"403", 403, codes.PermissionDenied},
		{"404", 404, codes.NotFound},
		{"409", 409, codes.AlreadyExists},
		{"429", 429, codes.ResourceExhausted},
		{"499", 499, codes.Canceled},
		{"500", 500, codes.Internal},
		{"501", 501, codes.Unimplemented},
		{"503", 503, codes.Unavailable},
		{"504", 504, codes.DeadlineExceeded},
		{"418_other_4xx", 418, codes.InvalidArgument},
		{"599_other_5xx", 599, codes.Unknown},
		{"302_redirect_returns_unknown", 302, codes.Unknown},
		{"100_informational_returns_unknown", 100, codes.Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HTTPToGRPCStatus(tt.httpCode)
			if result != tt.expected {
				t.Errorf("HTTPToGRPCStatus(%d) = %v, want %v", tt.httpCode, result, tt.expected)
			}
		})
	}
}
