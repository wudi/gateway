package ingress

import (
	"sync"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Store holds a thread-safe in-memory snapshot of Kubernetes resources
// relevant to the ingress controller. Each mutation increments a generation
// counter used to skip redundant config rebuilds.
type Store struct {
	mu sync.RWMutex

	ingresses      map[types.NamespacedName]*networkingv1.Ingress
	gateways       map[types.NamespacedName]*gatewayv1.Gateway
	gatewayClasses map[string]*gatewayv1.GatewayClass
	httpRoutes     map[types.NamespacedName]*gatewayv1.HTTPRoute
	endpointSlices map[types.NamespacedName]*discoveryv1.EndpointSlice
	secrets        map[types.NamespacedName]*corev1.Secret
	services       map[types.NamespacedName]*corev1.Service

	generation atomic.Int64
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{
		ingresses:      make(map[types.NamespacedName]*networkingv1.Ingress),
		gateways:       make(map[types.NamespacedName]*gatewayv1.Gateway),
		gatewayClasses: make(map[string]*gatewayv1.GatewayClass),
		httpRoutes:     make(map[types.NamespacedName]*gatewayv1.HTTPRoute),
		endpointSlices: make(map[types.NamespacedName]*discoveryv1.EndpointSlice),
		secrets:        make(map[types.NamespacedName]*corev1.Secret),
		services:       make(map[types.NamespacedName]*corev1.Service),
	}
}

// Generation returns the current generation counter.
func (s *Store) Generation() int64 {
	return s.generation.Load()
}

// --- Ingresses ---

func (s *Store) SetIngress(ing *networkingv1.Ingress) {
	s.mu.Lock()
	s.ingresses[keyOf(ing.Namespace, ing.Name)] = ing
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) DeleteIngress(key types.NamespacedName) {
	s.mu.Lock()
	delete(s.ingresses, key)
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) ListIngresses() []*networkingv1.Ingress {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*networkingv1.Ingress, 0, len(s.ingresses))
	for _, v := range s.ingresses {
		out = append(out, v)
	}
	return out
}

// --- Gateways ---

func (s *Store) SetGateway(gw *gatewayv1.Gateway) {
	s.mu.Lock()
	s.gateways[keyOf(gw.Namespace, gw.Name)] = gw
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) DeleteGateway(key types.NamespacedName) {
	s.mu.Lock()
	delete(s.gateways, key)
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) ListGateways() []*gatewayv1.Gateway {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*gatewayv1.Gateway, 0, len(s.gateways))
	for _, v := range s.gateways {
		out = append(out, v)
	}
	return out
}

// --- GatewayClasses ---

func (s *Store) SetGatewayClass(gc *gatewayv1.GatewayClass) {
	s.mu.Lock()
	s.gatewayClasses[gc.Name] = gc
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) DeleteGatewayClass(name string) {
	s.mu.Lock()
	delete(s.gatewayClasses, name)
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) GetGatewayClass(name string) (*gatewayv1.GatewayClass, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	gc, ok := s.gatewayClasses[name]
	return gc, ok
}

// --- HTTPRoutes ---

func (s *Store) SetHTTPRoute(hr *gatewayv1.HTTPRoute) {
	s.mu.Lock()
	s.httpRoutes[keyOf(hr.Namespace, hr.Name)] = hr
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) DeleteHTTPRoute(key types.NamespacedName) {
	s.mu.Lock()
	delete(s.httpRoutes, key)
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) ListHTTPRoutes() []*gatewayv1.HTTPRoute {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*gatewayv1.HTTPRoute, 0, len(s.httpRoutes))
	for _, v := range s.httpRoutes {
		out = append(out, v)
	}
	return out
}

// --- EndpointSlices ---

func (s *Store) SetEndpointSlice(es *discoveryv1.EndpointSlice) {
	s.mu.Lock()
	s.endpointSlices[keyOf(es.Namespace, es.Name)] = es
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) DeleteEndpointSlice(key types.NamespacedName) {
	s.mu.Lock()
	delete(s.endpointSlices, key)
	s.generation.Add(1)
	s.mu.Unlock()
}

// GetEndpointSlicesForService returns all EndpointSlices owned by the given service.
func (s *Store) GetEndpointSlicesForService(namespace, serviceName string) []*discoveryv1.EndpointSlice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*discoveryv1.EndpointSlice
	for _, es := range s.endpointSlices {
		if es.Namespace != namespace {
			continue
		}
		// EndpointSlice labels contain the service name.
		if es.Labels[discoveryv1.LabelServiceName] == serviceName {
			out = append(out, es)
		}
	}
	return out
}

// --- Secrets ---

func (s *Store) SetSecret(secret *corev1.Secret) {
	s.mu.Lock()
	s.secrets[keyOf(secret.Namespace, secret.Name)] = secret
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) DeleteSecret(key types.NamespacedName) {
	s.mu.Lock()
	delete(s.secrets, key)
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) GetSecret(key types.NamespacedName) (*corev1.Secret, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sec, ok := s.secrets[key]
	return sec, ok
}

// --- Services ---

func (s *Store) SetService(svc *corev1.Service) {
	s.mu.Lock()
	s.services[keyOf(svc.Namespace, svc.Name)] = svc
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) DeleteService(key types.NamespacedName) {
	s.mu.Lock()
	delete(s.services, key)
	s.generation.Add(1)
	s.mu.Unlock()
}

func (s *Store) GetService(key types.NamespacedName) (*corev1.Service, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.services[key]
	return svc, ok
}

func keyOf(namespace, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: name}
}
