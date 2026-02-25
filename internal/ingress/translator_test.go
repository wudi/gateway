package ingress

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/wudi/gateway/config"
)

func ptr[T any](v T) *T { return &v }

func TestTranslateIngress(t *testing.T) {
	store := NewStore()

	pathPrefix := networkingv1.PathTypePrefix
	className := "gateway"
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ing",
			Namespace: "default",
			Annotations: map[string]string{
				AnnRetryMax: "3",
				AnnTimeout:  "10s",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/api",
									PathType: &pathPrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "api-svc",
											Port: networkingv1.ServiceBackendPort{Number: 8080},
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

	// Add endpoint slice for the backend service
	port := int32(8080)
	ready := true
	store.SetEndpointSlice(&discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-svc-abc",
			Namespace: "default",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "api-svc"},
		},
		Ports: []discoveryv1.EndpointPort{
			{Port: &port},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses:  []string{"10.0.0.1"},
				Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			},
			{
				Addresses:  []string{"10.0.0.2"},
				Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			},
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "gateway",
		ControllerName: "apigw.dev/ingress-controller",
	})

	cfg, warnings := tr.Translate()
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}

	if len(cfg.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(cfg.Routes))
	}

	route := cfg.Routes[0]
	if route.Path != "/api" {
		t.Errorf("expected path /api, got %s", route.Path)
	}
	if !route.PathPrefix {
		t.Error("expected PathPrefix=true")
	}
	if len(route.Match.Domains) != 1 || route.Match.Domains[0] != "example.com" {
		t.Errorf("expected domains [example.com], got %v", route.Match.Domains)
	}
	if len(route.Backends) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(route.Backends))
	}
	if route.Backends[0].URL != "http://10.0.0.1:8080" {
		t.Errorf("expected http://10.0.0.1:8080, got %s", route.Backends[0].URL)
	}
	if route.Retries != 3 {
		t.Errorf("expected 3 retries, got %d", route.Retries)
	}
}

func TestTranslateIngressClassFilter(t *testing.T) {
	store := NewStore()

	// Ingress with wrong class
	otherClass := "nginx"
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "wrong-class", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &otherClass,
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{Path: "/ignored", Backend: networkingv1.IngressBackend{
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
		IngressClass:   "gateway",
		ControllerName: "apigw.dev/ingress-controller",
	})

	cfg, _ := tr.Translate()
	if len(cfg.Routes) != 0 {
		t.Errorf("expected 0 routes for wrong class, got %d", len(cfg.Routes))
	}
}

func TestTranslateIngressWithoutClass(t *testing.T) {
	store := NewStore()

	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "no-class", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
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

	// Without flag: should be ignored
	tr1 := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:      "gateway",
		ControllerName:    "apigw.dev/ingress-controller",
		WatchWithoutClass: false,
	})
	cfg1, _ := tr1.Translate()
	if len(cfg1.Routes) != 0 {
		t.Errorf("expected 0 routes without --watch-ingress-without-class, got %d", len(cfg1.Routes))
	}

	// With flag: should be included
	tr2 := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:      "gateway",
		ControllerName:    "apigw.dev/ingress-controller",
		WatchWithoutClass: true,
	})
	cfg2, _ := tr2.Translate()
	if len(cfg2.Routes) != 1 {
		t.Errorf("expected 1 route with --watch-ingress-without-class, got %d", len(cfg2.Routes))
	}
}

func TestTranslateIngressTLS(t *testing.T) {
	store := NewStore()

	className := "gateway"
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-ing", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS: []networkingv1.IngressTLS{
				{
					Hosts:      []string{"secure.example.com"},
					SecretName: "tls-secret",
				},
			},
			Rules: []networkingv1.IngressRule{
				{
					Host: "secure.example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{Path: "/", Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: "svc",
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

	store.SetSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-secret", Namespace: "default"},
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte("fake-cert-pem"),
			corev1.TLSPrivateKeyKey: []byte("fake-key-pem"),
		},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "gateway",
		ControllerName: "apigw.dev/ingress-controller",
	})

	cfg, warnings := tr.Translate()
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}

	// Should have an HTTPS listener
	var httpsFound bool
	for _, l := range cfg.Listeners {
		if l.TLS.Enabled && len(l.TLS.Certificates) > 0 {
			httpsFound = true
			if string(l.TLS.Certificates[0].CertData) != "fake-cert-pem" {
				t.Errorf("unexpected cert data: %s", string(l.TLS.Certificates[0].CertData))
			}
			if l.TLS.Certificates[0].Hosts[0] != "secure.example.com" {
				t.Errorf("expected host secure.example.com, got %v", l.TLS.Certificates[0].Hosts)
			}
		}
	}
	if !httpsFound {
		t.Error("expected an HTTPS listener with TLS certificates")
	}
}

