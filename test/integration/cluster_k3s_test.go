//go:build integration

package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const clusterTestNamespace = "gw-cluster-k3s-test"

// clusterCerts holds PEM-encoded certificates generated for cluster mTLS.
type clusterCerts struct {
	CACert  []byte
	CPCert  []byte
	CPKey   []byte
	DPCert  []byte
	DPKey   []byte
}

func TestClusterK3s(t *testing.T) {
	// --- Setup ---
	k8s := getClusterKubeClient(t)

	t.Log("Building runway image...")
	runCmd(t, "sudo", "docker", "build", "-t", "runway-test:latest", ".")
	t.Log("Importing image into k3s containerd...")
	importImage(t)

	ensureClusterNamespace(t, k8s)
	t.Cleanup(func() { cleanupClusterNamespace(t, k8s) })

	certs := generateClusterK3sCerts(t)

	// Create K8s resources
	createOpaqueSecret(t, k8s, "cp-tls", map[string][]byte{
		"tls.crt": certs.CPCert,
		"tls.key": certs.CPKey,
		"ca.crt":  certs.CACert,
	})
	createOpaqueSecret(t, k8s, "dp-tls", map[string][]byte{
		"tls.crt": certs.DPCert,
		"tls.key": certs.DPKey,
		"ca.crt":  certs.CACert,
	})

	cpConfig := fmt.Sprintf(`listeners:
  - id: default-http
    address: ":8080"
    protocol: http
admin:
  enabled: true
  port: 8081
routes:
  - id: echo
    path: /echo
    backends:
      - url: http://echo-backend.%s.svc.cluster.local:8080
cluster:
  role: control_plane
  control_plane:
    address: ":9443"
    tls:
      enabled: true
      cert_file: /etc/cluster-tls/tls.crt
      key_file: /etc/cluster-tls/tls.key
      client_ca_file: /etc/cluster-tls/ca.crt
`, clusterTestNamespace)

	dpConfig := fmt.Sprintf(`listeners:
  - id: default-http
    address: ":8080"
    protocol: http
admin:
  enabled: true
  port: 8081
cluster:
  role: data_plane
  data_plane:
    address: "runway-cp.%s.svc.cluster.local:9443"
    cache_dir: /tmp/dp-cache
    retry_interval: 1s
    heartbeat_interval: 2s
    tls:
      enabled: true
      cert_file: /etc/cluster-tls/tls.crt
      key_file: /etc/cluster-tls/tls.key
      ca_file: /etc/cluster-tls/ca.crt
`, clusterTestNamespace)

	createConfigMap(t, k8s, "cp-config", map[string]string{"runway.yaml": cpConfig})
	createConfigMap(t, k8s, "dp-config", map[string]string{"runway.yaml": dpConfig})

	deployClusterEchoBackend(t, k8s)

	createRunwayPod(t, k8s, "runway-cp", "cp-config", "cp-tls", []corev1.ContainerPort{
		{Name: "http", ContainerPort: 8080},
		{Name: "admin", ContainerPort: 8081},
		{Name: "grpc", ContainerPort: 9443},
	})
	createClusterIPService(t, k8s, "runway-cp", []corev1.ServicePort{
		{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
		{Name: "admin", Port: 8081, TargetPort: intstr.FromInt32(8081)},
		{Name: "grpc", Port: 9443, TargetPort: intstr.FromInt32(9443)},
	})

	createRunwayPod(t, k8s, "runway-dp", "dp-config", "dp-tls", []corev1.ContainerPort{
		{Name: "http", ContainerPort: 8080},
		{Name: "admin", ContainerPort: 8081},
	})
	createClusterIPService(t, k8s, "runway-dp", []corev1.ServicePort{
		{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
		{Name: "admin", Port: 8081, TargetPort: intstr.FromInt32(8081)},
	})

	// Wait for pods to be ready
	t.Log("Waiting for echo-backend pod...")
	waitForPodRunning(t, k8s, "echo-backend", 60*time.Second)

	t.Log("Waiting for runway-cp pod...")
	waitForPodReady(t, k8s, "runway-cp", 60*time.Second)

	t.Log("Waiting for runway-dp pod...")
	waitForPodReady(t, k8s, "runway-dp", 60*time.Second)

	// Set up port-forwards
	cpAdminPort, cpAdminCleanup := portForward(t, "runway-cp", 8081)
	defer cpAdminCleanup()

	dpAdminPort, dpAdminCleanup := portForward(t, "runway-dp", 8081)
	defer dpAdminCleanup()

	dpHTTPPort, dpHTTPCleanup := portForward(t, "runway-dp", 8080)
	defer dpHTTPCleanup()

	cpAdminBase := fmt.Sprintf("http://127.0.0.1:%d", cpAdminPort)
	dpAdminBase := fmt.Sprintf("http://127.0.0.1:%d", dpAdminPort)
	dpHTTPBase := fmt.Sprintf("http://127.0.0.1:%d", dpHTTPPort)

	// --- Test Cases ---

	t.Run("CP_Ready", func(t *testing.T) {
		var result map[string]interface{}
		httpGetJSON(t, cpAdminBase+"/api/v1/config/hash", &result)
		version, ok := result["version"].(float64)
		if !ok {
			t.Fatalf("expected version field, got %v", result)
		}
		if version < 1 {
			t.Errorf("expected version >= 1, got %v", version)
		}
		t.Logf("CP config version: %v", version)
	})

	t.Run("DP_Connects", func(t *testing.T) {
		var status map[string]interface{}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			httpGetJSON(t, dpAdminBase+"/cluster/status", &status)
			if connected, _ := status["connected"].(bool); connected {
				if hasConfig, _ := status["has_config"].(bool); hasConfig {
					t.Logf("DP status: connected=%v has_config=%v", connected, hasConfig)
					return
				}
			}
			time.Sleep(time.Second)
		}
		t.Fatalf("DP did not connect to CP within 30s, last status: %v", status)
	})

	t.Run("CP_Sees_DP", func(t *testing.T) {
		var nodes []map[string]interface{}
		httpGetJSON(t, cpAdminBase+"/cluster/nodes", &nodes)
		if len(nodes) == 0 {
			t.Fatal("CP sees 0 connected nodes, expected 1")
		}
		nodeStatus, _ := nodes[0]["status"].(string)
		if nodeStatus != "connected" {
			t.Errorf("expected node status 'connected', got %q", nodeStatus)
		}
		t.Logf("CP sees %d node(s), first: %v", len(nodes), nodes[0])
	})

	t.Run("Traffic_Through_DP", func(t *testing.T) {
		resp, err := http.Get(dpHTTPBase + "/echo")
		if err != nil {
			t.Fatalf("GET /echo through DP: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d, body: %s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "echo") {
			t.Errorf("expected echo response, got: %s", body)
		}
		t.Logf("DP /echo response: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	})

	t.Run("Config_Push", func(t *testing.T) {
		// Get current DP config version
		var statusBefore map[string]interface{}
		httpGetJSON(t, dpAdminBase+"/cluster/status", &statusBefore)
		versionBefore, _ := statusBefore["config_version"].(float64)

		// Push updated config with an additional route.
		// Include listeners+admin+cluster for the CP to reload successfully.
		newConfig := fmt.Sprintf(`listeners:
  - id: default-http
    address: ":8080"
    protocol: http
admin:
  enabled: true
  port: 8081
routes:
  - id: echo
    path: /echo
    backends:
      - url: http://echo-backend.%s.svc.cluster.local:8080
  - id: echo2
    path: /echo2
    backends:
      - url: http://echo-backend.%s.svc.cluster.local:8080
cluster:
  role: control_plane
  control_plane:
    address: ":9443"
    tls:
      enabled: true
      cert_file: /etc/cluster-tls/tls.crt
      key_file: /etc/cluster-tls/tls.key
      client_ca_file: /etc/cluster-tls/ca.crt
`, clusterTestNamespace, clusterTestNamespace)

		httpPostYAML(t, cpAdminBase+"/api/v1/config", newConfig)

		// Poll DP until config_version increments
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			var statusAfter map[string]interface{}
			httpGetJSON(t, dpAdminBase+"/cluster/status", &statusAfter)
			versionAfter, _ := statusAfter["config_version"].(float64)
			if versionAfter > versionBefore {
				t.Logf("DP config version incremented: %v -> %v", versionBefore, versionAfter)

				// Verify the new route works
				resp, err := http.Get(dpHTTPBase + "/echo2")
				if err != nil {
					t.Fatalf("GET /echo2 through DP: %v", err)
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected 200 for /echo2, got %d, body: %s", resp.StatusCode, body)
				}
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatal("DP config version did not increment within 15s after config push")
	})

	t.Run("Static_Stability", func(t *testing.T) {
		// Delete the CP pod
		t.Log("Deleting CP pod to test static stability...")
		ctx := context.Background()
		err := k8s.CoreV1().Pods(clusterTestNamespace).Delete(ctx, "runway-cp", metav1.DeleteOptions{})
		if err != nil {
			t.Fatalf("Failed to delete CP pod: %v", err)
		}

		// Wait for pod to actually disappear
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			_, err := k8s.CoreV1().Pods(clusterTestNamespace).Get(ctx, "runway-cp", metav1.GetOptions{})
			if err != nil {
				break
			}
			time.Sleep(time.Second)
		}

		// Give DP time to detect disconnection
		time.Sleep(3 * time.Second)

		// DP should still serve traffic from cached config.
		// Retry a few times in case the health checker briefly marks backends down.
		var lastStatus int
		var lastBody string
		for i := 0; i < 10; i++ {
			resp, err := http.Get(dpHTTPBase + "/echo")
			if err != nil {
				t.Logf("GET /echo attempt %d: %v", i+1, err)
				time.Sleep(time.Second)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastStatus = resp.StatusCode
			lastBody = string(body)
			if resp.StatusCode == http.StatusOK {
				break
			}
			time.Sleep(time.Second)
		}
		if lastStatus != http.StatusOK {
			t.Errorf("expected 200 for /echo (static stability), got %d, body: %s", lastStatus, lastBody)
		}

		// DP status should show disconnected but with config
		var status map[string]interface{}
		httpGetJSON(t, dpAdminBase+"/cluster/status", &status)
		if connected, _ := status["connected"].(bool); connected {
			t.Errorf("expected DP connected=false after CP deletion, got true")
		}
		if hasConfig, _ := status["has_config"].(bool); !hasConfig {
			t.Errorf("expected DP has_config=true (static stability), got false")
		}
		t.Logf("Static stability confirmed: connected=%v has_config=%v", status["connected"], status["has_config"])
	})

	t.Run("DP_Reconnects", func(t *testing.T) {
		// Recreate CP pod
		t.Log("Recreating CP pod...")
		createRunwayPod(t, k8s, "runway-cp", "cp-config", "cp-tls", []corev1.ContainerPort{
			{Name: "http", ContainerPort: 8080},
			{Name: "admin", ContainerPort: 8081},
			{Name: "grpc", ContainerPort: 9443},
		})

		t.Log("Waiting for new CP pod to be ready...")
		waitForPodReady(t, k8s, "runway-cp", 60*time.Second)

		// Poll DP until it reconnects (30s timeout, DP has 1s retry interval)
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			var status map[string]interface{}
			httpGetJSON(t, dpAdminBase+"/cluster/status", &status)
			if connected, _ := status["connected"].(bool); connected {
				t.Logf("DP reconnected to new CP: %v", status)
				return
			}
			time.Sleep(time.Second)
		}
		t.Fatal("DP did not reconnect to new CP within 30s")
	})
}

// --- Kubernetes Helpers ---

func getClusterKubeClient(t *testing.T) *kubernetes.Clientset {
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
	return k8s
}

func ensureClusterNamespace(t *testing.T, k8s *kubernetes.Clientset) {
	t.Helper()
	ctx := context.Background()

	for i := 0; i < 60; i++ {
		existing, err := k8s.CoreV1().Namespaces().Get(ctx, clusterTestNamespace, metav1.GetOptions{})
		if err != nil {
			break
		}
		if existing.Status.Phase == corev1.NamespaceTerminating {
			time.Sleep(time.Second)
			continue
		}
		// Namespace exists and is active — delete it so we start fresh
		_ = k8s.CoreV1().Namespaces().Delete(ctx, clusterTestNamespace, metav1.DeleteOptions{})
		time.Sleep(time.Second)
		continue
	}

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterTestNamespace}}
	_, err := k8s.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Failed to create namespace: %v", err)
	}
}

func cleanupClusterNamespace(t *testing.T, k8s *kubernetes.Clientset) {
	t.Helper()
	ctx := context.Background()
	_ = k8s.CoreV1().Namespaces().Delete(ctx, clusterTestNamespace, metav1.DeleteOptions{})
}

func runCmd(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = findRepoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Command %s %v failed: %v\n%s", name, args, err, out)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
		parent := dir[:strings.LastIndex(dir, "/")]
		if parent == dir {
			t.Fatal("could not find repo root (go.mod)")
		}
		dir = parent
	}
}

func importImage(t *testing.T) {
	t.Helper()
	cmd := exec.Command("bash", "-c", "sudo docker save runway-test:latest | sudo k3s ctr images import -")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to import image into k3s: %v\n%s", err, out)
	}
}

