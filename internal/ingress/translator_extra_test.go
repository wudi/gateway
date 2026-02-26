package ingress

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/wudi/runway/config"
)

// ---------------------------------------------------------------------------
// intersectHostnames
// ---------------------------------------------------------------------------

func TestIntersectHostnames(t *testing.T) {
	tests := []struct {
		name         string
		routeHosts   []string
		gatewayHosts []string
		want         []string
	}{
		{
			name:         "empty gateway hosts returns route hosts",
			routeHosts:   []string{"foo.example.com"},
			gatewayHosts: nil,
			want:         []string{"foo.example.com"},
		},
		{
			name:         "empty route hosts returns gateway hosts",
			routeHosts:   nil,
			gatewayHosts: []string{"*.example.com"},
			want:         []string{"*.example.com"},
		},
		{
			name:         "both empty returns nil",
			routeHosts:   nil,
			gatewayHosts: nil,
			want:         nil,
		},
		{
			name:         "exact match",
			routeHosts:   []string{"foo.example.com"},
			gatewayHosts: []string{"foo.example.com"},
			want:         []string{"foo.example.com"},
		},
		{
			name:         "wildcard gateway matches route",
			routeHosts:   []string{"foo.example.com", "bar.example.com"},
			gatewayHosts: []string{"*.example.com"},
			want:         []string{"foo.example.com", "bar.example.com"},
		},
		{
			name:         "no match",
			routeHosts:   []string{"foo.other.com"},
			gatewayHosts: []string{"*.example.com"},
			want:         nil,
		},
		{
			name:         "partial match",
			routeHosts:   []string{"foo.example.com", "bar.other.com"},
			gatewayHosts: []string{"*.example.com"},
			want:         []string{"foo.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := intersectHostnames(tt.routeHosts, tt.gatewayHosts)
			if len(got) != len(tt.want) {
				t.Fatalf("intersectHostnames() returned %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("intersectHostnames()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// hostnameMatch (supplement existing tests for wildcard-hostname branch)
// ---------------------------------------------------------------------------

func TestHostnameMatchWildcardHostname(t *testing.T) {
	tests := []struct {
		hostname, pattern string
		want              bool
	}{
		// Wildcard hostname vs exact pattern
		{"*.example.com", "foo.example.com", true},
		{"*.example.com", "bar.example.com", true},
		{"*.example.com", "other.com", false},
		// Both wildcards
		{"*.example.com", "*.example.com", true},
		// Neither matches
		{"foo.com", "bar.com", false},
		// Empty strings
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.hostname+"_vs_"+tt.pattern, func(t *testing.T) {
			if got := hostnameMatch(tt.hostname, tt.pattern); got != tt.want {
				t.Errorf("hostnameMatch(%q, %q) = %v, want %v", tt.hostname, tt.pattern, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isSameParentRef
// ---------------------------------------------------------------------------

func TestIsSameParentRef(t *testing.T) {
	group := gatewayv1.Group(gatewayv1.GroupName)
	otherGroup := gatewayv1.Group("other.io")

	tests := []struct {
		name string
		a, b gatewayv1.ParentReference
		want bool
	}{
		{
			name: "same name nil groups",
			a:    gatewayv1.ParentReference{Name: "gw"},
			b:    gatewayv1.ParentReference{Name: "gw"},
			want: true,
		},
		{
			name: "same name same explicit group",
			a:    gatewayv1.ParentReference{Group: &group, Name: "gw"},
			b:    gatewayv1.ParentReference{Group: &group, Name: "gw"},
			want: true,
		},
		{
			name: "different names",
			a:    gatewayv1.ParentReference{Name: "gw1"},
			b:    gatewayv1.ParentReference{Name: "gw2"},
			want: false,
		},
		{
			name: "different groups",
			a:    gatewayv1.ParentReference{Group: &group, Name: "gw"},
			b:    gatewayv1.ParentReference{Group: &otherGroup, Name: "gw"},
			want: false,
		},
		{
			name: "one nil group one explicit matching group",
			a:    gatewayv1.ParentReference{Name: "gw"},
			b:    gatewayv1.ParentReference{Group: &group, Name: "gw"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSameParentRef(tt.a, tt.b); got != tt.want {
				t.Errorf("isSameParentRef() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IngressStatusError
// ---------------------------------------------------------------------------

func TestIngressStatusError(t *testing.T) {
	e := &IngressStatusError{
		Resource: "default/my-ingress",
		Err:      errors.New("network error"),
	}
	want := "status update for default/my-ingress: network error"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// IngressesForSecret
// ---------------------------------------------------------------------------

func TestIngressesForSecret(t *testing.T) {
	store := NewStore()

	className := "runway"
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ing1", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS: []networkingv1.IngressTLS{
				{SecretName: "tls-secret", Hosts: []string{"a.example.com"}},
			},
		},
	})
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ing2", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS: []networkingv1.IngressTLS{
				{SecretName: "other-secret", Hosts: []string{"b.example.com"}},
			},
		},
	})
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ing3", Namespace: "other-ns"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS: []networkingv1.IngressTLS{
				{SecretName: "tls-secret", Hosts: []string{"c.example.com"}},
			},
		},
	})

	t.Run("finds matching ingress", func(t *testing.T) {
		result := IngressesForSecret(store, types.NamespacedName{Namespace: "default", Name: "tls-secret"})
		if len(result) != 1 {
			t.Fatalf("expected 1 result, got %d", len(result))
		}
		if result[0].Name != "ing1" {
			t.Errorf("expected ing1, got %s", result[0].Name)
		}
	})

	t.Run("no match different namespace", func(t *testing.T) {
		result := IngressesForSecret(store, types.NamespacedName{Namespace: "other-ns", Name: "tls-secret"})
		if len(result) != 1 || result[0].Name != "ing3" {
			t.Errorf("expected ing3 for other-ns, got %v", result)
		}
	})

	t.Run("no matches for nonexistent secret", func(t *testing.T) {
		result := IngressesForSecret(store, types.NamespacedName{Namespace: "default", Name: "nonexistent"})
		if len(result) != 0 {
			t.Errorf("expected 0 results, got %d", len(result))
		}
	})
}

// ---------------------------------------------------------------------------
// resolveIngressPort
// ---------------------------------------------------------------------------

func TestResolveIngressPort(t *testing.T) {
	t.Run("numeric port", func(t *testing.T) {
		p := resolveIngressPort(networkingv1.ServiceBackendPort{Number: 9090})
		if p != 9090 {
			t.Errorf("expected 9090, got %d", p)
		}
	})

	t.Run("named port falls back to 80", func(t *testing.T) {
		p := resolveIngressPort(networkingv1.ServiceBackendPort{Name: "http"})
		if p != 80 {
			t.Errorf("expected 80 for named port, got %d", p)
		}
	})

	t.Run("zero port falls back to 80", func(t *testing.T) {
		p := resolveIngressPort(networkingv1.ServiceBackendPort{})
		if p != 80 {
			t.Errorf("expected 80 for zero port, got %d", p)
		}
	})
}

// ---------------------------------------------------------------------------
// applyAnnotations (cover all annotation branches)
// ---------------------------------------------------------------------------

func TestApplyAnnotationsAllBranches(t *testing.T) {
	t.Run("all annotations set", func(t *testing.T) {
		ann := NewAnnotationParser(map[string]string{
			AnnRateLimit:      "100",
			AnnTimeout:        "5s",
			AnnRetryMax:       "2",
			AnnCORSEnabled:    "true",
			AnnCircuitBreaker: "true",
			AnnAuthRequired:   "true",
			AnnCacheEnabled:   "true",
			AnnLoadBalancer:   "least_conn",
		})

		store := NewStore()
		tr := NewTranslator(store, nil, TranslatorConfig{
			IngressClass:   "runway",
			ControllerName: "runway.wudi.io/ingress-controller",
		})

		rc := newEmptyRouteConfig("test-all-ann")
		tr.applyAnnotations(&rc, ann)

		if !rc.RateLimit.Enabled {
			t.Error("expected rate limit enabled")
		}
		if rc.RateLimit.Rate != 100 {
			t.Errorf("expected rate limit 100, got %d", rc.RateLimit.Rate)
		}
		if rc.TimeoutPolicy.Request.String() != "5s" {
			t.Errorf("expected timeout 5s, got %v", rc.TimeoutPolicy.Request)
		}
		if rc.Retries != 2 {
			t.Errorf("expected 2 retries, got %d", rc.Retries)
		}
		if !rc.CORS.Enabled {
			t.Error("expected CORS enabled")
		}
		if !rc.CircuitBreaker.Enabled {
			t.Error("expected circuit breaker enabled")
		}
		if !rc.Auth.Required {
			t.Error("expected auth required")
		}
		if !rc.Cache.Enabled {
			t.Error("expected cache enabled")
		}
		if rc.LoadBalancer != "least_conn" {
			t.Errorf("expected least_conn, got %s", rc.LoadBalancer)
		}
	})

	t.Run("no annotations set does not modify route", func(t *testing.T) {
		ann := NewAnnotationParser(map[string]string{})

		store := NewStore()
		tr := NewTranslator(store, nil, TranslatorConfig{
			IngressClass:   "runway",
			ControllerName: "runway.wudi.io/ingress-controller",
		})

		rc := newEmptyRouteConfig("test-no-ann")
		tr.applyAnnotations(&rc, ann)

		if rc.RateLimit.Enabled {
			t.Error("rate limit should not be enabled")
		}
		if rc.Retries != 0 {
			t.Errorf("retries should be 0, got %d", rc.Retries)
		}
	})
}

// ---------------------------------------------------------------------------
// ingressDefaultBackendToRoute
// ---------------------------------------------------------------------------

func TestIngressDefaultBackendToRoute(t *testing.T) {
	store := NewStore()
	className := "runway"

	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-backend-ing",
			Namespace: "myns",
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			DefaultBackend: &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: "fallback-svc",
					Port: networkingv1.ServiceBackendPort{Number: 3000},
				},
			},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	cfg, _ := tr.Translate()

	if len(cfg.Routes) != 1 {
		t.Fatalf("expected 1 route (default backend), got %d", len(cfg.Routes))
	}

	route := cfg.Routes[0]
	if route.ID != "ing-myns-default-backend-ing-default" {
		t.Errorf("unexpected route ID: %s", route.ID)
	}
	if route.Path != "/" {
		t.Errorf("expected path /, got %s", route.Path)
	}
	if !route.PathPrefix {
		t.Error("expected PathPrefix=true for default backend")
	}
	// ClusterIP fallback since no EndpointSlice
	if len(route.Backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(route.Backends))
	}
	want := "http://fallback-svc.myns.svc.cluster.local:3000"
	if route.Backends[0].URL != want {
		t.Errorf("expected backend %s, got %s", want, route.Backends[0].URL)
	}
}

// ---------------------------------------------------------------------------
// shouldProcessIngress - annotation-based class
// ---------------------------------------------------------------------------

func TestShouldProcessIngressAnnotation(t *testing.T) {
	store := NewStore()
	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	t.Run("annotation matches", func(t *testing.T) {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"kubernetes.io/ingress.class": "runway",
				},
			},
		}
		if !tr.shouldProcessIngress(ing) {
			t.Error("expected ingress with matching annotation to be processed")
		}
	})

	t.Run("annotation does not match", func(t *testing.T) {
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"kubernetes.io/ingress.class": "nginx",
				},
			},
		}
		if tr.shouldProcessIngress(ing) {
			t.Error("expected ingress with non-matching annotation to be skipped")
		}
	})

	t.Run("spec class takes precedence over annotation", func(t *testing.T) {
		otherClass := "nginx"
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"kubernetes.io/ingress.class": "runway",
				},
			},
			Spec: networkingv1.IngressSpec{
				IngressClassName: &otherClass,
			},
		}
		// spec.ingressClassName is checked first; it says "nginx" so we should skip
		if tr.shouldProcessIngress(ing) {
			t.Error("spec.ingressClassName should take precedence over annotation")
		}
	})
}

