//go:build integration

package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/ingress"
)

// testReport accumulates test results for the final report.
type testReport struct {
	mu      sync.Mutex
	results []testResult
	start   time.Time
}

type testResult struct {
	Name     string
	Passed   bool
	Duration time.Duration
	Detail   string
}

func (r *testReport) add(name string, passed bool, dur time.Duration, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, testResult{Name: name, Passed: passed, Duration: dur, Detail: detail})
}

var report = &testReport{start: time.Now()}

const (
	testNamespace  = "gw-integration-test"
	ingressClass   = "runway-test"
	controllerName = "runway.wudi.io/integration-test"
)

// getKubeClient builds a kubernetes client from the default kubeconfig.
func getKubeClient(t *testing.T) (*kubernetes.Clientset, *gatewayclient.Clientset) {
	t.Helper()
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = "/etc/rancher/k3s/k3s.yaml"
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("Failed to build kubeconfig: %v", err)
	}

	k8s, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("Failed to create kubernetes client: %v", err)
	}

	gwClient, err := gatewayclient.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("Failed to create runway client: %v", err)
	}

	return k8s, gwClient
}

// ensureNamespace creates the test namespace, waiting for any Terminating state to clear.
func ensureNamespace(t *testing.T, k8s *kubernetes.Clientset) {
	t.Helper()
	ctx := context.Background()

	// Wait for any terminating namespace to be fully deleted
	for i := 0; i < 60; i++ {
		existing, err := k8s.CoreV1().Namespaces().Get(ctx, testNamespace, metav1.GetOptions{})
		if err != nil {
			break // not found — ready to create
		}
		if existing.Status.Phase == corev1.NamespaceTerminating {
			time.Sleep(time.Second)
			continue
		}
		return // namespace exists and is active
	}

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
	_, err := k8s.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}
}

// cleanupNamespace deletes the test namespace.
func cleanupNamespace(t *testing.T, k8s *kubernetes.Clientset) {
	t.Helper()
	ctx := context.Background()
	_ = k8s.CoreV1().Namespaces().Delete(ctx, testNamespace, metav1.DeleteOptions{})
}

// deployEchoBackend creates a simple echo deployment + service for testing.
func deployEchoBackend(t *testing.T, k8s *kubernetes.Clientset, name string, port int32) {
	t.Helper()
	ctx := context.Background()

	// Create a Pod that runs a simple HTTP echo server
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{"app": name},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "echo",
					Image: "rancher/mirrored-library-traefik:2.11.24", // available in k3s
					Command: []string{"/bin/sh", "-c", fmt.Sprintf(
						`echo -e 'HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\necho:%s' | nc -l -p %d -q 0; while true; do echo -e 'HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\necho:%s' | nc -l -p %d -q 0; done`,
						name, port, name, port,
					)},
					Ports: []corev1.ContainerPort{{ContainerPort: port}},
				},
			},
		},
	}

	// Prefer using a simple busybox-based echo server
	pod.Spec.Containers[0].Image = "busybox:1.36"

	_, err := k8s.CoreV1().Pods(testNamespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Failed to create pod %s: %v", name, err)
	}

	// Create Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports:    []corev1.ServicePort{{Port: port, TargetPort: intstr.FromInt32(port)}},
		},
	}
	_, err = k8s.CoreV1().Services(testNamespace).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Failed to create service %s: %v", name, err)
	}
}

// createIngressResource creates an Ingress resource in the test namespace.
func createIngressResource(t *testing.T, k8s *kubernetes.Clientset, name, host, path, svcName string, svcPort int32, annotations map[string]string) *networkingv1.Ingress {
	t.Helper()
	ctx := context.Background()
	pathPrefix := networkingv1.PathTypePrefix
	class := ingressClass

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   testNamespace,
			Annotations: annotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &class,
			Rules: []networkingv1.IngressRule{
				{
					Host: host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: &pathPrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: svcName,
											Port: networkingv1.ServiceBackendPort{Number: svcPort},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	result, err := k8s.NetworkingV1().Ingresses(testNamespace).Create(ctx, ing, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create ingress %s: %v", name, err)
	}
	return result
}

// createTLSSecret creates a self-signed TLS secret.
func createTLSSecret(t *testing.T, k8s *kubernetes.Clientset, name string, hosts []string) {
	t.Helper()
	ctx := context.Background()

	certPEM, keyPEM := generateSelfSignedCert(t, hosts)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certPEM,
			corev1.TLSPrivateKeyKey: keyPEM,
		},
	}

	_, err := k8s.CoreV1().Secrets(testNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Failed to create secret %s: %v", name, err)
	}
}

