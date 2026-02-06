package grpc

import (
	"testing"

	"google.golang.org/grpc/codes"
)

func TestGRPCStatusToHTTP(t *testing.T) {
	tests := []struct {
		code     codes.Code
		expected int
	}{
		{codes.OK, 200},
		{codes.Canceled, 499},
		{codes.Unknown, 500},
		{codes.InvalidArgument, 400},
		{codes.DeadlineExceeded, 504},
		{codes.NotFound, 404},
		{codes.AlreadyExists, 409},
		{codes.PermissionDenied, 403},
		{codes.ResourceExhausted, 429},
		{codes.FailedPrecondition, 400},
		{codes.Aborted, 409},
		{codes.OutOfRange, 400},
		{codes.Unimplemented, 501},
		{codes.Internal, 500},
		{codes.Unavailable, 503},
		{codes.DataLoss, 500},
		{codes.Unauthenticated, 401},
	}

	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			result := GRPCStatusToHTTP(tt.code)
			if result != tt.expected {
				t.Errorf("GRPCStatusToHTTP(%v) = %d, want %d", tt.code, result, tt.expected)
			}
		})
	}
}

func TestHTTPToGRPCStatus(t *testing.T) {
	tests := []struct {
		httpCode int
		expected codes.Code
	}{
		{200, codes.OK},
		{201, codes.OK},
		{204, codes.OK},
		{400, codes.InvalidArgument},
		{401, codes.Unauthenticated},
		{403, codes.PermissionDenied},
		{404, codes.NotFound},
		{409, codes.AlreadyExists},
		{429, codes.ResourceExhausted},
		{499, codes.Canceled},
		{500, codes.Internal},
		{501, codes.Unimplemented},
		{503, codes.Unavailable},
		{504, codes.DeadlineExceeded},
		{418, codes.InvalidArgument}, // Other 4xx
		{599, codes.Unknown},         // Other 5xx (mapped to Unknown)
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.httpCode)), func(t *testing.T) {
			result := HTTPToGRPCStatus(tt.httpCode)
			if result != tt.expected {
				t.Errorf("HTTPToGRPCStatus(%d) = %v, want %v", tt.httpCode, result, tt.expected)
			}
		})
	}
}