// ---------------------------------------------------------------------------
// applyHTTPRouteFilter
// ---------------------------------------------------------------------------

func TestApplyHTTPRouteFilter(t *testing.T) {
	store := NewStore()
	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	t.Run("request header modifier", func(t *testing.T) {
		rc := newEmptyRouteConfig("test-rhm")
		var warnings []string

		tr.applyHTTPRouteFilter(&rc, gatewayv1.HTTPRouteFilter{
			Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier,
			RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{
				Add: []gatewayv1.HTTPHeader{
					{Name: "X-Added", Value: "val1"},
				},
				Set: []gatewayv1.HTTPHeader{
					{Name: "X-Set", Value: "val2"},
				},
				Remove: []string{"X-Remove"},
			},
		}, &warnings)

		if len(warnings) != 0 {
			t.Errorf("unexpected warnings: %v", warnings)
		}
		if rc.Transform.Request.Headers.Add["X-Added"] != "val1" {
			t.Errorf("expected X-Added=val1, got %v", rc.Transform.Request.Headers.Add)
		}
		if rc.Transform.Request.Headers.Set["X-Set"] != "val2" {
			t.Errorf("expected X-Set=val2, got %v", rc.Transform.Request.Headers.Set)
		}
		if len(rc.Transform.Request.Headers.Remove) != 1 || rc.Transform.Request.Headers.Remove[0] != "X-Remove" {
			t.Errorf("expected Remove=[X-Remove], got %v", rc.Transform.Request.Headers.Remove)
		}
	})

	t.Run("response header modifier", func(t *testing.T) {
		rc := newEmptyRouteConfig("test-resp")
		var warnings []string

		tr.applyHTTPRouteFilter(&rc, gatewayv1.HTTPRouteFilter{
			Type: gatewayv1.HTTPRouteFilterResponseHeaderModifier,
			ResponseHeaderModifier: &gatewayv1.HTTPHeaderFilter{
				Add: []gatewayv1.HTTPHeader{
					{Name: "X-Resp-Added", Value: "val3"},
				},
				Set: []gatewayv1.HTTPHeader{
					{Name: "X-Resp-Set", Value: "val4"},
				},
				Remove: []string{"X-Resp-Remove"},
			},
		}, &warnings)

		if len(warnings) != 0 {
			t.Errorf("unexpected warnings: %v", warnings)
		}
		if rc.Transform.Response.Headers.Add["X-Resp-Added"] != "val3" {
			t.Errorf("expected X-Resp-Added=val3, got %v", rc.Transform.Response.Headers.Add)
		}
		if rc.Transform.Response.Headers.Set["X-Resp-Set"] != "val4" {
			t.Errorf("expected X-Resp-Set=val4, got %v", rc.Transform.Response.Headers.Set)
		}
		if len(rc.Transform.Response.Headers.Remove) != 1 || rc.Transform.Response.Headers.Remove[0] != "X-Resp-Remove" {
			t.Errorf("expected Remove=[X-Resp-Remove], got %v", rc.Transform.Response.Headers.Remove)
		}
	})

	t.Run("URL rewrite with prefix replace", func(t *testing.T) {
		rc := newEmptyRouteConfig("test-rewrite")
		var warnings []string

		replacePrefixMatch := "/new-prefix"
		tr.applyHTTPRouteFilter(&rc, gatewayv1.HTTPRouteFilter{
			Type: gatewayv1.HTTPRouteFilterURLRewrite,
			URLRewrite: &gatewayv1.HTTPURLRewriteFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:               gatewayv1.PrefixMatchHTTPPathModifier,
					ReplacePrefixMatch: &replacePrefixMatch,
				},
			},
		}, &warnings)

		if !rc.StripPrefix {
			t.Error("expected StripPrefix=true for URL rewrite with prefix replace")
		}
	})

	t.Run("URL rewrite nil path", func(t *testing.T) {
		rc := newEmptyRouteConfig("test-rewrite-nil")
		var warnings []string

		tr.applyHTTPRouteFilter(&rc, gatewayv1.HTTPRouteFilter{
			Type:       gatewayv1.HTTPRouteFilterURLRewrite,
			URLRewrite: &gatewayv1.HTTPURLRewriteFilter{},
		}, &warnings)

		if rc.StripPrefix {
			t.Error("StripPrefix should not be set when path is nil")
		}
	})

	t.Run("request redirect produces warning", func(t *testing.T) {
		rc := newEmptyRouteConfig("test-redir")
		var warnings []string

		scheme := "https"
		tr.applyHTTPRouteFilter(&rc, gatewayv1.HTTPRouteFilter{
			Type: gatewayv1.HTTPRouteFilterRequestRedirect,
			RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
				Scheme: &scheme,
			},
		}, &warnings)

		if len(warnings) != 1 {
			t.Fatalf("expected 1 warning for redirect, got %d: %v", len(warnings), warnings)
		}
	})

	t.Run("unsupported filter type produces warning", func(t *testing.T) {
		rc := newEmptyRouteConfig("test-unsup")
		var warnings []string

		tr.applyHTTPRouteFilter(&rc, gatewayv1.HTTPRouteFilter{
			Type: gatewayv1.HTTPRouteFilterRequestMirror,
		}, &warnings)

		if len(warnings) != 1 {
			t.Fatalf("expected 1 warning for unsupported filter, got %d", len(warnings))
		}
	})

	t.Run("nil request header modifier", func(t *testing.T) {
		rc := newEmptyRouteConfig("test-nil-rhm")
		var warnings []string

		tr.applyHTTPRouteFilter(&rc, gatewayv1.HTTPRouteFilter{
			Type:                  gatewayv1.HTTPRouteFilterRequestHeaderModifier,
			RequestHeaderModifier: nil,
		}, &warnings)

		if rc.Transform.Request.Headers.Add != nil {
			t.Error("expected no Add headers when modifier is nil")
		}
	})

	t.Run("nil response header modifier", func(t *testing.T) {
		rc := newEmptyRouteConfig("test-nil-resp")
		var warnings []string

		tr.applyHTTPRouteFilter(&rc, gatewayv1.HTTPRouteFilter{
			Type:                   gatewayv1.HTTPRouteFilterResponseHeaderModifier,
			ResponseHeaderModifier: nil,
		}, &warnings)

		if rc.Transform.Response.Headers.Add != nil {
			t.Error("expected no Add headers when modifier is nil")
		}
	})

	t.Run("nil request redirect", func(t *testing.T) {
		rc := newEmptyRouteConfig("test-nil-redir")
		var warnings []string

		tr.applyHTTPRouteFilter(&rc, gatewayv1.HTTPRouteFilter{
			Type:            gatewayv1.HTTPRouteFilterRequestRedirect,
			RequestRedirect: nil,
		}, &warnings)

		if len(warnings) != 0 {
			t.Errorf("expected no warnings for nil redirect, got %v", warnings)
		}
	})
}