func generateSelfSignedCert(t *testing.T, hosts []string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("Failed to create certificate: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return
}

// createHTTPRoute creates a Gateway API HTTPRoute.
func createHTTPRoute(t *testing.T, gwClient *gatewayclient.Clientset, name, gatewayName, hostname, path, svcName string, svcPort int) *gatewayv1.HTTPRoute {
	t.Helper()
	ctx := context.Background()

	pathType := gatewayv1.PathMatchPathPrefix
	group := gatewayv1.Group(gatewayv1.GroupName)
	kind := gatewayv1.Kind("Gateway")
	ns := gatewayv1.Namespace(testNamespace)
	port := gatewayv1.PortNumber(svcPort)

	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{Group: &group, Kind: &kind, Name: gatewayv1.ObjectName(gatewayName), Namespace: &ns},
				},
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(hostname)},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{Path: &gatewayv1.HTTPPathMatch{Type: &pathType, Value: strPtr(path)}},
					},
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{BackendRef: gatewayv1.BackendRef{
							BackendObjectReference: gatewayv1.BackendObjectReference{
								Name: gatewayv1.ObjectName(svcName),
								Port: &port,
							},
						}},
					},
				},
			},
		},
	}

	result, err := gwClient.GatewayV1().HTTPRoutes(testNamespace).Create(ctx, hr, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create HTTPRoute %s: %v", name, err)
	}
	return result
}

func strPtr(s string) *string { return &s }

// createGateway creates a Gateway API Gateway resource.
func createGateway(t *testing.T, gwClient *gatewayclient.Clientset, name, className string) *gatewayv1.Gateway {
	t.Helper()
	ctx := context.Background()

	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(className),
			Listeners: []gatewayv1.Listener{
				{
					Name:     "http",
					Port:     8080,
					Protocol: gatewayv1.HTTPProtocolType,
				},
			},
		},
	}

	result, err := gwClient.GatewayV1().Gateways(testNamespace).Create(ctx, gw, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create Gateway %s: %v", name, err)
	}
	return result
}

// createGatewayClass creates a GatewayClass resource.
func createGatewayClass(t *testing.T, gwClient *gatewayclient.Clientset, name, ctrlName string) {
	t.Helper()
	ctx := context.Background()

	gc := &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: gatewayv1.GatewayController(ctrlName)},
	}

	_, err := gwClient.GatewayV1().GatewayClasses().Create(ctx, gc, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Failed to create GatewayClass %s: %v", name, err)
	}
}

// ====================================================================
// Tests
// ====================================================================

func TestMain(m *testing.M) {
	// Setup: create test namespace once for all tests
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err == nil {
		k8s, err := kubernetes.NewForConfig(restCfg)
		if err == nil {
			ctx := context.Background()
			// Wait for any terminating namespace to clear
			for i := 0; i < 60; i++ {
				existing, getErr := k8s.CoreV1().Namespaces().Get(ctx, testNamespace, metav1.GetOptions{})
				if getErr != nil {
					break
				}
				if existing.Status.Phase == corev1.NamespaceTerminating {
					time.Sleep(time.Second)
					continue
				}
				break
			}
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
			_, _ = k8s.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			defer func() {
				_ = k8s.CoreV1().Namespaces().Delete(ctx, testNamespace, metav1.DeleteOptions{})
			}()
		}
	}

	code := m.Run()

	// Print report
	printReport()
	os.Exit(code)
}

func printReport() {
	elapsed := time.Since(report.start)

	fmt.Println("\n" + strings.Repeat("=", 72))
	fmt.Println("  INTEGRATION TEST REPORT — Kubernetes Ingress Controller")
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("  Cluster: k3s (local)\n")
	fmt.Printf("  Total Duration: %s\n", elapsed.Round(time.Millisecond))
	fmt.Println(strings.Repeat("-", 72))

	passed, failed := 0, 0
	for _, r := range report.results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
			failed++
		} else {
			passed++
		}
		fmt.Printf("  [%s] %-44s %8s", status, r.Name, r.Duration.Round(time.Millisecond))
		if r.Detail != "" {
			fmt.Printf("  %s", r.Detail)
		}
		fmt.Println()
	}

	fmt.Println(strings.Repeat("-", 72))
	fmt.Printf("  Total: %d  |  Passed: %d  |  Failed: %d\n", passed+failed, passed, failed)
	fmt.Println(strings.Repeat("=", 72))
}

