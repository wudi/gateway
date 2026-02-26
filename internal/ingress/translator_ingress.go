package ingress

import (
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"

	"github.com/wudi/runway/config"
)

const (
	// Standard Kubernetes annotation for ingress class (deprecated but still widely used).
	annIngressClass = "kubernetes.io/ingress.class"
)

// translateIngresses converts all Ingress resources in the store to routes and listeners.
func (t *Translator) translateIngresses() ([]config.RouteConfig, []config.ListenerConfig, []string) {
	var routes []config.RouteConfig
	var warnings []string
	tlsEntries := make(map[string][]TLSEntry) // host → TLS entries

	for _, ing := range t.store.ListIngresses() {
		if !t.shouldProcessIngress(ing) {
			continue
		}
		ann := NewAnnotationParser(ing.Annotations)

		// Collect TLS entries
		for _, tls := range ing.Spec.TLS {
			entry := TLSEntry{
				SecretName: tls.SecretName,
				Hosts:      tls.Hosts,
			}
			for _, host := range tls.Hosts {
				tlsEntries[host] = append(tlsEntries[host], entry)
			}
			if len(tls.Hosts) == 0 {
				tlsEntries["*"] = append(tlsEntries["*"], entry)
			}
		}

		// Translate rules
		for i, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for j, path := range rule.HTTP.Paths {
				route := t.ingressPathToRoute(ing, ann, rule.Host, path, i, j)
				routes = append(routes, route)
			}
		}

		// Default backend
		if ing.Spec.DefaultBackend != nil {
			route := t.ingressDefaultBackendToRoute(ing, ann)
			routes = append(routes, route)
		}
	}

	// Build TLS listeners from collected entries
	listeners := t.buildIngressTLSListeners(tlsEntries, &warnings)

	return routes, listeners, warnings
}

// shouldProcessIngress returns true if the Ingress matches our ingress class.
func (t *Translator) shouldProcessIngress(ing *networkingv1.Ingress) bool {
	// Check spec.ingressClassName
	if ing.Spec.IngressClassName != nil {
		return *ing.Spec.IngressClassName == t.ingressClass
	}
	// Check deprecated annotation
	if v, ok := ing.Annotations[annIngressClass]; ok {
		return v == t.ingressClass
	}
	// No class specified — claim if --watch-ingress-without-class is set
	return t.watchWithoutClass
}

// ingressPathToRoute converts an Ingress path rule to a RouteConfig.
func (t *Translator) ingressPathToRoute(
	ing *networkingv1.Ingress,
	ann *AnnotationParser,
	host string,
	path networkingv1.HTTPIngressPath,
	ruleIdx, pathIdx int,
) config.RouteConfig {
	routeID := fmt.Sprintf("ing-%s-%s-%d-%d", ing.Namespace, ing.Name, ruleIdx, pathIdx)

	pathStr := "/"
	if path.Path != "" {
		pathStr = path.Path
	}

	isPrefix := false
	if path.PathType != nil && *path.PathType == networkingv1.PathTypePrefix {
		isPrefix = true
	}

	rc := config.RouteConfig{
		ID:         routeID,
		Path:       pathStr,
		PathPrefix: isPrefix,
		StripPrefix: ann.GetBool(AnnStripPrefix, false),
	}

	// Host matching
	if host != "" {
		rc.Match.Domains = []string{host}
	}

	// Resolve backends
	if path.Backend.Service != nil {
		backends := t.resolveIngressBackend(ing.Namespace, path.Backend.Service, ann)
		rc.Backends = backends
	}

	// Apply annotations
	t.applyAnnotations(&rc, ann)

	return rc
}

// ingressDefaultBackendToRoute creates a catch-all route from the Ingress default backend.
func (t *Translator) ingressDefaultBackendToRoute(ing *networkingv1.Ingress, ann *AnnotationParser) config.RouteConfig {
	routeID := fmt.Sprintf("ing-%s-%s-default", ing.Namespace, ing.Name)
	rc := config.RouteConfig{
		ID:         routeID,
		Path:       "/",
		PathPrefix: true,
	}
	if ing.Spec.DefaultBackend.Service != nil {
		rc.Backends = t.resolveIngressBackend(ing.Namespace, ing.Spec.DefaultBackend.Service, ann)
	}
	t.applyAnnotations(&rc, ann)
	return rc
}