// ---------------------------------------------------------------------------
// parentGatewayHostnames
// ---------------------------------------------------------------------------

func TestParentGatewayHostnames(t *testing.T) {
	store := NewStore()
	controllerName := gatewayv1.GatewayController("runway.wudi.io/ingress-controller")

	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})

	hostname1 := gatewayv1.Hostname("*.example.com")
	hostname2 := gatewayv1.Hostname("api.example.com")
	sectionHTTP := gatewayv1.SectionName("http")
	sectionHTTPS := gatewayv1.SectionName("https")

	store.SetGateway(&gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "my-gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "runway",
			Listeners: []gatewayv1.Listener{
				{
					Name:     "http",
					Port:     8080,
					Protocol: gatewayv1.HTTPProtocolType,
					Hostname: &hostname1,
				},
				{
					Name:     "https",
					Port:     8443,
					Protocol: gatewayv1.HTTPSProtocolType,
					Hostname: &hostname2,
				},
			},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	t.Run("all listeners no section filter", func(t *testing.T) {
		group := gatewayv1.Group(gatewayv1.GroupName)
		hr := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route1", Namespace: "default"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{Group: &group, Name: "my-gw"},
					},
				},
			},
		}
		hosts := tr.parentGatewayHostnames(hr)
		if len(hosts) != 2 {
			t.Fatalf("expected 2 hostnames, got %d: %v", len(hosts), hosts)
		}
	})

	t.Run("section name filter", func(t *testing.T) {
		group := gatewayv1.Group(gatewayv1.GroupName)
		hr := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route2", Namespace: "default"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{Group: &group, Name: "my-gw", SectionName: &sectionHTTP},
					},
				},
			},
		}
		hosts := tr.parentGatewayHostnames(hr)
		if len(hosts) != 1 || hosts[0] != "*.example.com" {
			t.Errorf("expected [*.example.com], got %v", hosts)
		}
	})

	t.Run("section name filter https", func(t *testing.T) {
		group := gatewayv1.Group(gatewayv1.GroupName)
		hr := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route3", Namespace: "default"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{Group: &group, Name: "my-gw", SectionName: &sectionHTTPS},
					},
				},
			},
		}
		hosts := tr.parentGatewayHostnames(hr)
		if len(hosts) != 1 || hosts[0] != "api.example.com" {
			t.Errorf("expected [api.example.com], got %v", hosts)
		}
	})

	t.Run("cross namespace with explicit namespace", func(t *testing.T) {
		group := gatewayv1.Group(gatewayv1.GroupName)
		ns := gatewayv1.Namespace("default")
		hr := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route4", Namespace: "other-ns"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{Group: &group, Name: "my-gw", Namespace: &ns},
					},
				},
			},
		}
		hosts := tr.parentGatewayHostnames(hr)
		if len(hosts) != 2 {
			t.Fatalf("expected 2 hostnames (cross-ns), got %d: %v", len(hosts), hosts)
		}
	})

	t.Run("no matching gateway", func(t *testing.T) {
		group := gatewayv1.Group(gatewayv1.GroupName)
		hr := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "route5", Namespace: "default"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{Group: &group, Name: "nonexistent"},
					},
				},
			},
		}
		hosts := tr.parentGatewayHostnames(hr)
		if len(hosts) != 0 {
			t.Errorf("expected no hostnames, got %v", hosts)
		}
	})
}