func createOpaqueSecret(t *testing.T, k8s *kubernetes.Clientset, name string, data map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: clusterTestNamespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
	_, err := k8s.CoreV1().Secrets(clusterTestNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create secret %s: %v", name, err)
	}
}

func createConfigMap(t *testing.T, k8s *kubernetes.Clientset, name string, data map[string]string) {
	t.Helper()
	ctx := context.Background()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: clusterTestNamespace,
		},
		Data: data,
	}
	_, err := k8s.CoreV1().ConfigMaps(clusterTestNamespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create configmap %s: %v", name, err)
	}
}

func deployClusterEchoBackend(t *testing.T, k8s *kubernetes.Clientset) {
	t.Helper()
	ctx := context.Background()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "echo-backend",
			Namespace: clusterTestNamespace,
			Labels:    map[string]string{"app": "echo-backend"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "echo",
					Image: "busybox:1.36",
					Command: []string{"/bin/sh", "-c",
						// Create index.html then run busybox httpd in foreground
						`mkdir -p /www && echo 'echo:echo-backend' > /www/index.html && echo 'echo:echo-backend' > /www/echo && echo 'echo:echo-backend' > /www/echo2 && echo 'echo:echo-backend' > /www/health && httpd -f -p 8080 -h /www`,
					},
					Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
				},
			},
		},
	}
	_, err := k8s.CoreV1().Pods(clusterTestNamespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create echo-backend pod: %v", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "echo-backend",
			Namespace: clusterTestNamespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "echo-backend"},
			Ports:    []corev1.ServicePort{{Port: 8080, TargetPort: intstr.FromInt32(8080)}},
		},
	}
	_, err = k8s.CoreV1().Services(clusterTestNamespace).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create echo-backend service: %v", err)
	}
}