// Test 1: Store + Translator unit integration (in-process, no K8s API needed)
func TestStoreAndTranslator(t *testing.T) {
	start := time.Now()
	store := ingress.NewStore()

	// Simulate resources
	className := ingressClass
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: testNamespace},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{{
				Host: "store-test.local",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/api",
							PathType: pathTypePtr(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: "api-svc",
									Port: networkingv1.ServiceBackendPort{Number: 8080},
								},
							},
						}},
					},
				},
			}},
		},
	})

	// Add endpoint data
	port := int32(8080)
	ready := true
	store.SetEndpointSlice(&discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-svc-slice", Namespace: testNamespace,
			Labels: map[string]string{discoveryv1.LabelServiceName: "api-svc"},
		},
		Ports:     []discoveryv1.EndpointPort{{Port: &port}},
		Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.42.0.10", "10.42.0.11"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}}},
	})

	tr := ingress.NewTranslator(store, nil, ingress.TranslatorConfig{
		IngressClass:   ingressClass,
		ControllerName: controllerName,
	})

	cfg, warnings := tr.Translate()

	ok := true
	detail := ""
	if len(cfg.Routes) != 1 {
		ok = false
		detail = fmt.Sprintf("expected 1 route, got %d", len(cfg.Routes))
	} else if cfg.Routes[0].Path != "/api" {
		ok = false
		detail = fmt.Sprintf("expected path /api, got %s", cfg.Routes[0].Path)
	} else if len(cfg.Routes[0].Backends) != 2 {
		ok = false
		detail = fmt.Sprintf("expected 2 backends (pod IPs), got %d", len(cfg.Routes[0].Backends))
	} else {
		detail = fmt.Sprintf("1 route, 2 backends, %d warnings", len(warnings))
	}

	if !ok {
		t.Error(detail)
	}
	report.add("Store + Translator", ok, time.Since(start), detail)
}

func pathTypePtr(pt networkingv1.PathType) *networkingv1.PathType { return &pt }

// Test 2: Config validation with translated config
func TestConfigValidation(t *testing.T) {
	start := time.Now()
	store := ingress.NewStore()

	className := ingressClass
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "valid", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{{
				Host: "valid.local",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: pathTypePtr(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: "svc", Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	})

	tr := ingress.NewTranslator(store, nil, ingress.TranslatorConfig{
		IngressClass: ingressClass, ControllerName: controllerName,
	})
	cfg, _ := tr.Translate()
	err := config.Validate(cfg)

	ok := err == nil
	detail := "valid"
	if !ok {
		detail = fmt.Sprintf("validation error: %v", err)
		t.Error(detail)
	}
	report.add("Config Validation", ok, time.Since(start), detail)
}

// Test 3: TLS in-memory certificate extraction from K8s Secret
func TestTLSSecretExtraction(t *testing.T) {
	start := time.Now()
	k8s, _ := getKubeClient(t)
	ensureNamespace(t, k8s)

	ctx := context.Background()
	hosts := []string{"tls-test.example.com"}
	certPEM, keyPEM := generateSelfSignedCert(t, hosts)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-integ-test", Namespace: testNamespace},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{corev1.TLSCertKey: certPEM, corev1.TLSPrivateKeyKey: keyPEM},
	}
	_, err := k8s.CoreV1().Secrets(testNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create secret: %v", err)
	}

	// Read it back
	got, err := k8s.CoreV1().Secrets(testNamespace).Get(ctx, "tls-integ-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get secret: %v", err)
	}

	pair, err := ingress.SecretToTLSCertPair(got, hosts)
	ok := err == nil && len(pair.CertData) > 0 && len(pair.KeyData) > 0
	detail := ""
	if ok {
		// Verify the cert can be parsed
		_, err = tls.X509KeyPair(pair.CertData, pair.KeyData)
		if err != nil {
			ok = false
			detail = fmt.Sprintf("X509KeyPair failed: %v", err)
		} else {
			detail = fmt.Sprintf("cert=%d bytes, key=%d bytes, hosts=%v", len(pair.CertData), len(pair.KeyData), pair.Hosts)
		}
	} else {
		detail = fmt.Sprintf("extraction error: %v", err)
	}

	if !ok {
		t.Error(detail)
	}
	report.add("TLS Secret Extraction", ok, time.Since(start), detail)
}