// ---------------------------------------------------------------------------
// gatewayListenerToConfig (TLS path)
// ---------------------------------------------------------------------------

func TestGatewayListenerToConfigTLS(t *testing.T) {
	store := NewStore()
	controllerName := gatewayv1.GatewayController("runway.wudi.io/ingress-controller")

	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})

	store.SetSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gw-tls", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte("cert-pem"),
			corev1.TLSPrivateKeyKey: []byte("key-pem"),
		},
	})

	hostname := gatewayv1.Hostname("secure.example.com")
	tlsTerminate := gatewayv1.TLSModeTerminate

	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "runway",
			Listeners: []gatewayv1.Listener{
				{
					Name:     "https",
					Port:     8443,
					Protocol: gatewayv1.HTTPSProtocolType,
					Hostname: &hostname,
					TLS: &gatewayv1.ListenerTLSConfig{
						Mode: &tlsTerminate,
						CertificateRefs: []gatewayv1.SecretObjectReference{
							{Name: "gw-tls"},
						},
					},
				},
			},
		},
	}
	store.SetGateway(gw)

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	lc, warnings := tr.gatewayListenerToConfig(gw, gw.Spec.Listeners[0], 0)
	if lc == nil {
		t.Fatal("expected non-nil listener config")
	}
	if !lc.TLS.Enabled {
		t.Error("expected TLS to be enabled")
	}
	if len(lc.TLS.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(lc.TLS.Certificates))
	}
	if string(lc.TLS.Certificates[0].CertData) != "cert-pem" {
		t.Errorf("unexpected cert data: %s", string(lc.TLS.Certificates[0].CertData))
	}
	if len(lc.TLS.Certificates[0].Hosts) != 1 || lc.TLS.Certificates[0].Hosts[0] != "secure.example.com" {
		t.Errorf("expected hosts [secure.example.com], got %v", lc.TLS.Certificates[0].Hosts)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestGatewayListenerToConfigMissingSecret(t *testing.T) {
	store := NewStore()
	controllerName := gatewayv1.GatewayController("runway.wudi.io/ingress-controller")
	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})

	tlsTerminate := gatewayv1.TLSModeTerminate
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "missing-secret-gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "runway",
			Listeners: []gatewayv1.Listener{
				{
					Name:     "https",
					Port:     443,
					Protocol: gatewayv1.HTTPSProtocolType,
					TLS: &gatewayv1.ListenerTLSConfig{
						Mode: &tlsTerminate,
						CertificateRefs: []gatewayv1.SecretObjectReference{
							{Name: "nonexistent-secret"},
						},
					},
				},
			},
		},
	}
	store.SetGateway(gw)

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	lc, warnings := tr.gatewayListenerToConfig(gw, gw.Spec.Listeners[0], 0)
	if lc == nil {
		t.Fatal("expected non-nil listener config even with missing secret")
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for missing secret, got %d: %v", len(warnings), warnings)
	}
}

func TestGatewayListenerToConfigBadSecretData(t *testing.T) {
	store := NewStore()
	controllerName := gatewayv1.GatewayController("runway.wudi.io/ingress-controller")
	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})

	// Secret with missing key data
	store.SetSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-secret", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSCertKey: []byte("cert-only"),
		},
	})

	tlsTerminate := gatewayv1.TLSModeTerminate
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-data-gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "runway",
			Listeners: []gatewayv1.Listener{
				{
					Name:     "https",
					Port:     443,
					Protocol: gatewayv1.HTTPSProtocolType,
					TLS: &gatewayv1.ListenerTLSConfig{
						Mode: &tlsTerminate,
						CertificateRefs: []gatewayv1.SecretObjectReference{
							{Name: "bad-secret"},
						},
					},
				},
			},
		},
	}
	store.SetGateway(gw)

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	lc, warnings := tr.gatewayListenerToConfig(gw, gw.Spec.Listeners[0], 0)
	if lc == nil {
		t.Fatal("expected non-nil listener config")
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for bad secret data, got %d: %v", len(warnings), warnings)
	}
	if len(lc.TLS.Certificates) != 0 {
		t.Errorf("expected 0 certificates, got %d", len(lc.TLS.Certificates))
	}
}

