package kubernetes

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/registry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDiscover_BasicEndpoints(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
					{IP: "10.0.0.2"},
				},
				Ports: []corev1.EndpointPort{
					{Port: 8080},
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	services, err := r.Discover(context.Background(), "my-service")
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Address < services[j].Address
	})

	if services[0].Address != "10.0.0.1" {
		t.Errorf("expected address 10.0.0.1, got %s", services[0].Address)
	}
	if services[0].Port != 8080 {
		t.Errorf("expected port 8080, got %d", services[0].Port)
	}
	if services[0].Health != registry.HealthPassing {
		t.Errorf("expected health passing, got %s", services[0].Health)
	}
	if services[0].Name != "my-service" {
		t.Errorf("expected name my-service, got %s", services[0].Name)
	}
	if services[0].ID != "my-service-10.0.0.1" {
		t.Errorf("expected ID my-service-10.0.0.1, got %s", services[0].ID)
	}
	if services[1].Address != "10.0.0.2" {
		t.Errorf("expected address 10.0.0.2, got %s", services[1].Address)
	}
}

func TestDiscover_DefaultPort(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	services, err := r.Discover(context.Background(), "my-service")
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Port != 80 {
		t.Errorf("expected default port 80, got %d", services[0].Port)
	}
}

func TestDiscover_NotReadyAddresses(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
				NotReadyAddresses: []corev1.EndpointAddress{
					{IP: "10.0.0.2"},
				},
				Ports: []corev1.EndpointPort{
					{Port: 9090},
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	services, err := r.Discover(context.Background(), "my-service")
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Address < services[j].Address
	})

	if services[0].Health != registry.HealthPassing {
		t.Errorf("expected ready address to be passing, got %s", services[0].Health)
	}
	if services[1].Health != registry.HealthCritical {
		t.Errorf("expected not-ready address to be critical, got %s", services[1].Health)
	}
}

func TestDiscover_NotFound(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	_, err := r.Discover(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestDiscover_Cache(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	// Pre-populate cache
	cached := []*registry.Service{
		{ID: "cached-1", Name: "my-service", Address: "1.2.3.4", Port: 80, Health: registry.HealthPassing},
	}
	r.cacheMu.Lock()
	r.cache["my-service"] = cached
	r.cacheMu.Unlock()

	services, err := r.Discover(context.Background(), "my-service")
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 cached service, got %d", len(services))
	}
	if services[0].ID != "cached-1" {
		t.Errorf("expected cached service ID, got %s", services[0].ID)
	}
}

func TestDiscover_PodLabelsAsTags(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pod",
			Namespace: "default",
			Labels: map[string]string{
				"app":     "web",
				"version": "v1",
			},
		},
	}

	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: "10.0.0.1",
						TargetRef: &corev1.ObjectReference{
							Kind: "Pod",
							Name: "my-pod",
						},
					},
				},
				Ports: []corev1.EndpointPort{
					{Port: 8080},
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints, pod)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	services, err := r.Discover(context.Background(), "my-service")
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}

	svc := services[0]
	if len(svc.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d: %v", len(svc.Tags), svc.Tags)
	}
	sort.Strings(svc.Tags)
	if svc.Tags[0] != "app=web" || svc.Tags[1] != "version=v1" {
		t.Errorf("unexpected tags: %v", svc.Tags)
	}
	if svc.Metadata["app"] != "web" {
		t.Errorf("expected metadata app=web, got %v", svc.Metadata)
	}
}

func TestDiscoverWithTags(t *testing.T) {
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels:    map[string]string{"app": "web", "env": "prod"},
		},
	}
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels:    map[string]string{"app": "web", "env": "staging"},
		},
	}

	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1", TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "pod-1"}},
					{IP: "10.0.0.2", TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "pod-2"}},
				},
				Ports: []corev1.EndpointPort{{Port: 8080}},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints, pod1, pod2)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	services, err := r.DiscoverWithTags(context.Background(), "my-service", []string{"env=prod"})
	if err != nil {
		t.Fatalf("DiscoverWithTags failed: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service matching tag, got %d", len(services))
	}
	if services[0].Address != "10.0.0.1" {
		t.Errorf("expected address 10.0.0.1, got %s", services[0].Address)
	}
}