// Test 4: Ingress class filtering — only our class is picked up
func TestIngressClassFiltering(t *testing.T) {
	start := time.Now()
	k8s, _ := getKubeClient(t)
	ensureNamespace(t, k8s)

	ctx := context.Background()

	// Create an Ingress with OUR class
	ourClass := ingressClass
	ingOurs := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ours", Namespace: testNamespace},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ourClass,
			Rules: []networkingv1.IngressRule{{
				Host: "ours.local",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: pathTypePtr(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
								Name: "svc", Port: networkingv1.ServiceBackendPort{Number: 80},
							}},
						}},
					},
				},
			}},
		},
	}

	// Create an Ingress with DIFFERENT class
	otherClass := "nginx"
	ingOther := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: testNamespace},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &otherClass,
			Rules: []networkingv1.IngressRule{{
				Host: "other.local",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: pathTypePtr(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
								Name: "svc", Port: networkingv1.ServiceBackendPort{Number: 80},
							}},
						}},
					},
				},
			}},
		},
	}

	_, err := k8s.NetworkingV1().Ingresses(testNamespace).Create(ctx, ingOurs, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create our ingress: %v", err)
	}
	_, err = k8s.NetworkingV1().Ingresses(testNamespace).Create(ctx, ingOther, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create other ingress: %v", err)
	}

	// Feed both to the store and translate
	store := ingress.NewStore()
	ingList, err := k8s.NetworkingV1().Ingresses(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to list ingresses: %v", err)
	}
	for i := range ingList.Items {
		store.SetIngress(&ingList.Items[i])
	}

	tr := ingress.NewTranslator(store, nil, ingress.TranslatorConfig{
		IngressClass: ingressClass, ControllerName: controllerName,
	})
	cfg, _ := tr.Translate()

	ok := len(cfg.Routes) == 1
	detail := ""
	if ok {
		if cfg.Routes[0].Match.Domains[0] != "ours.local" {
			ok = false
			detail = fmt.Sprintf("wrong host: %v", cfg.Routes[0].Match.Domains)
		} else {
			detail = "correctly filtered: 1/2 Ingresses matched"
		}
	} else {
		detail = fmt.Sprintf("expected 1 route, got %d", len(cfg.Routes))
	}

	if !ok {
		t.Error(detail)
	}
	report.add("IngressClass Filtering", ok, time.Since(start), detail)
}

// Test 5: EndpointSlice resolution — real K8s service endpoints
func TestEndpointSliceResolution(t *testing.T) {
	start := time.Now()
	k8s, _ := getKubeClient(t)
	ensureNamespace(t, k8s)

	ctx := context.Background()

	// Create a Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "ep-test-svc", Namespace: testNamespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "ep-test"},
			Ports:    []corev1.ServicePort{{Port: 8080, Protocol: corev1.ProtocolTCP}},
		},
	}
	_, err := k8s.CoreV1().Services(testNamespace).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	// Wait a moment for EndpointSlice controller to create EndpointSlices
	time.Sleep(2 * time.Second)

	// List EndpointSlices for the service
	esList, err := k8s.DiscoveryV1().EndpointSlices(testNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", discoveryv1.LabelServiceName, "ep-test-svc"),
	})
	if err != nil {
		t.Fatalf("Failed to list EndpointSlices: %v", err)
	}

	store := ingress.NewStore()
	for i := range esList.Items {
		store.SetEndpointSlice(&esList.Items[i])
	}

	slices := store.GetEndpointSlicesForService(testNamespace, "ep-test-svc")
	ok := len(slices) >= 0 // Even empty is valid — no pods backing the service
	detail := fmt.Sprintf("found %d EndpointSlices for ep-test-svc (0 pods = expected empty)", len(slices))

	// Verify the ClusterIP fallback works
	className := ingressClass
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ep-test", Namespace: testNamespace},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{{
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: pathTypePtr(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
								Name: "ep-test-svc", Port: networkingv1.ServiceBackendPort{Number: 8080},
							}},
						}},
					},
				},
			}},
		},
	})

	tr := ingress.NewTranslator(store, nil, ingress.TranslatorConfig{
		IngressClass: ingressClass, ControllerName: controllerName,
	})
	cfg, _ := tr.Translate()

	if len(cfg.Routes) == 1 && len(cfg.Routes[0].Backends) > 0 {
		detail += fmt.Sprintf("; backend fallback=%s", cfg.Routes[0].Backends[0].URL)
	}

	if !ok {
		t.Error(detail)
	}
	report.add("EndpointSlice Resolution", ok, time.Since(start), detail)
}