func TestGatewayListenerToConfigCrossNamespaceSecret(t *testing.T) {
	store := NewStore()
	controllerName := gatewayv1.GatewayController("runway.wudi.io/ingress-controller")
	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})

	store.SetSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-ns-secret", Namespace: "cert-ns"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte("cert"),
			corev1.TLSPrivateKeyKey: []byte("key"),
		},
	})

	tlsTerminate := gatewayv1.TLSModeTerminate
	certNs := gatewayv1.Namespace("cert-ns")
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-ns-gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "runway",
			Listeners: []gatewayv1.Listener{
				{
					Name:     "https",
					Port:     443,
					Protocol: gatewayv1.HTTPSProtocolType,
					TLS: &gatewayv1.ListenerTLSConfig{
						Mode: &tlsTerminate,
						CertificateRefs: []gatewayv1.SecretObjectReference{
							{Name: "cross-ns-secret", Namespace: &certNs},
						},
					},
				},
			},
		},
	}
	store.SetGateway(gw)

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	lc, warnings := tr.gatewayListenerToConfig(gw, gw.Spec.Listeners[0], 0)
	if lc == nil {
		t.Fatal("expected non-nil listener config")
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(lc.TLS.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(lc.TLS.Certificates))
	}
}

// ---------------------------------------------------------------------------
// Store CRUD for types not covered in store_test.go
// ---------------------------------------------------------------------------

func TestStoreGatewayCRUD(t *testing.T) {
	s := NewStore()

	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "my-gw", Namespace: "default"},
	}
	s.SetGateway(gw)

	list := s.ListGateways()
	if len(list) != 1 {
		t.Fatalf("expected 1 gateway, got %d", len(list))
	}

	s.DeleteGateway(types.NamespacedName{Namespace: "default", Name: "my-gw"})
	if len(s.ListGateways()) != 0 {
		t.Error("expected 0 gateways after delete")
	}
}

func TestStoreHTTPRouteCRUD(t *testing.T) {
	s := NewStore()

	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "my-route", Namespace: "default"},
	}
	s.SetHTTPRoute(hr)

	list := s.ListHTTPRoutes()
	if len(list) != 1 {
		t.Fatalf("expected 1 HTTPRoute, got %d", len(list))
	}

	s.DeleteHTTPRoute(types.NamespacedName{Namespace: "default", Name: "my-route"})
	if len(s.ListHTTPRoutes()) != 0 {
		t.Error("expected 0 HTTPRoutes after delete")
	}
}

func TestStoreEndpointSliceCRUD(t *testing.T) {
	s := NewStore()

	es := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-slice",
			Namespace: "default",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "my-svc"},
		},
	}
	s.SetEndpointSlice(es)

	slices := s.GetEndpointSlicesForService("default", "my-svc")
	if len(slices) != 1 {
		t.Fatalf("expected 1 slice, got %d", len(slices))
	}

	s.DeleteEndpointSlice(types.NamespacedName{Namespace: "default", Name: "svc-slice"})
	if len(s.GetEndpointSlicesForService("default", "my-svc")) != 0 {
		t.Error("expected 0 slices after delete")
	}
}

func TestStoreServiceCRUD(t *testing.T) {
	s := NewStore()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
		},
	}
	s.SetService(svc)

	got, ok := s.GetService(types.NamespacedName{Namespace: "default", Name: "my-svc"})
	if !ok {
		t.Fatal("expected service to exist")
	}
	if got.Name != "my-svc" {
		t.Errorf("expected my-svc, got %s", got.Name)
	}

	s.DeleteService(types.NamespacedName{Namespace: "default", Name: "my-svc"})
	_, ok = s.GetService(types.NamespacedName{Namespace: "default", Name: "my-svc"})
	if ok {
		t.Error("expected service to be deleted")
	}
}

// ---------------------------------------------------------------------------
// SecretToTLSCertPair - missing cert
// ---------------------------------------------------------------------------

func TestSecretToTLSCertPairMissingCert(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "no-cert", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSPrivateKeyKey: []byte("key-data"),
		},
	}
	_, err := SecretToTLSCertPair(secret, nil)
	if err == nil {
		t.Error("expected error for missing cert data")
	}
}

func TestSecretToTLSCertPairEmptyCert(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "empty-cert", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       {},
			corev1.TLSPrivateKeyKey: []byte("key-data"),
		},
	}
	_, err := SecretToTLSCertPair(secret, nil)
	if err == nil {
		t.Error("expected error for empty cert data")
	}
}

func TestSecretToTLSCertPairEmptyKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "empty-key", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte("cert-data"),
			corev1.TLSPrivateKeyKey: {},
		},
	}
	_, err := SecretToTLSCertPair(secret, nil)
	if err == nil {
		t.Error("expected error for empty key data")
	}
}

// ---------------------------------------------------------------------------
// ResolveTLSCertPairs - empty secret name
// ---------------------------------------------------------------------------

func TestResolveTLSCertPairsEmptySecretName(t *testing.T) {
	store := NewStore()
	entries := []TLSEntry{
		{SecretName: "", Hosts: []string{"a.example.com"}},
	}
	pairs, warnings := ResolveTLSCertPairs(store, "default", entries)
	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs for empty secret name, got %d", len(pairs))
	}
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for empty secret name, got %v", warnings)
	}
}

// ---------------------------------------------------------------------------
// Translate - gateway listener conflict warning
// ---------------------------------------------------------------------------

func TestTranslateGatewayListenerConflict(t *testing.T) {
	store := NewStore()
	controllerName := gatewayv1.GatewayController("runway.wudi.io/ingress-controller")

	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})

	store.SetGateway(&gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw1", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "runway",
			Listeners: []gatewayv1.Listener{
				{Name: "http", Port: 8080, Protocol: gatewayv1.HTTPProtocolType},
			},
		},
	})
	store.SetGateway(&gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw2", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "runway",
			Listeners: []gatewayv1.Listener{
				// Same listener ID pattern
				{Name: "http", Port: 9090, Protocol: gatewayv1.HTTPProtocolType},
			},
		},
	})

	// The listener IDs will be different (gw-default-gw1-http vs gw-default-gw2-http)
	// so no conflict. Let's use a base config that creates a conflict.
	baseCfg := &config.Config{
		Listeners: []config.ListenerConfig{
			{ID: "gw-default-gw1-http", Address: ":80", Protocol: config.ProtocolHTTP},
		},
	}

	tr := NewTranslator(store, baseCfg, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	_, warnings := tr.Translate()
	hasConflict := false
	for _, w := range warnings {
		if hasSubstring(w, "conflicts with existing listener") {
			hasConflict = true
			break
		}
	}
	if !hasConflict {
		t.Errorf("expected a conflict warning, got warnings: %v", warnings)
	}
}