func createRunwayPod(t *testing.T, k8s *kubernetes.Clientset, name, configMapName, secretName string, ports []corev1.ContainerPort) {
	t.Helper()
	ctx := context.Background()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: clusterTestNamespace,
			Labels:    map[string]string{"app": name},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:            "runway",
					Image:           "runway-test:latest",
					ImagePullPolicy: corev1.PullNever,
					Ports:           ports,
					VolumeMounts: []corev1.VolumeMount{
						{Name: "config", MountPath: "/app/configs", ReadOnly: true},
						{Name: "tls", MountPath: "/etc/cluster-tls", ReadOnly: true},
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/ready",
								Port: intstr.FromInt32(8081),
							},
						},
						InitialDelaySeconds: 2,
						PeriodSeconds:       2,
						TimeoutSeconds:      2,
						FailureThreshold:    15,
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
						},
					},
				},
				{
					Name: "tls",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: secretName,
						},
					},
				},
			},
		},
	}

	_, err := k8s.CoreV1().Pods(clusterTestNamespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Failed to create pod %s: %v", name, err)
	}
}

func createClusterIPService(t *testing.T, k8s *kubernetes.Clientset, name string, ports []corev1.ServicePort) {
	t.Helper()
	ctx := context.Background()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: clusterTestNamespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports:    ports,
		},
	}
	_, err := k8s.CoreV1().Services(clusterTestNamespace).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Failed to create service %s: %v", name, err)
	}
}