func TestTranslateHTTPRoute(t *testing.T) {
	store := NewStore()

	controllerName := gatewayv1.GatewayController("apigw.dev/ingress-controller")
	store.SetGatewayClass(&gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "gateway"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	})

	store.SetGateway(&gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "main-gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "gateway",
			Listeners: []gatewayv1.Listener{
				{
					Name:     "http",
					Port:     8080,
					Protocol: gatewayv1.HTTPProtocolType,
				},
			},
		},
	})

	group := gatewayv1.Group(gatewayv1.GroupName)
	kind := gatewayv1.Kind("Gateway")
	pathType := gatewayv1.PathMatchPathPrefix
	pathVal := "/v1"
	svcPort := gatewayv1.PortNumber(8080)

	store.SetHTTPRoute(&gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "test-route", Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Group: &group,
						Kind:  &kind,
						Name:  "main-gw",
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{"api.example.com"},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  &pathType,
								Value: &pathVal,
							},
						},
					},
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "backend-svc",
									Port: &svcPort,
								},
							},
						},
					},
				},
			},
		},
	})

	// Add endpoint slices
	port := int32(8080)
	ready := true
	store.SetEndpointSlice(&discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-svc-abc",
			Namespace: "default",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "backend-svc"},
		},
		Ports:     []discoveryv1.EndpointPort{{Port: &port}},
		Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.0.1.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}}},
	})

	tr := NewTranslator(store, nil, TranslatorConfig{
		IngressClass:   "gateway",
		ControllerName: "apigw.dev/ingress-controller",
	})

	cfg, warnings := tr.Translate()
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}

	// Find the HTTPRoute-derived route
	var found bool
	for _, r := range cfg.Routes {
		if r.Path == "/v1" {
			found = true
			if !r.PathPrefix {
				t.Error("expected PathPrefix=true for HTTPRoute prefix match")
			}
			if len(r.Match.Domains) != 1 || r.Match.Domains[0] != "api.example.com" {
				t.Errorf("expected domains [api.example.com], got %v", r.Match.Domains)
			}
			if len(r.Backends) != 1 || r.Backends[0].URL != "http://10.0.1.1:8080" {
				t.Errorf("expected backend http://10.0.1.1:8080, got %v", r.Backends)
			}
		}
	}
	if !found {
		t.Error("expected to find HTTPRoute-derived route with path /v1")
	}
}

func TestTranslateBaseConfigMerge(t *testing.T) {
	store := NewStore()

	baseCfg := &config.Config{
		Listeners: []config.ListenerConfig{
			{ID: "base-http", Address: ":9090", Protocol: config.ProtocolHTTP},
		},
		Routes: []config.RouteConfig{
			{ID: "base-route", Path: "/health", PathPrefix: false},
		},
	}

	className := "gateway"
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-ing", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{Path: "/app", Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: "app",
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

	tr := NewTranslator(store, baseCfg, TranslatorConfig{
		IngressClass:   "gateway",
		ControllerName: "apigw.dev/ingress-controller",
	})

	cfg, _ := tr.Translate()

	// Should have both base and K8s routes
	if len(cfg.Routes) != 2 {
		t.Fatalf("expected 2 routes (base + k8s), got %d", len(cfg.Routes))
	}

	// Should still have the base listener
	var baseListenerFound bool
	for _, l := range cfg.Listeners {
		if l.ID == "base-http" {
			baseListenerFound = true
		}
	}
	if !baseListenerFound {
		t.Error("expected base listener to be preserved")
	}
}

func TestTranslateClusterIPMode(t *testing.T) {
	store := NewStore()

	className := "gateway"
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "clusterip-ing",
			Namespace: "prod",
			Annotations: map[string]string{
				AnnUpstreamMode: "clusterip",
			},
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
										Name: "api",
										Port: networkingv1.ServiceBackendPort{Number: 8080},
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
		IngressClass:   "gateway",
		ControllerName: "apigw.dev/ingress-controller",
	})

	cfg, _ := tr.Translate()
	if len(cfg.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(cfg.Routes))
	}
	if len(cfg.Routes[0].Backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(cfg.Routes[0].Backends))
	}
	expected := "http://api.prod.svc.cluster.local:8080"
	if cfg.Routes[0].Backends[0].URL != expected {
		t.Errorf("expected %s, got %s", expected, cfg.Routes[0].Backends[0].URL)
	}
}

func TestHostnameMatch(t *testing.T) {
	tests := []struct {
		hostname, pattern string
		match             bool
	}{
		{"example.com", "example.com", true},
		{"foo.example.com", "*.example.com", true},
		{"bar.example.com", "*.example.com", true},
		{"deep.foo.example.com", "*.example.com", false},
		{"other.com", "*.example.com", false},
	}
	for _, tt := range tests {
		if got := hostnameMatch(tt.hostname, tt.pattern); got != tt.match {
			t.Errorf("hostnameMatch(%q, %q) = %v, want %v", tt.hostname, tt.pattern, got, tt.match)
		}
	}
}