func TestDiscoverWithTags_NoTags(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8080}},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	// Empty tags should return all
	services, err := r.DiscoverWithTags(context.Background(), "my-service", nil)
	if err != nil {
		t.Fatalf("DiscoverWithTags failed: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
}

func TestRegisterAndDeregister_NoOp(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	err := r.Register(context.Background(), &registry.Service{ID: "test"})
	if err != nil {
		t.Errorf("Register should be no-op, got error: %v", err)
	}

	err = r.Deregister(context.Background(), "test")
	if err != nil {
		t.Errorf("Deregister should be no-op, got error: %v", err)
	}
}

func TestWatch_InitialState(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8080}},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := r.Watch(ctx, "my-service")
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	// Should receive initial state
	select {
	case services := <-ch:
		if len(services) != 1 {
			t.Fatalf("expected 1 service in initial state, got %d", len(services))
		}
		if services[0].Address != "10.0.0.1" {
			t.Errorf("expected address 10.0.0.1, got %s", services[0].Address)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial state")
	}
}

func TestWatch_ReceivesUpdates(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8080}},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := r.Watch(ctx, "my-service")
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	// Drain initial state
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial state")
	}

	// Update the endpoints
	updated := endpoints.DeepCopy()
	updated.Subsets[0].Addresses = append(updated.Subsets[0].Addresses,
		corev1.EndpointAddress{IP: "10.0.0.2"},
	)
	_, err = fakeClient.CoreV1().Endpoints("default").Update(ctx, updated, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update endpoints: %v", err)
	}

	// Should receive update notification
	select {
	case services := <-ch:
		if len(services) != 2 {
			t.Fatalf("expected 2 services after update, got %d", len(services))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch update")
	}
}

func TestWatch_ContextCancellation(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8080}},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := r.Watch(ctx, "my-service")
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	// Drain initial state
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial state")
	}

	// Cancel and verify channel closes
	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			// May receive a final event; drain once more
			select {
			case _, ok := <-ch:
				if ok {
					t.Error("expected channel to close after context cancellation")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for channel close")
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel close after cancel")
	}
}

func TestWatch_ReplacesExistingWatcher(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8080}},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	ctx := context.Background()

	ch1, err := r.Watch(ctx, "my-service")
	if err != nil {
		t.Fatalf("Watch 1 failed: %v", err)
	}

	// Drain initial state from first watcher
	select {
	case <-ch1:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial state on ch1")
	}

	// Second watch should cancel the first
	ch2, err := r.Watch(ctx, "my-service")
	if err != nil {
		t.Fatalf("Watch 2 failed: %v", err)
	}

	// First channel should eventually close
	select {
	case _, ok := <-ch1:
		if ok {
			// Drain remaining
			for range ch1 {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first watcher channel did not close after replacement")
	}

	// Second channel should receive initial state
	select {
	case services := <-ch2:
		if len(services) != 1 {
			t.Fatalf("expected 1 service from second watcher, got %d", len(services))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial state on ch2")
	}
}

func TestClose(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8080}},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	ch, err := r.Watch(context.Background(), "my-service")
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	// Drain initial state
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial state")
	}

	err = r.Close()
	if err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	// Channel should close after Close()
	select {
	case _, ok := <-ch:
		if ok {
			for range ch {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after Close()")
	}

	// Watchers map should be empty
	r.watchMu.Lock()
	defer r.watchMu.Unlock()
	if len(r.watchers) != 0 {
		t.Errorf("expected empty watchers map, got %d entries", len(r.watchers))
	}
}

func TestDiscoverBySelector(t *testing.T) {
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-1",
			Namespace: "default",
			UID:       types.UID("uid-1"),
			Labels:    map[string]string{"app": "web"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Ports: []corev1.ContainerPort{{ContainerPort: 3000}}},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.1",
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-2",
			Namespace: "default",
			UID:       types.UID("uid-2"),
			Labels:    map[string]string{"app": "web"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Ports: []corev1.ContainerPort{{ContainerPort: 3000}}},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.2",
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			},
		},
	}

	// Non-running pod should be excluded
	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-3",
			Namespace: "default",
			UID:       types.UID("uid-3"),
			Labels:    map[string]string{"app": "web"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			PodIP: "10.0.0.3",
		},
	}

	fakeClient := fake.NewSimpleClientset(pod1, pod2, pod3)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	services, err := r.DiscoverBySelector(context.Background(), "app=web")
	if err != nil {
		t.Fatalf("DiscoverBySelector failed: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 running pods, got %d", len(services))
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Address < services[j].Address
	})

	if services[0].Port != 3000 {
		t.Errorf("expected port 3000, got %d", services[0].Port)
	}
	if services[0].Health != registry.HealthPassing {
		t.Errorf("expected ready pod to be passing, got %s", services[0].Health)
	}
	if services[1].Health != registry.HealthCritical {
		t.Errorf("expected not-ready pod to be critical, got %s", services[1].Health)
	}
}

