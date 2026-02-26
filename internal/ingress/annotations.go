package ingress

import (
	"strconv"
	"time"
)

// Annotation prefix for runway-specific annotations.
const annotationPrefix = "runway.wudi.io/"

// Annotation keys.
const (
	AnnRateLimit      = annotationPrefix + "rate-limit"
	AnnTimeout        = annotationPrefix + "timeout"
	AnnRetryMax       = annotationPrefix + "retry-max"
	AnnCORSEnabled    = annotationPrefix + "cors-enabled"
	AnnCircuitBreaker = annotationPrefix + "circuit-breaker"
	AnnAuthRequired   = annotationPrefix + "auth-required"
	AnnCacheEnabled   = annotationPrefix + "cache-enabled"
	AnnLoadBalancer   = annotationPrefix + "load-balancer"
	AnnStripPrefix    = annotationPrefix + "strip-prefix"
	AnnUpstreamMode   = annotationPrefix + "upstream-mode"
)

// Upstream modes.
const (
	UpstreamModeEndpointSlice = "endpointslice"
	UpstreamModeClusterIP     = "clusterip"
)

// AnnotationParser extracts typed values from Kubernetes annotations.
type AnnotationParser struct {
	annotations map[string]string
}

// NewAnnotationParser creates a parser for the given annotation map.
func NewAnnotationParser(annotations map[string]string) *AnnotationParser {
	return &AnnotationParser{annotations: annotations}
}

// GetString returns the annotation value or the default.
func (p *AnnotationParser) GetString(key, defaultVal string) string {
	if v, ok := p.annotations[key]; ok && v != "" {
		return v
	}
	return defaultVal
}

// GetBool returns the annotation value as a bool, or the default.
func (p *AnnotationParser) GetBool(key string, defaultVal bool) bool {
	v, ok := p.annotations[key]
	if !ok || v == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return defaultVal
	}
	return b
}

// GetInt returns the annotation value as an int, or the default.
func (p *AnnotationParser) GetInt(key string, defaultVal int) int {
	v, ok := p.annotations[key]
	if !ok || v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return i
}

// GetDuration returns the annotation value as a time.Duration, or the default.
func (p *AnnotationParser) GetDuration(key string, defaultVal time.Duration) time.Duration {
	v, ok := p.annotations[key]
	if !ok || v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultVal
	}
	return d
}

// Has returns true if the annotation key is present.
func (p *AnnotationParser) Has(key string) bool {
	_, ok := p.annotations[key]
	return ok
}