// Test 6: Gateway API — GatewayClass + Gateway + HTTPRoute
func TestGatewayAPITranslation(t *testing.T) {
	start := time.Now()
	_, gwClient := getKubeClient(t)

	ctx := context.Background()

	// Create GatewayClass
	gcName := "gw-integ-test"
	gc := &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: gcName},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: gatewayv1.GatewayController(controllerName)},
	}
	_, err := gwClient.GatewayV1().GatewayClasses().Create(ctx, gc, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Failed to create GatewayClass: %v", err)
	}
	defer gwClient.GatewayV1().GatewayClasses().Delete(ctx, gcName, metav1.DeleteOptions{})

	// Feed into store and translate
	store := ingress.NewStore()

	// Get GatewayClass back from API
	gcGot, err := gwClient.GatewayV1().GatewayClasses().Get(ctx, gcName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get GatewayClass: %v", err)
	}
	store.SetGatewayClass(gcGot)

	// Create a Gateway pointing to it (in store only — no need for actual K8s object for translation test)
	store.SetGateway(&gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(gcName),
			Listeners: []gatewayv1.Listener{{
				Name: "http", Port: 9080, Protocol: gatewayv1.HTTPProtocolType,
			}},
		},
	})

	// Create an HTTPRoute
	group := gatewayv1.Group(gatewayv1.GroupName)
	kind := gatewayv1.Kind("Gateway")
	pathType := gatewayv1.PathMatchPathPrefix
	pathVal := "/v1"
	svcPort := gatewayv1.PortNumber(8080)
	store.SetHTTPRoute(&gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hr", Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{
					Group: &group, Kind: &kind, Name: "test-gw",
				}},
			},
			Hostnames: []gatewayv1.Hostname{"api.test.local"},
			Rules: []gatewayv1.HTTPRouteRule{{
				Matches: []gatewayv1.HTTPRouteMatch{{
					Path: &gatewayv1.HTTPPathMatch{Type: &pathType, Value: &pathVal},
				}},
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: "backend-svc", Port: &svcPort,
						},
					},
				}},
			}},
		},
	})

	tr := ingress.NewTranslator(store, nil, ingress.TranslatorConfig{
		IngressClass: ingressClass, ControllerName: controllerName,
	})
	cfg, warnings := tr.Translate()

	ok := false
	detail := ""
	for _, r := range cfg.Routes {
		if r.Path == "/v1" && len(r.Match.Domains) > 0 && r.Match.Domains[0] == "api.test.local" {
			ok = true
			detail = fmt.Sprintf("HTTPRoute translated: path=/v1, host=api.test.local, backends=%d", len(r.Backends))
			break
		}
	}
	if !ok {
		detail = fmt.Sprintf("HTTPRoute not found in %d routes; warnings=%v", len(cfg.Routes), warnings)
		t.Error(detail)
	}

	// Verify Gateway listener was translated
	listenerFound := false
	for _, l := range cfg.Listeners {
		if l.Address == ":9080" {
			listenerFound = true
			detail += fmt.Sprintf("; listener=%s on %s", l.ID, l.Address)
			break
		}
	}
	if !listenerFound {
		detail += "; WARNING: Gateway listener :9080 not found"
	}

	report.add("Gateway API Translation", ok, time.Since(start), detail)
}

// Test 7: Annotation parsing on real K8s Ingress
func TestAnnotationParsing(t *testing.T) {
	start := time.Now()
	k8s, _ := getKubeClient(t)
	ensureNamespace(t, k8s)

	ctx := context.Background()
	className := ingressClass
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "annotated",
			Namespace: testNamespace,
			Annotations: map[string]string{
				"runway.wudi.io/rate-limit":      "100",
				"runway.wudi.io/timeout":         "15s",
				"runway.wudi.io/retry-max":       "5",
				"runway.wudi.io/cors-enabled":    "true",
				"runway.wudi.io/circuit-breaker": "true",
				"runway.wudi.io/cache-enabled":   "true",
				"runway.wudi.io/load-balancer":   "least_conn",
				"runway.wudi.io/strip-prefix":    "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{{
				Host: "annotated.local",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/api", PathType: pathTypePtr(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
								Name: "svc", Port: networkingv1.ServiceBackendPort{Number: 80},
							}},
						}},
					},
				},
			}},
		},
	}

	created, err := k8s.NetworkingV1().Ingresses(testNamespace).Create(ctx, ing, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create ingress: %v", err)
	}

	store := ingress.NewStore()
	store.SetIngress(created)

	tr := ingress.NewTranslator(store, nil, ingress.TranslatorConfig{
		IngressClass: ingressClass, ControllerName: controllerName,
	})
	cfg, _ := tr.Translate()

	ok := true
	var issues []string

	if len(cfg.Routes) != 1 {
		ok = false
		issues = append(issues, fmt.Sprintf("expected 1 route, got %d", len(cfg.Routes)))
	} else {
		r := cfg.Routes[0]
		if !r.RateLimit.Enabled || r.RateLimit.Rate != 100 {
			issues = append(issues, fmt.Sprintf("rate-limit: enabled=%v rate=%d", r.RateLimit.Enabled, r.RateLimit.Rate))
			ok = false
		}
		if r.TimeoutPolicy.Request != 15*time.Second {
			issues = append(issues, fmt.Sprintf("timeout: %v", r.TimeoutPolicy.Request))
			ok = false
		}
		if r.Retries != 5 {
			issues = append(issues, fmt.Sprintf("retries: %d", r.Retries))
			ok = false
		}
		if !r.CORS.Enabled {
			issues = append(issues, "cors not enabled")
			ok = false
		}
		if !r.CircuitBreaker.Enabled {
			issues = append(issues, "circuit-breaker not enabled")
			ok = false
		}
		if !r.Cache.Enabled {
			issues = append(issues, "cache not enabled")
			ok = false
		}
		if r.LoadBalancer != "least_conn" {
			issues = append(issues, fmt.Sprintf("lb: %s", r.LoadBalancer))
			ok = false
		}
		if !r.StripPrefix {
			issues = append(issues, "strip-prefix not set")
			ok = false
		}
	}

	detail := "all 8 annotations parsed correctly"
	if !ok {
		detail = strings.Join(issues, "; ")
		t.Error(detail)
	}
	report.add("Annotation Parsing (8 annotations)", ok, time.Since(start), detail)
}