func TestDiscoverBySelector_DefaultPort(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-1",
			Namespace: "default",
			UID:       types.UID("uid-1"),
			Labels:    map[string]string{"app": "web"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.1",
		},
	}

	fakeClient := fake.NewSimpleClientset(pod)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	services, err := r.DiscoverBySelector(context.Background(), "app=web")
	if err != nil {
		t.Fatalf("DiscoverBySelector failed: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Port != 80 {
		t.Errorf("expected default port 80, got %d", services[0].Port)
	}
}

func TestGetServicePort(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80},
				{Name: "grpc", Port: 9090},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(svc)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	// By name
	port, err := r.GetServicePort(context.Background(), "my-service", "grpc")
	if err != nil {
		t.Fatalf("GetServicePort failed: %v", err)
	}
	if port != 9090 {
		t.Errorf("expected port 9090, got %d", port)
	}

	// Empty name returns first port
	port, err = r.GetServicePort(context.Background(), "my-service", "")
	if err != nil {
		t.Fatalf("GetServicePort with empty name failed: %v", err)
	}
	if port != 80 {
		t.Errorf("expected port 80, got %d", port)
	}
}

func TestGetServicePort_Annotation(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
			Annotations: map[string]string{
				"gateway.port": "7777",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{},
		},
	}

	fakeClient := fake.NewSimpleClientset(svc)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	port, err := r.GetServicePort(context.Background(), "my-service", "")
	if err != nil {
		t.Fatalf("GetServicePort with annotation failed: %v", err)
	}
	if port != 7777 {
		t.Errorf("expected port 7777 from annotation, got %d", port)
	}
}

func TestGetServicePort_NotFound(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(svc)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	_, err := r.GetServicePort(context.Background(), "my-service", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent port name")
	}
}

func TestLabelsToTags(t *testing.T) {
	labels := map[string]string{
		"app":     "web",
		"version": "v2",
	}
	tags := labelsToTags(labels)
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tags))
	}
	sort.Strings(tags)
	if tags[0] != "app=web" || tags[1] != "version=v2" {
		t.Errorf("unexpected tags: %v", tags)
	}
}

func TestLabelsToTags_Empty(t *testing.T) {
	tags := labelsToTags(nil)
	if len(tags) != 0 {
		t.Errorf("expected 0 tags for nil labels, got %d", len(tags))
	}
}

func TestHasAllTags(t *testing.T) {
	tests := []struct {
		name     string
		have     []string
		required []string
		want     bool
	}{
		{"empty required", []string{"a", "b"}, nil, true},
		{"all present", []string{"a=1", "b=2", "c=3"}, []string{"a=1", "c=3"}, true},
		{"missing one", []string{"a=1", "b=2"}, []string{"a=1", "c=3"}, false},
		{"empty have", nil, []string{"a=1"}, false},
		{"both empty", nil, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasAllTags(tt.have, tt.required)
			if got != tt.want {
				t.Errorf("hasAllTags(%v, %v) = %v, want %v", tt.have, tt.required, got, tt.want)
			}
		})
	}
}

func TestMultipleSubsets(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8080}},
			},
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.2"}},
				Ports:     []corev1.EndpointPort{{Port: 9090}},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "default",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	services, err := r.Discover(context.Background(), "my-service")
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services from 2 subsets, got %d", len(services))
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Address < services[j].Address
	})

	if services[0].Port != 8080 {
		t.Errorf("expected port 8080 for first subset, got %d", services[0].Port)
	}
	if services[1].Port != 9090 {
		t.Errorf("expected port 9090 for second subset, got %d", services[1].Port)
	}
}

func TestCustomNamespace(t *testing.T) {
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: "production",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8080}},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(endpoints)
	r := &Registry{
		client:    fakeClient,
		namespace: "production",
		watchers:  make(map[string]context.CancelFunc),
		cache:     make(map[string][]*registry.Service),
	}

	services, err := r.Discover(context.Background(), "my-service")
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
}