// ---------------------------------------------------------------------------
// Translate - no listeners produces default
// ---------------------------------------------------------------------------

func TestTranslateDefaultListenerWhenEmpty(t *testing.T) {
	store := NewStore()
	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:     "runway",
		ControllerName:   "runway.wudi.io/ingress-controller",
		DefaultHTTPPort:  9999,
		DefaultHTTPSPort: 9443,
	})

	cfg, _ := tr.Translate()
	if len(cfg.Listeners) != 1 {
		t.Fatalf("expected 1 default listener, got %d", len(cfg.Listeners))
	}
	if cfg.Listeners[0].ID != "ingress-http" {
		t.Errorf("expected listener ID ingress-http, got %s", cfg.Listeners[0].ID)
	}
	if cfg.Listeners[0].Address != ":9999" {
		t.Errorf("expected address :9999, got %s", cfg.Listeners[0].Address)
	}
}

// ---------------------------------------------------------------------------
// resolveHTTPBackendRef - unsupported kind, weight, ExternalName
// ---------------------------------------------------------------------------

func TestResolveHTTPBackendRefUnsupportedKind(t *testing.T) {
	store := NewStore()
	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	kind := gatewayv1.Kind("ConfigMap")
	ref := gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Kind: &kind,
				Name: "my-cm",
			},
		},
	}

	backends, warnings := tr.resolveHTTPBackendRef("default", ref)
	if len(backends) != 0 {
		t.Errorf("expected 0 backends for unsupported kind, got %d", len(backends))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
}

func TestResolveHTTPBackendRefWithWeight(t *testing.T) {
	store := NewStore()

	port := int32(8080)
	ready := true
	store.SetEndpointSlice(&discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-abc",
			Namespace: "default",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "my-svc"},
		},
		Ports: []discoveryv1.EndpointPort{{Port: &port}},
		Endpoints: []discoveryv1.Endpoint{
			{Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	weight := int32(75)
	svcPort := gatewayv1.PortNumber(8080)
	ref := gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			Weight: &weight,
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Name: "my-svc",
				Port: &svcPort,
			},
		},
	}

	backends, warnings := tr.resolveHTTPBackendRef("default", ref)
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(backends))
	}
	if backends[0].Weight != 75 {
		t.Errorf("expected weight 75, got %d", backends[0].Weight)
	}
}

func TestResolveHTTPBackendRefExternalName(t *testing.T) {
	store := NewStore()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "ext-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: "external.example.com",
		},
	}
	store.SetService(svc)

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	svcPort := gatewayv1.PortNumber(8080)
	weight := int32(1)
	ref := gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			Weight: &weight,
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Name: "ext-svc",
				Port: &svcPort,
			},
		},
	}

	backends, warnings := tr.resolveHTTPBackendRef("default", ref)
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(backends))
	}
	if backends[0].URL != "http://external.example.com:8080" {
		t.Errorf("expected http://external.example.com:8080, got %s", backends[0].URL)
	}
}

func TestResolveHTTPBackendRefCrossNamespace(t *testing.T) {
	store := NewStore()
	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	ns := gatewayv1.Namespace("other-ns")
	svcPort := gatewayv1.PortNumber(3000)
	ref := gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Name:      "remote-svc",
				Port:      &svcPort,
				Namespace: &ns,
			},
		},
	}

	backends, _ := tr.resolveHTTPBackendRef("default", ref)
	// Falls back to ClusterIP since no EndpointSlice
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(backends))
	}
	if backends[0].URL != "http://remote-svc.other-ns.svc.cluster.local:3000" {
		t.Errorf("expected cross-ns ClusterIP URL, got %s", backends[0].URL)
	}
}

// ---------------------------------------------------------------------------
// resolveIngressBackend - ExternalName
// ---------------------------------------------------------------------------

func TestResolveIngressBackendExternalName(t *testing.T) {
	store := NewStore()
	className := "runway"

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "ext-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: "external.example.com",
		},
	}
	store.SetService(svc)

	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ext-ing",
			Namespace: "default",
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{Path: "/", Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: "ext-svc",
										Port: networkingv1.ServiceBackendPort{Number: 443},
									},
								}},
							},
						},
					},
				},
			},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	cfg, _ := tr.Translate()
	if len(cfg.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(cfg.Routes))
	}
	if len(cfg.Routes[0].Backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(cfg.Routes[0].Backends))
	}
	if cfg.Routes[0].Backends[0].URL != "http://external.example.com:443" {
		t.Errorf("expected http://external.example.com:443, got %s", cfg.Routes[0].Backends[0].URL)
	}
}

// ---------------------------------------------------------------------------
// resolveEndpointSliceBackends - unready endpoints skipped
// ---------------------------------------------------------------------------

func TestResolveEndpointSliceBackendsSkipsUnready(t *testing.T) {
	store := NewStore()

	port := int32(8080)
	ready := true
	notReady := false
	store.SetEndpointSlice(&discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-abc",
			Namespace: "default",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "my-svc"},
		},
		Ports: []discoveryv1.EndpointPort{{Port: &port}},
		Endpoints: []discoveryv1.Endpoint{
			{Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			{Addresses: []string{"10.0.0.2"}, Conditions: discoveryv1.EndpointConditions{Ready: &notReady}},
			{Addresses: []string{"10.0.0.3"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	backends := tr.resolveEndpointSliceBackends("default", "my-svc", 8080)
	if len(backends) != 2 {
		t.Fatalf("expected 2 ready backends, got %d", len(backends))
	}
	if backends[0].URL != "http://10.0.0.1:8080" {
		t.Errorf("expected http://10.0.0.1:8080, got %s", backends[0].URL)
	}
	if backends[1].URL != "http://10.0.0.3:8080" {
		t.Errorf("expected http://10.0.0.3:8080, got %s", backends[1].URL)
	}
}

// ---------------------------------------------------------------------------
// httpRouteToRoutes - no matches, headers, methods
// ---------------------------------------------------------------------------

func TestHTTPRouteToRoutesNoMatches(t *testing.T) {
	store := NewStore()
	controllerName := gatewayv1.GatewayController("runway.wudi.io/ingress-controller")
	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})
	store.SetGateway(&gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "runway",
			Listeners: []gatewayv1.Listener{
				{Name: "http", Port: 8080, Protocol: gatewayv1.HTTPProtocolType},
			},
		},
	})

	group := gatewayv1.Group(gatewayv1.GroupName)
	kind := gatewayv1.Kind("Gateway")

	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "no-match-route", Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{Group: &group, Kind: &kind, Name: "gw"},
				},
			},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					// No matches — should produce a catch-all
					BackendRefs: []gatewayv1.HTTPBackendRef{},
				},
			},
		},
	}

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	routes, _ := tr.httpRouteToRoutes(hr)
	if len(routes) != 1 {
		t.Fatalf("expected 1 catch-all route, got %d", len(routes))
	}
	if routes[0].Path != "/" {
		t.Errorf("expected path /, got %s", routes[0].Path)
	}
	if !routes[0].PathPrefix {
		t.Error("expected PathPrefix=true for catch-all")
	}
}