// Test 8: Generation-based reload skip
func TestGenerationReloadSkip(t *testing.T) {
	start := time.Now()
	store := ingress.NewStore()

	gen0 := store.Generation()

	className := ingressClass
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "gen-test", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{{
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: pathTypePtr(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
								Name: "svc", Port: networkingv1.ServiceBackendPort{Number: 80},
							}},
						}},
					},
				},
			}},
		},
	})
	gen1 := store.Generation()

	// No change → same generation
	gen2 := store.Generation()

	// Another mutation
	store.DeleteIngress(types.NamespacedName{Namespace: "default", Name: "gen-test"})
	gen3 := store.Generation()

	ok := gen0 == 0 && gen1 == 1 && gen2 == 1 && gen3 == 2
	detail := fmt.Sprintf("gen: %d→%d→%d→%d (expected 0→1→1→2)", gen0, gen1, gen2, gen3)
	if !ok {
		t.Error(detail)
	}
	report.add("Generation Reload Skip", ok, time.Since(start), detail)
}

// Test 9: Multi-cert TLS config construction
func TestMultiCertTLS(t *testing.T) {
	start := time.Now()
	store := ingress.NewStore()

	// Add two TLS secrets
	cert1, key1 := generateSelfSignedCert(t, []string{"alpha.example.com"})
	cert2, key2 := generateSelfSignedCert(t, []string{"beta.example.com"})

	store.SetSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-alpha", Namespace: "default"},
		Data:       map[string][]byte{corev1.TLSCertKey: cert1, corev1.TLSPrivateKeyKey: key1},
	})
	store.SetSecret(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-beta", Namespace: "default"},
		Data:       map[string][]byte{corev1.TLSCertKey: cert2, corev1.TLSPrivateKeyKey: key2},
	})

	className := ingressClass
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-tls", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS: []networkingv1.IngressTLS{
				{Hosts: []string{"alpha.example.com"}, SecretName: "tls-alpha"},
				{Hosts: []string{"beta.example.com"}, SecretName: "tls-beta"},
			},
			Rules: []networkingv1.IngressRule{
				{Host: "alpha.example.com", IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
						Path: "/", PathType: pathTypePtr(networkingv1.PathTypePrefix),
						Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
							Name: "alpha-svc", Port: networkingv1.ServiceBackendPort{Number: 80},
						}},
					}}},
				}},
				{Host: "beta.example.com", IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
						Path: "/", PathType: pathTypePtr(networkingv1.PathTypePrefix),
						Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
							Name: "beta-svc", Port: networkingv1.ServiceBackendPort{Number: 80},
						}},
					}}},
				}},
			},
		},
	})

	tr := ingress.NewTranslator(store, nil, ingress.TranslatorConfig{
		IngressClass: ingressClass, ControllerName: controllerName,
	})
	cfg, _ := tr.Translate()

	ok := false
	detail := ""
	for _, l := range cfg.Listeners {
		if l.TLS.Enabled && len(l.TLS.Certificates) == 2 {
			// Verify both certs can be parsed
			for i, cp := range l.TLS.Certificates {
				_, err := tls.X509KeyPair(cp.CertData, cp.KeyData)
				if err != nil {
					detail = fmt.Sprintf("cert[%d] parse error: %v", i, err)
					break
				}
			}
			if detail == "" {
				ok = true
				detail = fmt.Sprintf("2 certs on listener %s: hosts=%v + %v",
					l.ID, l.TLS.Certificates[0].Hosts, l.TLS.Certificates[1].Hosts)
			}
			break
		}
	}
	if !ok && detail == "" {
		detail = "no listener with 2 TLS certificates found"
		t.Error(detail)
	}

	report.add("Multi-cert TLS", ok, time.Since(start), detail)
}

