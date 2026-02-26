package ingress

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestStoreGenerationCounter(t *testing.T) {
	s := NewStore()
	if g := s.Generation(); g != 0 {
		t.Fatalf("expected generation 0, got %d", g)
	}

	s.SetIngress(&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "default"}})
	if g := s.Generation(); g != 1 {
		t.Fatalf("expected generation 1, got %d", g)
	}

	s.SetIngress(&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "default"}})
	if g := s.Generation(); g != 2 {
		t.Fatalf("expected generation 2, got %d", g)
	}

	s.DeleteIngress(types.NamespacedName{Namespace: "default", Name: "a"})
	if g := s.Generation(); g != 3 {
		t.Fatalf("expected generation 3, got %d", g)
	}
}

func TestStoreIngressCRUD(t *testing.T) {
	s := NewStore()
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "ns"}}
	s.SetIngress(ing)

	list := s.ListIngresses()
	if len(list) != 1 {
		t.Fatalf("expected 1 ingress, got %d", len(list))
	}
	if list[0].Name != "test" {
		t.Errorf("expected name test, got %s", list[0].Name)
	}

	s.DeleteIngress(types.NamespacedName{Namespace: "ns", Name: "test"})
	if len(s.ListIngresses()) != 0 {
		t.Error("expected 0 ingresses after delete")
	}
}

func TestStoreEndpointSlicesForService(t *testing.T) {
	s := NewStore()
	es1 := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-abc",
			Namespace: "default",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "my-svc"},
		},
	}
	es2 := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-def",
			Namespace: "default",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "other"},
		},
	}
	s.SetEndpointSlice(es1)
	s.SetEndpointSlice(es2)

	slices := s.GetEndpointSlicesForService("default", "my-svc")
	if len(slices) != 1 {
		t.Fatalf("expected 1 slice for my-svc, got %d", len(slices))
	}
	if slices[0].Name != "svc-abc" {
		t.Errorf("expected svc-abc, got %s", slices[0].Name)
	}
}

func TestStoreSecretCRUD(t *testing.T) {
	s := NewStore()
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "tls-secret", Namespace: "default"}}
	s.SetSecret(sec)

	got, ok := s.GetSecret(types.NamespacedName{Namespace: "default", Name: "tls-secret"})
	if !ok {
		t.Fatal("expected secret to exist")
	}
	if got.Name != "tls-secret" {
		t.Errorf("expected tls-secret, got %s", got.Name)
	}

	s.DeleteSecret(types.NamespacedName{Namespace: "default", Name: "tls-secret"})
	_, ok = s.GetSecret(types.NamespacedName{Namespace: "default", Name: "tls-secret"})
	if ok {
		t.Error("expected secret to be deleted")
	}
}

func TestStoreGatewayClassCRUD(t *testing.T) {
	s := NewStore()
	gc := &gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "runway"}}
	s.SetGatewayClass(gc)

	got, ok := s.GetGatewayClass("runway")
	if !ok {
		t.Fatal("expected runway class to exist")
	}
	if got.Name != "runway" {
		t.Errorf("expected runway, got %s", got.Name)
	}

	s.DeleteGatewayClass("runway")
	_, ok = s.GetGatewayClass("runway")
	if ok {
		t.Error("expected runway class to be deleted")
	}
}
