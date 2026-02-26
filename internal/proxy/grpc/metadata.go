package grpc

import (
	"net/http"
	"strings"

	"github.com/wudi/runway/config"
)

// MetadataTransformer applies metadata mapping rules between HTTP headers and gRPC metadata.
type MetadataTransformer struct {
	requestMap  map[string]string // HTTP header → gRPC metadata (lowercased keys)
	responseMap map[string]string // gRPC metadata → HTTP header (lowercased keys)
	stripPrefix string
	passthrough map[string]bool // headers to pass through as-is
}

// NewMetadataTransformer creates a MetadataTransformer from config.
func NewMetadataTransformer(cfg config.GRPCMetadataTransforms) *MetadataTransformer {
	mt := &MetadataTransformer{
		requestMap:  make(map[string]string, len(cfg.RequestMap)),
		responseMap: make(map[string]string, len(cfg.ResponseMap)),
		stripPrefix: strings.ToLower(cfg.StripPrefix),
		passthrough: make(map[string]bool, len(cfg.Passthrough)),
	}
	for k, v := range cfg.RequestMap {
		mt.requestMap[strings.ToLower(k)] = strings.ToLower(v)
	}
	for k, v := range cfg.ResponseMap {
		mt.responseMap[strings.ToLower(k)] = v
	}
	for _, h := range cfg.Passthrough {
		mt.passthrough[strings.ToLower(h)] = true
	}
	return mt
}

// HasTransforms returns true if any transforms are configured.
func (mt *MetadataTransformer) HasTransforms() bool {
	return len(mt.requestMap) > 0 || len(mt.responseMap) > 0 ||
		mt.stripPrefix != "" || len(mt.passthrough) > 0
}

// TransformRequest applies request-side metadata transforms.
// Maps HTTP headers to gRPC metadata names, strips prefix if configured,
// and passes through designated headers.
func (mt *MetadataTransformer) TransformRequest(r *http.Request) {
	// Apply explicit request mappings: rename HTTP headers to gRPC metadata names
	for httpHeader, grpcMeta := range mt.requestMap {
		vals := r.Header.Values(http.CanonicalHeaderKey(httpHeader))
		if len(vals) == 0 {
			continue
		}
		// Set the gRPC metadata header
		canonical := http.CanonicalHeaderKey(grpcMeta)
		r.Header.Del(http.CanonicalHeaderKey(httpHeader))
		for _, v := range vals {
			r.Header.Add(canonical, v)
		}
	}

	// Strip prefix: rename headers with prefix to metadata without prefix
	if mt.stripPrefix != "" {
		for key, vals := range r.Header {
			lower := strings.ToLower(key)
			if strings.HasPrefix(lower, mt.stripPrefix) {
				newKey := lower[len(mt.stripPrefix):]
				if newKey == "" {
					continue
				}
				r.Header.Del(key)
				for _, v := range vals {
					r.Header.Add(http.CanonicalHeaderKey(newKey), v)
				}
			}
		}
	}
}

// TransformResponse applies response-side metadata transforms.
// Maps gRPC metadata names back to HTTP header names.
func (mt *MetadataTransformer) TransformResponse(w http.ResponseWriter) {
	headers := w.Header()
	for grpcMeta, httpHeader := range mt.responseMap {
		canonical := http.CanonicalHeaderKey(grpcMeta)
		vals := headers.Values(canonical)
		if len(vals) == 0 {
			continue
		}
		headers.Del(canonical)
		for _, v := range vals {
			headers.Add(httpHeader, v)
		}
	}
}
