package ingress

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/wudi/runway/config"
)

// translateGatewayAPI converts Gateway API resources (Gateway, HTTPRoute) into routes and listeners.
func (t *Translator) translateGatewayAPI() ([]config.RouteConfig, []config.ListenerConfig, []string) {
	var routes []config.RouteConfig
	var listeners []config.ListenerConfig
	var warnings []string

	// Translate Gateways → Listeners
	for _, gw := range t.store.ListGateways() {
		gcName := string(gw.Spec.GatewayClassName)
		gc, ok := t.store.GetGatewayClass(gcName)
		if !ok || string(gc.Spec.ControllerName) != t.controllerName {
			continue
		}

		for i, l := range gw.Spec.Listeners {
			lc, w := t.gatewayListenerToConfig(gw, l, i)
			warnings = append(warnings, w...)
			if lc != nil {
				listeners = append(listeners, *lc)
			}
		}
	}

	// Translate HTTPRoutes → Routes
	for _, hr := range t.store.ListHTTPRoutes() {
		if !t.httpRouteReferencesOurGateway(hr) {
			continue
		}
		r, w := t.httpRouteToRoutes(hr)
		warnings = append(warnings, w...)
		routes = append(routes, r...)
	}

	return routes, listeners, warnings
}

// gatewayListenerToConfig converts a Gateway listener to a ListenerConfig.
func (t *Translator) gatewayListenerToConfig(gw *gatewayv1.Gateway, l gatewayv1.Listener, idx int) (*config.ListenerConfig, []string) {
	var warnings []string

	listenerID := fmt.Sprintf("gw-%s-%s-%s", gw.Namespace, gw.Name, l.Name)
	port := int(l.Port)

	lc := &config.ListenerConfig{
		ID:       listenerID,
		Address:  fmt.Sprintf(":%d", port),
		Protocol: config.ProtocolHTTP,
	}

	// TLS configuration
	if l.TLS != nil && l.TLS.Mode != nil && *l.TLS.Mode == gatewayv1.TLSModeTerminate {
		lc.TLS.Enabled = true
		for _, ref := range l.TLS.CertificateRefs {
			ns := gw.Namespace
			if ref.Namespace != nil {
				ns = string(*ref.Namespace)
			}
			secretKey := types.NamespacedName{Namespace: ns, Name: string(ref.Name)}
			secret, ok := t.store.GetSecret(secretKey)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("Gateway %s/%s listener %s: secret %s not found", gw.Namespace, gw.Name, l.Name, secretKey))
				continue
			}
			var hosts []string
			if l.Hostname != nil {
				hosts = []string{string(*l.Hostname)}
			}
			pair, err := SecretToTLSCertPair(secret, hosts)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			lc.TLS.Certificates = append(lc.TLS.Certificates, pair)
		}
	}

	return lc, warnings
}

// httpRouteReferencesOurGateway checks if an HTTPRoute's parentRefs include a Gateway managed by us.
func (t *Translator) httpRouteReferencesOurGateway(hr *gatewayv1.HTTPRoute) bool {
	for _, ref := range hr.Spec.ParentRefs {
		group := gatewayv1.GroupName
		if ref.Group != nil {
			group = string(*ref.Group)
		}
		if group != "" && group != gatewayv1.GroupName {
			continue
		}
		kind := gatewayv1.Kind("Gateway")
		if ref.Kind != nil {
			kind = *ref.Kind
		}
		if kind != "Gateway" {
			continue
		}
		ns := hr.Namespace
		if ref.Namespace != nil {
			ns = string(*ref.Namespace)
		}
		gwKey := types.NamespacedName{Namespace: ns, Name: string(ref.Name)}
		for _, gw := range t.store.ListGateways() {
			if gw.Namespace == gwKey.Namespace && gw.Name == gwKey.Name {
				gcName := string(gw.Spec.GatewayClassName)
				if gc, ok := t.store.GetGatewayClass(gcName); ok {
					if string(gc.Spec.ControllerName) == t.controllerName {
						return true
					}
				}
			}
		}
	}
	return false
}

