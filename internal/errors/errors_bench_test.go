package errors

import (
	"net/http/httptest"
	"testing"
)

func BenchmarkWriteJSON_Base(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		ErrNotFound.WriteJSON(w)
	}
}

func BenchmarkWriteJSON_WithDetails(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		ErrNotFound.WithDetails("resource not found").WriteJSON(w)
	}
}
