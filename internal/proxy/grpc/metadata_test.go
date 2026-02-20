package grpc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestMetadataTransformerRequest(t *testing.T) {
	mt := NewMetadataTransformer(config.GRPCMetadataTransforms{
		RequestMap: map[string]string{
			"X-Request-Id": "x-request-id-meta",
		},
	})

	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)
	r.Header.Set("X-Request-Id", "abc123")

	mt.TransformRequest(r)

	if v := r.Header.Get("X-Request-Id-Meta"); v != "abc123" {
		t.Errorf("expected mapped header value 'abc123', got %q", v)
	}
	if v := r.Header.Get("X-Request-Id"); v != "" {
		t.Errorf("expected original header to be removed, got %q", v)
	}
}

func TestMetadataTransformerStripPrefix(t *testing.T) {
	mt := NewMetadataTransformer(config.GRPCMetadataTransforms{
		StripPrefix: "x-custom-",
	})

	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)
	r.Header.Set("X-Custom-Foo", "bar")
	r.Header.Set("X-Custom-Baz", "qux")
	r.Header.Set("Authorization", "Bearer token")

	mt.TransformRequest(r)

	if v := r.Header.Get("Foo"); v != "bar" {
		t.Errorf("expected stripped header Foo=bar, got %q", v)
	}
	if v := r.Header.Get("Baz"); v != "qux" {
		t.Errorf("expected stripped header Baz=qux, got %q", v)
	}
	if v := r.Header.Get("Authorization"); v != "Bearer token" {
		t.Errorf("expected Authorization to be preserved, got %q", v)
	}
}

func TestMetadataTransformerResponse(t *testing.T) {
	mt := NewMetadataTransformer(config.GRPCMetadataTransforms{
		ResponseMap: map[string]string{
			"x-grpc-trace-id": "X-Trace-Id",
		},
	})

	rec := httptest.NewRecorder()
	rec.Header().Set("X-Grpc-Trace-Id", "trace123")

	mt.TransformResponse(rec)

	if v := rec.Header().Get("X-Trace-Id"); v != "trace123" {
		t.Errorf("expected mapped response header X-Trace-Id=trace123, got %q", v)
	}
	if v := rec.Header().Get("X-Grpc-Trace-Id"); v != "" {
		t.Errorf("expected original response header to be removed, got %q", v)
	}
}

func TestMetadataTransformerHasTransforms(t *testing.T) {
	empty := NewMetadataTransformer(config.GRPCMetadataTransforms{})
	if empty.HasTransforms() {
		t.Error("expected empty transformer to have no transforms")
	}

	withMap := NewMetadataTransformer(config.GRPCMetadataTransforms{
		RequestMap: map[string]string{"a": "b"},
	})
	if !withMap.HasTransforms() {
		t.Error("expected transformer with request map to have transforms")
	}

	withPrefix := NewMetadataTransformer(config.GRPCMetadataTransforms{
		StripPrefix: "x-",
	})
	if !withPrefix.HasTransforms() {
		t.Error("expected transformer with strip prefix to have transforms")
	}
}

func TestMetadataTransformerPassthrough(t *testing.T) {
	mt := NewMetadataTransformer(config.GRPCMetadataTransforms{
		Passthrough: []string{"X-Custom-Header"},
	})

	if !mt.HasTransforms() {
		t.Error("expected transformer with passthrough to have transforms")
	}

	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)
	r.Header.Set("X-Custom-Header", "value")

	mt.TransformRequest(r)

	// Passthrough headers should remain as-is
	if v := r.Header.Get("X-Custom-Header"); v != "value" {
		t.Errorf("expected passthrough header preserved, got %q", v)
	}
}

func TestMetadataTransformerRequestMultiValues(t *testing.T) {
	mt := NewMetadataTransformer(config.GRPCMetadataTransforms{
		RequestMap: map[string]string{
			"X-Multi": "x-multi-meta",
		},
	})

	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)
	r.Header.Add("X-Multi", "val1")
	r.Header.Add("X-Multi", "val2")

	mt.TransformRequest(r)

	vals := r.Header[http.CanonicalHeaderKey("X-Multi-Meta")]
	if len(vals) != 2 || vals[0] != "val1" || vals[1] != "val2" {
		t.Errorf("expected multi-value mapped header, got %v", vals)
	}
}