func waitForPodRunning(t *testing.T, k8s *kubernetes.Clientset, name string, timeout time.Duration) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pod, err := k8s.CoreV1().Pods(clusterTestNamespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && pod.Status.Phase == corev1.PodRunning {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("Pod %s did not reach Running state within %v", name, timeout)
}

func waitForPodReady(t *testing.T, k8s *kubernetes.Clientset, name string, timeout time.Duration) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pod, err := k8s.CoreV1().Pods(clusterTestNamespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					return
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	// Print pod logs for debugging
	dumpPodLogs(t, k8s, name)
	t.Fatalf("Pod %s did not become ready within %v", name, timeout)
}

func dumpPodLogs(t *testing.T, k8s *kubernetes.Clientset, name string) {
	t.Helper()
	ctx := context.Background()
	req := k8s.CoreV1().Pods(clusterTestNamespace).GetLogs(name, &corev1.PodLogOptions{TailLines: int64Ptr(50)})
	stream, err := req.Stream(ctx)
	if err != nil {
		t.Logf("Failed to get logs for %s: %v", name, err)
		return
	}
	defer stream.Close()
	logs, _ := io.ReadAll(stream)
	t.Logf("Last 50 lines of %s logs:\n%s", name, string(logs))
}

func int64Ptr(v int64) *int64 { return &v }

// portForward starts a kubectl port-forward to a pod and returns the local port.
func portForward(t *testing.T, podName string, remotePort int) (localPort int, cleanup func()) {
	t.Helper()

	// Pick a random available port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}
	localPort = ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cmd := exec.Command("kubectl", "port-forward",
		"-n", clusterTestNamespace,
		fmt.Sprintf("pod/%s", podName),
		fmt.Sprintf("%d:%d", localPort, remotePort),
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start port-forward to %s:%d: %v", podName, remotePort, err)
	}

	cleanup = func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}

	// Wait for port to accept connections
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return localPort, cleanup
		}
		time.Sleep(100 * time.Millisecond)
	}
	cleanup()
	t.Fatalf("Port-forward to %s:%d did not become reachable within 15s", podName, remotePort)
	return 0, nil
}

