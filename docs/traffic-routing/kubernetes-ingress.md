# Kubernetes Ingress Controller

The runway can run as a Kubernetes Ingress Controller, translating standard Kubernetes `Ingress` and Gateway API `HTTPRoute` resources into runway configuration. This enables declarative route management using native Kubernetes resources.

## Architecture

The ingress controller runs as an embedded binary that includes both the K8s controller and the runway data plane in a single process. It watches Kubernetes resources, builds a `config.Config`, and uses `Server.ReloadWithConfig()` for zero-downtime updates.

```
K8s API Server
      │ Watch/List
      ▼
  controller-runtime    ← watches Ingress, Gateway, HTTPRoute,
  manager + reconcilers    EndpointSlices, Secrets
         │
    Translator           ← K8s resources → config.Config
         │
  Server.ReloadWithConfig ← atomic config swap, zero-downtime
         │
    Gateway data plane   ← HTTP/HTTPS traffic
```

## Installation

### Helm

```bash
helm install runway ./deploy/kubernetes/helm/runway \
  --namespace runway-system \
  --create-namespace
```

### Quickstart

```bash
kubectl apply -f deploy/kubernetes/quickstart.yaml
```

## Ingress v1

### Basic Example

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-app
  annotations:
    runway.wudi.io/timeout: "30s"
    runway.wudi.io/retry-max: "3"
spec:
  ingressClassName: runway
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /api
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 8080
          - path: /
            pathType: Prefix
            backend:
              service:
                name: frontend
                port:
                  number: 80
  tls:
    - hosts:
        - app.example.com
      secretName: app-tls
```

### IngressClass

The controller creates an `IngressClass` named `runway` during Helm installation. Set `spec.ingressClassName: runway` on your Ingress resources to route them through this controller.

To claim Ingress resources without an explicit class:

```bash
helm install runway ./deploy/kubernetes/helm/runway \
  --set controller.watchIngressWithoutClass=true
```

## Gateway API

The controller supports the Gateway API Core conformance profile (GatewayClass, Gateway, HTTPRoute).

### GatewayClass

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: runway
spec:
  controllerName: runway.wudi.io/ingress-controller
```

### Gateway

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: main
  namespace: runway-system
spec:
  gatewayClassName: runway
  listeners:
    - name: http
      port: 8080
      protocol: HTTP
    - name: https
      port: 8443
      protocol: HTTPS
      tls:
        mode: Terminate
        certificateRefs:
          - name: wildcard-tls
```

### HTTPRoute

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: api-routes
spec:
  parentRefs:
    - name: main
      namespace: runway-system
  hostnames:
    - api.example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /v1
      backendRefs:
        - name: api-v1
          port: 8080
    - matches:
        - path:
            type: PathPrefix
            value: /v2
      backendRefs:
        - name: api-v2
          port: 8080
```

## Annotations Reference

All annotations use the `runway.wudi.io/` prefix.

| Annotation | Type | Default | Description |
|---|---|---|---|
| `runway.wudi.io/rate-limit` | int | - | Requests per period |
| `runway.wudi.io/timeout` | duration | - | Request timeout |
| `runway.wudi.io/retry-max` | int | - | Maximum retry attempts |
| `runway.wudi.io/cors-enabled` | bool | false | Enable CORS |
| `runway.wudi.io/circuit-breaker` | bool | false | Enable circuit breaker |
| `runway.wudi.io/auth-required` | bool | false | Require authentication |
| `runway.wudi.io/cache-enabled` | bool | false | Enable response caching |
| `runway.wudi.io/load-balancer` | string | round_robin | Load balancer algorithm |
| `runway.wudi.io/strip-prefix` | bool | false | Strip matched prefix |
| `runway.wudi.io/upstream-mode` | string | endpointslice | Backend resolution mode |

## Backend Resolution

### EndpointSlice Mode (Default)

By default, the controller resolves backends using EndpointSlice resources. Each ready endpoint address becomes a direct backend, bypassing kube-proxy. This enables the runway's load balancer features (least_conn, consistent_hash, etc.).

### ClusterIP Mode

For cases where pod-IP routing is undesirable, set the annotation:

```yaml
annotations:
  runway.wudi.io/upstream-mode: clusterip
```

This resolves to the Service ClusterIP instead of individual pod IPs.

### ExternalName Services

Services of type `ExternalName` are resolved to their external hostname.

## TLS

TLS certificates are loaded from Kubernetes Secrets (type `kubernetes.io/tls`). The controller extracts PEM data from the Secret and loads certificates in-memory without writing to disk.

For Ingress resources, reference a Secret in the `tls` section. For Gateway API, use `certificateRefs` in the Gateway listener.

Multiple certificates with different SNI hostnames are supported. The listener uses the `ClientHelloInfo.ServerName` to select the appropriate certificate.

## Base Config

For settings that cannot be expressed through Kubernetes resources (authentication providers, Redis configuration, tracing, etc.), use a base config file:

```bash
helm install runway ./deploy/kubernetes/helm/runway \
  --set-file controller.baseConfig=base-config.yaml
```

K8s-derived routes and listeners are merged on top of the base config. Base config routes with the same ID are overridden by K8s-derived routes.

## Namespace Scoping

Three modes via `--watch-namespaces`:

- **All namespaces** (default): watches all namespaces, requires ClusterRole
- **Specific namespaces**: `--watch-namespaces=ns1,ns2` watches only named namespaces
- **Single namespace**: `--watch-namespaces=my-ns` for namespace-scoped deployment

## High Availability

Deploy with multiple replicas:

```yaml
replicaCount: 3
podDisruptionBudget:
  enabled: true
  minAvailable: 1
```

All replicas watch K8s resources and serve traffic. Only the leader writes status updates to prevent conflicts. Leader election uses Kubernetes Leases.

## RBAC

The Helm chart creates a ClusterRole with the minimum permissions needed:

- **Core API**: Services, Namespaces (get/list/watch), Events (create/patch), Secrets (get/list/watch)
- **Discovery API**: EndpointSlices (get/list/watch)
- **Coordination API**: Leases (full access for leader election)
- **Networking API**: Ingresses, IngressClasses (get/list/watch), Ingresses/status (update/patch)
- **Gateway API**: GatewayClasses, Gateways, HTTPRoutes, ReferenceGrants (get/list/watch), status subresources (update/patch)

## Flags

| Flag | Default | Description |
|---|---|---|
| `--ingress-class` | runway | IngressClass name to watch |
| `--controller-name` | runway.wudi.io/ingress-controller | GatewayClass controller name |
| `--watch-namespaces` | (all) | Comma-separated namespaces |
| `--watch-ingress-without-class` | false | Claim unclassed Ingress resources |
| `--publish-service` | - | Service for status IP (ns/name) |
| `--publish-status-address` | - | Explicit IP/hostname for status |
| `--http-port` | 8080 | HTTP listener port |
| `--https-port` | 8443 | HTTPS listener port |
| `--admin-port` | 8081 | Admin API port |
| `--metrics-port` | 9090 | Prometheus metrics port |
| `--base-config` | - | Path to base YAML config |
| `--debounce-delay` | 100ms | Config rebuild debounce delay |
| `--enable-gateway-api` | true | Enable Gateway API support |
| `--enable-ingress` | true | Enable Ingress v1 support |