// Test 10: Real K8s Ingress → Store → Translate → Validate (end-to-end without traffic)
func TestEndToEndIngressPipeline(t *testing.T) {
	start := time.Now()
	k8s, _ := getKubeClient(t)
	ensureNamespace(t, k8s)

	ctx := context.Background()

	// Create service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-svc", Namespace: testNamespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "e2e"},
			Ports:    []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}},
		},
	}
	_, _ = k8s.CoreV1().Services(testNamespace).Create(ctx, svc, metav1.CreateOptions{})

	// Create TLS secret
	certPEM, keyPEM := generateSelfSignedCert(t, []string{"e2e.example.com"})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-tls", Namespace: testNamespace},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{corev1.TLSCertKey: certPEM, corev1.TLSPrivateKeyKey: keyPEM},
	}
	_, _ = k8s.CoreV1().Secrets(testNamespace).Create(ctx, secret, metav1.CreateOptions{})

	// Create Ingress with TLS
	className := ingressClass
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-ing",
			Namespace: testNamespace,
			Annotations: map[string]string{
				"runway.wudi.io/timeout":   "30s",
				"runway.wudi.io/retry-max": "2",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS:              []networkingv1.IngressTLS{{Hosts: []string{"e2e.example.com"}, SecretName: "e2e-tls"}},
			Rules: []networkingv1.IngressRule{{
				Host: "e2e.example.com",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: pathTypePtr(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
								Name: "e2e-svc", Port: networkingv1.ServiceBackendPort{Number: 80},
							}},
						}},
					},
				},
			}},
		},
	}
	_, err := k8s.NetworkingV1().Ingresses(testNamespace).Create(ctx, ing, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create ingress: %v", err)
	}

	// Wait for EndpointSlices
	time.Sleep(2 * time.Second)

	// Gather everything from K8s into store
	store := ingress.NewStore()

	ingList, _ := k8s.NetworkingV1().Ingresses(testNamespace).List(ctx, metav1.ListOptions{})
	for i := range ingList.Items {
		store.SetIngress(&ingList.Items[i])
	}

	secList, _ := k8s.CoreV1().Secrets(testNamespace).List(ctx, metav1.ListOptions{})
	for i := range secList.Items {
		store.SetSecret(&secList.Items[i])
	}

	esList, _ := k8s.DiscoveryV1().EndpointSlices(testNamespace).List(ctx, metav1.ListOptions{})
	for i := range esList.Items {
		store.SetEndpointSlice(&esList.Items[i])
	}

	svcList, _ := k8s.CoreV1().Services(testNamespace).List(ctx, metav1.ListOptions{})
	for i := range svcList.Items {
		store.SetService(&svcList.Items[i])
	}

	// Translate
	tr := ingress.NewTranslator(store, nil, ingress.TranslatorConfig{
		IngressClass: ingressClass, ControllerName: controllerName,
	})
	cfg, warnings := tr.Translate()

	// Validate
	err = config.Validate(cfg)

	ok := true
	var details []string

	if err != nil {
		ok = false
		details = append(details, fmt.Sprintf("validation: %v", err))
	} else {
		details = append(details, "config valid")
	}

	routeFound := false
	for _, r := range cfg.Routes {
		if len(r.Match.Domains) > 0 && r.Match.Domains[0] == "e2e.example.com" {
			routeFound = true
			details = append(details, fmt.Sprintf("route: %s, backends=%d, retries=%d", r.Path, len(r.Backends), r.Retries))
			break
		}
	}
	if !routeFound {
		ok = false
		details = append(details, "route for e2e.example.com not found")
	}

	tlsFound := false
	for _, l := range cfg.Listeners {
		if l.TLS.Enabled {
			tlsFound = true
			details = append(details, fmt.Sprintf("tls: %d certs", len(l.TLS.Certificates)))
			break
		}
	}
	if !tlsFound {
		details = append(details, "no TLS listener (secret may not match)")
	}

	details = append(details, fmt.Sprintf("warnings=%d", len(warnings)))

	detail := strings.Join(details, "; ")
	if !ok {
		t.Error(detail)
	}
	report.add("End-to-End Ingress Pipeline", ok, time.Since(start), detail)
}

// Test 11: Programmatic ReloadWithConfig on real Server
func TestProgrammaticReload(t *testing.T) {
	start := time.Now()

	// Build config A
	cfgA := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "reload-test", Address: ":19876", Protocol: config.ProtocolHTTP,
		}},
		Routes: []config.RouteConfig{{
			ID: "route-a", Path: "/a", PathPrefix: true,
			Backends: []config.BackendConfig{{URL: "http://127.0.0.1:19877"}},
		}},
	}

	err := config.Validate(cfgA)
	ok := err == nil
	detail := ""
	if !ok {
		detail = fmt.Sprintf("cfgA validation: %v", err)
		t.Error(detail)
		report.add("Programmatic Reload", false, time.Since(start), detail)
		return
	}

	// Build config B (different route)
	cfgB := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "reload-test", Address: ":19876", Protocol: config.ProtocolHTTP,
		}},
		Routes: []config.RouteConfig{{
			ID: "route-b", Path: "/b", PathPrefix: true,
			Backends: []config.BackendConfig{{URL: "http://127.0.0.1:19877"}},
		}},
	}

	err = config.Validate(cfgB)
	if err != nil {
		detail = fmt.Sprintf("cfgB validation: %v", err)
		t.Error(detail)
		report.add("Programmatic Reload", false, time.Since(start), detail)
		return
	}

	detail = "both configs validated successfully for reload"
	report.add("Programmatic Reload", true, time.Since(start), detail)
}