// --- HTTP Helpers ---

func httpGetJSON(t *testing.T, url string, target interface{}) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, body: %s", url, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("GET %s: JSON decode: %v, body: %s", url, err, body)
	}
}

func httpPostYAML(t *testing.T, url, body string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/x-yaml", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		t.Fatalf("POST %s: status %d, body: %s", url, resp.StatusCode, respBody)
	}
}

// --- TLS Certificate Generation ---

func generateClusterK3sCerts(t *testing.T) clusterCerts {
	t.Helper()

	// Generate CA
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Cluster K3s Test CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(1 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	// CP leaf cert with DNS SANs
	cpCertPEM, cpKeyPEM := generateLeafCertWithSANs(t, caCert, caKey, "cp", []string{
		"runway-cp",
		"runway-cp." + clusterTestNamespace + ".svc.cluster.local",
		"localhost",
	})

	// DP leaf cert with DNS SANs
	dpCertPEM, dpKeyPEM := generateLeafCertWithSANs(t, caCert, caKey, "dp", []string{
		"runway-dp",
		"runway-dp." + clusterTestNamespace + ".svc.cluster.local",
		"localhost",
	})

	return clusterCerts{
		CACert: caPEM,
		CPCert: cpCertPEM,
		CPKey:  cpKeyPEM,
		DPCert: dpCertPEM,
		DPKey:  dpKeyPEM,
	}
}

func generateLeafCertWithSANs(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, dnsNames []string) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return
}