// httpRouteToRoutes converts an HTTPRoute to one or more RouteConfigs.
func (t *Translator) httpRouteToRoutes(hr *gatewayv1.HTTPRoute) ([]config.RouteConfig, []string) {
	var routes []config.RouteConfig
	var warnings []string

	// Collect hostnames from the route
	var hostnames []string
	for _, h := range hr.Spec.Hostnames {
		hostnames = append(hostnames, string(h))
	}

	for i, rule := range hr.Spec.Rules {
		// Build backends from backendRefs
		var backends []config.BackendConfig
		for _, ref := range rule.BackendRefs {
			b, w := t.resolveHTTPBackendRef(hr.Namespace, ref)
			warnings = append(warnings, w...)
			backends = append(backends, b...)
		}

		// Generate one route per match (or one catch-all if no matches)
		matches := rule.Matches
		if len(matches) == 0 {
			matches = []gatewayv1.HTTPRouteMatch{{}}
		}

		for j, match := range matches {
			routeID := fmt.Sprintf("hr-%s-%s-%d-%d", hr.Namespace, hr.Name, i, j)
			rc := config.RouteConfig{
				ID:       routeID,
				Backends: backends,
			}

			// Path
			if match.Path != nil {
				pathType := gatewayv1.PathMatchPathPrefix
				if match.Path.Type != nil {
					pathType = *match.Path.Type
				}
				pathVal := "/"
				if match.Path.Value != nil {
					pathVal = *match.Path.Value
				}
				rc.Path = pathVal
				rc.PathPrefix = pathType == gatewayv1.PathMatchPathPrefix
			} else {
				rc.Path = "/"
				rc.PathPrefix = true
			}

			// Hostnames
			if len(hostnames) > 0 {
				rc.Match.Domains = hostnames
			}

			// Headers
			for _, hm := range match.Headers {
				hc := config.HeaderMatchConfig{
					Name: string(hm.Name),
				}
				if hm.Type != nil && *hm.Type == gatewayv1.HeaderMatchRegularExpression {
					hc.Regex = hm.Value
				} else {
					hc.Value = hm.Value
				}
				rc.Match.Headers = append(rc.Match.Headers, hc)
			}

			// Methods
			if match.Method != nil {
				rc.Methods = []string{string(*match.Method)}
			}

			// Filters (request/response header modification, redirect, URL rewrite)
			for _, f := range rule.Filters {
				t.applyHTTPRouteFilter(&rc, f, &warnings)
			}

			routes = append(routes, rc)
		}
	}

	return routes, warnings
}

// resolveHTTPBackendRef resolves a Gateway API BackendRef to BackendConfigs.
func (t *Translator) resolveHTTPBackendRef(namespace string, ref gatewayv1.HTTPBackendRef) ([]config.BackendConfig, []string) {
	var warnings []string

	// Only support Service kind
	if ref.Kind != nil && *ref.Kind != "Service" {
		warnings = append(warnings, fmt.Sprintf("unsupported backendRef kind: %s", *ref.Kind))
		return nil, warnings
	}

	ns := namespace
	if ref.Namespace != nil {
		ns = string(*ref.Namespace)
	}
	svcName := string(ref.Name)

	var port int32
	if ref.Port != nil {
		port = int32(*ref.Port)
	}
	if port == 0 {
		port = 80
	}

	weight := 1
	if ref.Weight != nil {
		weight = int(*ref.Weight)
	}

	// Check for ExternalName service
	svcKey := keyOf(ns, svcName)
	if svc, ok := t.store.GetService(svcKey); ok && svc.Spec.Type == "ExternalName" {
		url := fmt.Sprintf("http://%s:%d", svc.Spec.ExternalName, port)
		return []config.BackendConfig{{URL: url, Weight: weight}}, nil
	}

	backends := t.resolveEndpointSliceBackends(ns, svcName, port)
	for i := range backends {
		backends[i].Weight = weight
	}
	return backends, warnings
}