func TestHTTPRouteToRoutesWithHeadersAndMethod(t *testing.T) {
	store := NewStore()
	controllerName := gatewayv1.GatewayController("runway.wudi.io/ingress-controller")
	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})
	store.SetGateway(&gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "runway",
			Listeners: []gatewayv1.Listener{
				{Name: "http", Port: 8080, Protocol: gatewayv1.HTTPProtocolType},
			},
		},
	})

	group := gatewayv1.Group(gatewayv1.GroupName)
	kind := gatewayv1.Kind("Gateway")
	method := gatewayv1.HTTPMethodPost
	headerExact := gatewayv1.HeaderMatchExact
	headerRegex := gatewayv1.HeaderMatchRegularExpression
	pathExact := gatewayv1.PathMatchExact
	pathVal := "/exact"

	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "detailed-route", Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{Group: &group, Kind: &kind, Name: "gw"},
				},
			},
			Hostnames: []gatewayv1.Hostname{"api.test.com"},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  &pathExact,
								Value: &pathVal,
							},
							Headers: []gatewayv1.HTTPHeaderMatch{
								{
									Type:  &headerExact,
									Name:  "X-Custom",
									Value: "yes",
								},
								{
									Type:  &headerRegex,
									Name:  "X-Pattern",
									Value: "^v[0-9]+$",
								},
							},
							Method: &method,
						},
					},
				},
			},
		},
	}

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	routes, _ := tr.httpRouteToRoutes(hr)
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Path != "/exact" {
		t.Errorf("expected /exact, got %s", r.Path)
	}
	if r.PathPrefix {
		t.Error("expected PathPrefix=false for exact match")
	}
	if len(r.Methods) != 1 || r.Methods[0] != "POST" {
		t.Errorf("expected methods [POST], got %v", r.Methods)
	}
	if len(r.Match.Headers) != 2 {
		t.Fatalf("expected 2 header matches, got %d", len(r.Match.Headers))
	}
	if r.Match.Headers[0].Value != "yes" {
		t.Errorf("expected header value 'yes', got %q", r.Match.Headers[0].Value)
	}
	if r.Match.Headers[1].Regex != "^v[0-9]+$" {
		t.Errorf("expected header regex '^v[0-9]+$', got %q", r.Match.Headers[1].Regex)
	}
	if len(r.Match.Domains) != 1 || r.Match.Domains[0] != "api.test.com" {
		t.Errorf("expected domains [api.test.com], got %v", r.Match.Domains)
	}
}

// ---------------------------------------------------------------------------
// httpRouteReferencesOurGateway
// ---------------------------------------------------------------------------

func TestHTTPRouteReferencesOurGatewayVariations(t *testing.T) {
	store := NewStore()
	controllerName := gatewayv1.GatewayController("runway.wudi.io/ingress-controller")
	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})
	store.SetGateway(&gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "default"},
		Spec:       gatewayv1.GatewaySpec{GatewayClassName: "runway"},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	t.Run("different group skipped", func(t *testing.T) {
		otherGroup := gatewayv1.Group("other.io")
		hr := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "default"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{Group: &otherGroup, Name: "gw"},
					},
				},
			},
		}
		if tr.httpRouteReferencesOurGateway(hr) {
			t.Error("expected false for non-gateway group")
		}
	})

	t.Run("different kind skipped", func(t *testing.T) {
		kind := gatewayv1.Kind("Service")
		hr := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "default"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{Kind: &kind, Name: "gw"},
					},
				},
			},
		}
		if tr.httpRouteReferencesOurGateway(hr) {
			t.Error("expected false for non-Gateway kind")
		}
	})

	t.Run("explicit namespace", func(t *testing.T) {
		ns := gatewayv1.Namespace("default")
		hr := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "other"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{Name: "gw", Namespace: &ns},
					},
				},
			},
		}
		if !tr.httpRouteReferencesOurGateway(hr) {
			t.Error("expected true when explicit namespace matches gateway")
		}
	})

	t.Run("no matching gateway name", func(t *testing.T) {
		hr := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "default"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{Name: "nonexistent"},
					},
				},
			},
		}
		if tr.httpRouteReferencesOurGateway(hr) {
			t.Error("expected false for nonexistent gateway")
		}
	})

	t.Run("no parent refs", func(t *testing.T) {
		hr := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "default"},
		}
		if tr.httpRouteReferencesOurGateway(hr) {
			t.Error("expected false for empty parent refs")
		}
	})
}

// ---------------------------------------------------------------------------
// Translate with HTTPRoute filters
// ---------------------------------------------------------------------------

func TestTranslateHTTPRouteWithFilters(t *testing.T) {
	store := NewStore()
	controllerName := gatewayv1.GatewayController("runway.wudi.io/ingress-controller")

	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "runway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})
	store.SetGateway(&gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "runway",
			Listeners: []gatewayv1.Listener{
				{Name: "http", Port: 8080, Protocol: gatewayv1.HTTPProtocolType},
			},
		},
	})

	group := gatewayv1.Group(gatewayv1.GroupName)
	kind := gatewayv1.Kind("Gateway")
	svcPort := gatewayv1.PortNumber(80)

	store.SetHTTPRoute(&gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "filtered-route", Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{Group: &group, Kind: &kind, Name: "gw"},
				},
			},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "svc",
									Port: &svcPort,
								},
							},
						},
					},
					Filters: []gatewayv1.HTTPRouteFilter{
						{
							Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier,
							RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{
								Set: []gatewayv1.HTTPHeader{
									{Name: "X-Injected", Value: "true"},
								},
							},
						},
					},
				},
			},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	cfg, _ := tr.Translate()
	var found bool
	for _, r := range cfg.Routes {
		if r.Transform.Request.Headers.Set != nil {
			if r.Transform.Request.Headers.Set["X-Injected"] == "true" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected route with X-Injected request header set filter")
	}
}

// ---------------------------------------------------------------------------
// Ingress with TLS and no hosts (wildcard)
// ---------------------------------------------------------------------------