// resolveIngressBackend resolves an IngressServiceBackend to BackendConfigs.
func (t *Translator) resolveIngressBackend(namespace string, svcBackend *networkingv1.IngressServiceBackend, ann *AnnotationParser) []config.BackendConfig {
	svcName := svcBackend.Name
	port := resolveIngressPort(svcBackend.Port)
	upstreamMode := ann.GetString(AnnUpstreamMode, UpstreamModeEndpointSlice)

	// Check for ExternalName service
	svcKey := keyOf(namespace, svcName)
	if svc, ok := t.store.GetService(svcKey); ok && svc.Spec.Type == "ExternalName" {
		url := fmt.Sprintf("http://%s:%d", svc.Spec.ExternalName, port)
		return []config.BackendConfig{{URL: url}}
	}

	if upstreamMode == UpstreamModeClusterIP {
		url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", svcName, namespace, port)
		return []config.BackendConfig{{URL: url}}
	}

	// EndpointSlice mode (default): resolve to individual pod IPs
	return t.resolveEndpointSliceBackends(namespace, svcName, port)
}

// resolveEndpointSliceBackends resolves EndpointSlices for a service to BackendConfigs.
func (t *Translator) resolveEndpointSliceBackends(namespace, svcName string, port int32) []config.BackendConfig {
	slices := t.store.GetEndpointSlicesForService(namespace, svcName)
	var backends []config.BackendConfig

	for _, es := range slices {
		targetPort := port
		// Find matching port in EndpointSlice
		for _, ep := range es.Ports {
			if ep.Port != nil {
				if port == 0 || (ep.Port != nil && *ep.Port == port) {
					targetPort = *ep.Port
					break
				}
			}
		}

		for _, endpoint := range es.Endpoints {
			if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
				continue
			}
			for _, addr := range endpoint.Addresses {
				url := fmt.Sprintf("http://%s:%d", addr, targetPort)
				backends = append(backends, config.BackendConfig{URL: url})
			}
		}
	}

	// If no endpoints found, fall back to ClusterIP
	if len(backends) == 0 {
		url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", svcName, namespace, port)
		return []config.BackendConfig{{URL: url}}
	}

	return backends
}

func resolveIngressPort(port networkingv1.ServiceBackendPort) int32 {
	if port.Number != 0 {
		return port.Number
	}
	// Named port — we can't resolve without endpoint information; use 80 as default.
	return 80
}

// applyAnnotations applies runway-specific annotations to a RouteConfig.
func (t *Translator) applyAnnotations(rc *config.RouteConfig, ann *AnnotationParser) {
	if ann.Has(AnnRateLimit) {
		rc.RateLimit.Enabled = true
		rc.RateLimit.Rate = ann.GetInt(AnnRateLimit, 0)
	}
	if ann.Has(AnnTimeout) {
		rc.TimeoutPolicy.Request = ann.GetDuration(AnnTimeout, 0)
	}
	if ann.Has(AnnRetryMax) {
		rc.Retries = ann.GetInt(AnnRetryMax, 0)
	}
	if ann.Has(AnnCORSEnabled) {
		rc.CORS.Enabled = ann.GetBool(AnnCORSEnabled, false)
	}
	if ann.Has(AnnCircuitBreaker) {
		rc.CircuitBreaker.Enabled = ann.GetBool(AnnCircuitBreaker, false)
	}
	if ann.Has(AnnAuthRequired) {
		rc.Auth.Required = ann.GetBool(AnnAuthRequired, false)
	}
	if ann.Has(AnnCacheEnabled) {
		rc.Cache.Enabled = ann.GetBool(AnnCacheEnabled, false)
	}
	if ann.Has(AnnLoadBalancer) {
		lb := ann.GetString(AnnLoadBalancer, "")
		rc.LoadBalancer = lb
	}
}

// buildIngressTLSListeners creates HTTPS listeners from Ingress TLS entries.
func (t *Translator) buildIngressTLSListeners(tlsEntries map[string][]TLSEntry, warnings *[]string) []config.ListenerConfig {
	if len(tlsEntries) == 0 {
		return nil
	}

	// Resolve TLS secrets per-ingress (secrets are namespace-scoped).
	var pairs []config.TLSCertPair
	for _, ing := range t.store.ListIngresses() {
		if !t.shouldProcessIngress(ing) {
			continue
		}
		for _, tls := range ing.Spec.TLS {
			entry := TLSEntry{SecretName: tls.SecretName, Hosts: tls.Hosts}
			p, w2 := ResolveTLSCertPairs(t.store, ing.Namespace, []TLSEntry{entry})
			*warnings = append(*warnings, w2...)
			pairs = append(pairs, p...)
		}
	}

	if len(pairs) == 0 {
		return nil
	}

	return []config.ListenerConfig{
		{
			ID:       "ingress-https",
			Address:  fmt.Sprintf(":%d", t.defaultHTTPSPort),
			Protocol: config.ProtocolHTTP,
			TLS: config.TLSConfig{
				Enabled:      true,
				Certificates: pairs,
			},
		},
	}
}