// applyHTTPRouteFilter applies a Gateway API filter to a RouteConfig.
func (t *Translator) applyHTTPRouteFilter(rc *config.RouteConfig, f gatewayv1.HTTPRouteFilter, warnings *[]string) {
	switch f.Type {
	case gatewayv1.HTTPRouteFilterRequestRedirect:
		if f.RequestRedirect != nil {
			// We can't fully express redirects in a RouteConfig without a rules engine,
			// but we log a warning. Users should use request rules for complex redirects.
			*warnings = append(*warnings, fmt.Sprintf("route %s: RequestRedirect filter requires manual rules configuration", rc.ID))
		}
	case gatewayv1.HTTPRouteFilterURLRewrite:
		if f.URLRewrite != nil && f.URLRewrite.Path != nil {
			switch f.URLRewrite.Path.Type {
			case gatewayv1.PrefixMatchHTTPPathModifier:
				if f.URLRewrite.Path.ReplacePrefixMatch != nil {
					rc.StripPrefix = true
				}
			}
		}
	case gatewayv1.HTTPRouteFilterRequestHeaderModifier:
		if f.RequestHeaderModifier != nil {
			if rc.Transform.Request.Headers.Add == nil {
				rc.Transform.Request.Headers.Add = make(map[string]string)
			}
			if rc.Transform.Request.Headers.Set == nil {
				rc.Transform.Request.Headers.Set = make(map[string]string)
			}
			for _, add := range f.RequestHeaderModifier.Add {
				rc.Transform.Request.Headers.Add[string(add.Name)] = add.Value
			}
			for _, set := range f.RequestHeaderModifier.Set {
				rc.Transform.Request.Headers.Set[string(set.Name)] = set.Value
			}
			for _, rm := range f.RequestHeaderModifier.Remove {
				rc.Transform.Request.Headers.Remove = append(rc.Transform.Request.Headers.Remove, rm)
			}
		}
	case gatewayv1.HTTPRouteFilterResponseHeaderModifier:
		if f.ResponseHeaderModifier != nil {
			if rc.Transform.Response.Headers.Add == nil {
				rc.Transform.Response.Headers.Add = make(map[string]string)
			}
			if rc.Transform.Response.Headers.Set == nil {
				rc.Transform.Response.Headers.Set = make(map[string]string)
			}
			for _, add := range f.ResponseHeaderModifier.Add {
				rc.Transform.Response.Headers.Add[string(add.Name)] = add.Value
			}
			for _, set := range f.ResponseHeaderModifier.Set {
				rc.Transform.Response.Headers.Set[string(set.Name)] = set.Value
			}
			for _, rm := range f.ResponseHeaderModifier.Remove {
				rc.Transform.Response.Headers.Remove = append(rc.Transform.Response.Headers.Remove, rm)
			}
		}
	default:
		*warnings = append(*warnings, fmt.Sprintf("route %s: unsupported filter type %s", rc.ID, f.Type))
	}
}

// parentGatewayHostnames returns the set of hostnames from the parent Gateway's
// listeners that match the given parentRef. This is used for hostname intersection.
func (t *Translator) parentGatewayHostnames(hr *gatewayv1.HTTPRoute) []string {
	var hosts []string
	for _, ref := range hr.Spec.ParentRefs {
		ns := hr.Namespace
		if ref.Namespace != nil {
			ns = string(*ref.Namespace)
		}
		for _, gw := range t.store.ListGateways() {
			if gw.Namespace != ns || gw.Name != string(ref.Name) {
				continue
			}
			for _, l := range gw.Spec.Listeners {
				if ref.SectionName != nil && l.Name != *ref.SectionName {
					continue
				}
				if l.Hostname != nil {
					hosts = append(hosts, string(*l.Hostname))
				}
			}
		}
	}
	return hosts
}

// intersectHostnames returns hostnames that match between route hostnames and
// gateway listener hostnames. A gateway hostname "*.example.com" matches
// "foo.example.com" from the route.
func intersectHostnames(routeHosts, gatewayHosts []string) []string {
	if len(gatewayHosts) == 0 {
		return routeHosts
	}
	if len(routeHosts) == 0 {
		return gatewayHosts
	}
	var result []string
	for _, rh := range routeHosts {
		for _, gh := range gatewayHosts {
			if hostnameMatch(rh, gh) {
				result = append(result, rh)
				break
			}
		}
	}
	return result
}

// hostnameMatch checks if a hostname matches a pattern (supports wildcard prefix).
func hostnameMatch(hostname, pattern string) bool {
	if hostname == pattern {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		return strings.HasSuffix(hostname, suffix) && !strings.Contains(hostname[:len(hostname)-len(suffix)], ".")
	}
	if strings.HasPrefix(hostname, "*.") {
		suffix := hostname[1:]
		return strings.HasSuffix(pattern, suffix)
	}
	return false
}