func TestTranslateIngressTLSNoHosts(t *testing.T) {
	store := NewStore()
	className := "runway"
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "wild-tls", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS: []networkingv1.IngressTLS{
				{SecretName: "wild-secret"}, // no hosts = wildcard
			},
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{Path: "/", Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: "svc",
										Port: networkingv1.ServiceBackendPort{Number: 80},
									},
								}},
							},
						},
					},
				},
			},
		},
	})
	store.SetSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "wild-secret", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte("cert"),
			corev1.TLSPrivateKeyKey: []byte("key"),
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	cfg, warnings := tr.Translate()
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}

	// Should have an HTTPS listener
	var httpsFound bool
	for _, l := range cfg.Listeners {
		if l.TLS.Enabled {
			httpsFound = true
		}
	}
	if !httpsFound {
		t.Error("expected HTTPS listener for wildcard TLS entry")
	}
}

// ---------------------------------------------------------------------------
// Ingress rule with nil HTTP
// ---------------------------------------------------------------------------

func TestTranslateIngressNilHTTPRule(t *testing.T) {
	store := NewStore()
	className := "runway"
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "nil-http", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{
				{
					Host:             "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{}, // nil HTTP
				},
			},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	cfg, _ := tr.Translate()
	if len(cfg.Routes) != 0 {
		t.Errorf("expected 0 routes for nil HTTP rule, got %d", len(cfg.Routes))
	}
}

// ---------------------------------------------------------------------------
// Ingress path with no host
// ---------------------------------------------------------------------------

func TestTranslateIngressPathNoHost(t *testing.T) {
	store := NewStore()
	className := "runway"
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "no-host", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{Path: "/app", Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: "svc",
										Port: networkingv1.ServiceBackendPort{Number: 80},
									},
								}},
							},
						},
					},
				},
			},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	cfg, _ := tr.Translate()
	if len(cfg.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(cfg.Routes))
	}
	if len(cfg.Routes[0].Match.Domains) != 0 {
		t.Errorf("expected no domains for hostless rule, got %v", cfg.Routes[0].Match.Domains)
	}
}

// ---------------------------------------------------------------------------
// Ingress with Exact path type
// ---------------------------------------------------------------------------

func TestTranslateIngressExactPath(t *testing.T) {
	store := NewStore()
	className := "runway"
	exactType := networkingv1.PathTypeExact
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "exact-path", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/exact",
									PathType: &exactType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "svc",
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	cfg, _ := tr.Translate()
	if len(cfg.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(cfg.Routes))
	}
	if cfg.Routes[0].PathPrefix {
		t.Error("expected PathPrefix=false for exact path type")
	}
}

// ---------------------------------------------------------------------------
// Ingress path with empty path string
// ---------------------------------------------------------------------------

func TestTranslateIngressEmptyPath(t *testing.T) {
	store := NewStore()
	className := "runway"
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "empty-path", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path: "",
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "svc",
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	cfg, _ := tr.Translate()
	if len(cfg.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(cfg.Routes))
	}
	if cfg.Routes[0].Path != "/" {
		t.Errorf("expected path / for empty path, got %s", cfg.Routes[0].Path)
	}
}

// ---------------------------------------------------------------------------
// isIP edge cases
// ---------------------------------------------------------------------------

func TestIsIPEdgeCases(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", true},   // empty string has no non-IP characters
		{"0", true},  // single digit
		{"a", true},  // hex char
		{"f", true},  // hex char
		{"g", false}, // not hex
		{"127.0.0.1", true},
		{"2001:db8::1", true},
		{"my-host", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := isIP(tt.input); got != tt.want {
				t.Errorf("isIP(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Translate with ingress listener merge into existing base
// ---------------------------------------------------------------------------

func TestTranslateIngressTLSMergeIntoExistingListener(t *testing.T) {
	store := NewStore()
	className := "runway"

	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-merge", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS: []networkingv1.IngressTLS{
				{SecretName: "cert1", Hosts: []string{"a.example.com"}},
			},
			Rules: []networkingv1.IngressRule{
				{
					Host: "a.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{Path: "/", Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: "svc",
										Port: networkingv1.ServiceBackendPort{Number: 80},
									},
								}},
							},
						},
					},
				},
			},
		},
	})
	store.SetSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cert1", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte("cert"),
			corev1.TLSPrivateKeyKey: []byte("key"),
		},
	})

	baseCfg := &config.Config{
		Listeners: []config.ListenerConfig{
			{
				ID:       "ingress-https",
				Address:  ":8443",
				Protocol: config.ProtocolHTTP,
				TLS: config.TLSConfig{
					Enabled: true,
					Certificates: []config.TLSCertPair{
						{CertData: []byte("existing-cert"), KeyData: []byte("existing-key")},
					},
				},
			},
		},
	}

	tr := NewTranslator(store, baseCfg, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})

	cfg, _ := tr.Translate()

	var httpsListener *config.ListenerConfig
	for i := range cfg.Listeners {
		if cfg.Listeners[i].ID == "ingress-https" {
			httpsListener = &cfg.Listeners[i]
			break
		}
	}
	if httpsListener == nil {
		t.Fatal("expected ingress-https listener")
	}
	if !httpsListener.TLS.Enabled {
		t.Error("expected TLS to be enabled on merged listener")
	}
	// Should have the existing cert + the new one
	if len(httpsListener.TLS.Certificates) < 2 {
		t.Errorf("expected at least 2 certificates after merge, got %d", len(httpsListener.TLS.Certificates))
	}
}

// ---------------------------------------------------------------------------
// NewTranslator default ports
// ---------------------------------------------------------------------------

func TestNewTranslatorDefaultPorts(t *testing.T) {
	store := NewStore()
	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "runway",
		ControllerName: "runway.wudi.io/ingress-controller",
	})
	if tr.defaultHTTPPort != 8080 {
		t.Errorf("expected default HTTP port 8080, got %d", tr.defaultHTTPPort)
	}
	if tr.defaultHTTPSPort != 8443 {
		t.Errorf("expected default HTTPS port 8443, got %d", tr.defaultHTTPSPort)
	}
}

func TestNewTranslatorCustomPorts(t *testing.T) {
	store := NewStore()
	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:    "runway",
		ControllerName:  "runway.wudi.io/ingress-controller",
		DefaultHTTPPort: 80,
		DefaultHTTPSPort: 443,
	})
	if tr.defaultHTTPPort != 80 {
		t.Errorf("expected HTTP port 80, got %d", tr.defaultHTTPPort)
	}
	if tr.defaultHTTPSPort != 443 {
		t.Errorf("expected HTTPS port 443, got %d", tr.defaultHTTPSPort)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newEmptyRouteConfig(id string) config.RouteConfig {
	return config.RouteConfig{ID: id}
}

func hasSubstring(s, sub string) bool {
	return len(s) >= len(sub) && containsStr(s, sub)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