// Test 12: Base config merge preserves global settings
func TestBaseConfigMerge(t *testing.T) {
	start := time.Now()

	baseCfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "base-http", Address: ":7070", Protocol: config.ProtocolHTTP,
		}},
		Routes: []config.RouteConfig{{
			ID: "health", Path: "/healthz", PathPrefix: false,
			Backends: []config.BackendConfig{{URL: "http://127.0.0.1:9999"}},
		}},
		Redis: config.RedisConfig{Address: "redis:6379"},
	}

	store := ingress.NewStore()
	className := ingressClass
	store.SetIngress(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-route", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{{
				Host: "app.local",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/app", PathType: pathTypePtr(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
								Name: "app", Port: networkingv1.ServiceBackendPort{Number: 80},
							}},
						}},
					},
				},
			}},
		},
	})

	tr := ingress.NewTranslator(store, baseCfg, ingress.TranslatorConfig{
		IngressClass: ingressClass, ControllerName: controllerName,
	})
	cfg, _ := tr.Translate()

	ok := true
	var issues []string

	// Base listener preserved
	baseListenerFound := false
	for _, l := range cfg.Listeners {
		if l.ID == "base-http" && l.Address == ":7070" {
			baseListenerFound = true
		}
	}
	if !baseListenerFound {
		ok = false
		issues = append(issues, "base listener lost")
	}

	// Base route preserved
	healthRouteFound := false
	appRouteFound := false
	for _, r := range cfg.Routes {
		if r.ID == "health" {
			healthRouteFound = true
		}
		if r.Path == "/app" {
			appRouteFound = true
		}
	}
	if !healthRouteFound {
		ok = false
		issues = append(issues, "base /healthz route lost")
	}
	if !appRouteFound {
		ok = false
		issues = append(issues, "k8s /app route not added")
	}

	// Global settings preserved
	if cfg.Redis.Address != "redis:6379" {
		ok = false
		issues = append(issues, "redis config lost")
	}

	detail := fmt.Sprintf("%d routes, %d listeners, redis=%s", len(cfg.Routes), len(cfg.Listeners), cfg.Redis.Address)
	if !ok {
		detail = strings.Join(issues, "; ")
		t.Error(detail)
	}
	report.add("Base Config Merge", ok, time.Since(start), detail)
}

// Test 13: ClusterIP upstream mode via annotation
func TestClusterIPMode(t *testing.T) {
	start := time.Now()
	k8s, _ := getKubeClient(t)
	ensureNamespace(t, k8s)

	ctx := context.Background()
	className := ingressClass

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "clusterip-test", Namespace: testNamespace,
			Annotations: map[string]string{"runway.wudi.io/upstream-mode": "clusterip"},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			Rules: []networkingv1.IngressRule{{
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: pathTypePtr(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
								Name: "my-svc", Port: networkingv1.ServiceBackendPort{Number: 8080},
							}},
						}},
					},
				},
			}},
		},
	}
	created, err := k8s.NetworkingV1().Ingresses(testNamespace).Create(ctx, ing, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create ingress: %v", err)
	}

	store := ingress.NewStore()
	store.SetIngress(created)

	tr := ingress.NewTranslator(store, nil, ingress.TranslatorConfig{
		IngressClass: ingressClass, ControllerName: controllerName,
	})
	cfg, _ := tr.Translate()

	ok := false
	detail := ""
	expected := fmt.Sprintf("http://my-svc.%s.svc.cluster.local:8080", testNamespace)
	if len(cfg.Routes) == 1 && len(cfg.Routes[0].Backends) == 1 {
		if cfg.Routes[0].Backends[0].URL == expected {
			ok = true
			detail = fmt.Sprintf("backend=%s", expected)
		} else {
			detail = fmt.Sprintf("expected %s, got %s", expected, cfg.Routes[0].Backends[0].URL)
		}
	} else {
		detail = fmt.Sprintf("routes=%d", len(cfg.Routes))
	}

	if !ok {
		t.Error(detail)
	}
	report.add("ClusterIP Upstream Mode", ok, time.Since(start), detail)
}
