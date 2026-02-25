package ingress

import (
	"context"
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// StatusUpdater writes K8s resource status updates. Only the leader should call these.
type StatusUpdater struct {
	client         client.Client
	publishAddress string // IP or hostname to set in Ingress status.loadBalancer
}

// NewStatusUpdater creates a new StatusUpdater.
func NewStatusUpdater(c client.Client, publishAddress string) *StatusUpdater {
	return &StatusUpdater{client: c, publishAddress: publishAddress}
}

// SetPublishAddress updates the address used for Ingress status.
func (u *StatusUpdater) SetPublishAddress(addr string) {
	u.publishAddress = addr
}

// UpdateIngressStatus sets the LoadBalancer ingress status on an Ingress resource.
func (u *StatusUpdater) UpdateIngressStatus(ctx context.Context, ing *networkingv1.Ingress) error {
	if u.publishAddress == "" {
		return nil
	}

	desired := networkingv1.IngressLoadBalancerIngress{}
	// Determine if the address is an IP or hostname.
	if isIP(u.publishAddress) {
		desired.IP = u.publishAddress
	} else {
		desired.Hostname = u.publishAddress
	}

	// Check if already correct.
	if len(ing.Status.LoadBalancer.Ingress) == 1 {
		existing := ing.Status.LoadBalancer.Ingress[0]
		if existing.IP == desired.IP && existing.Hostname == desired.Hostname {
			return nil
		}
	}

	patch := client.MergeFrom(ing.DeepCopy())
	ing.Status.LoadBalancer.Ingress = []networkingv1.IngressLoadBalancerIngress{desired}
	return u.client.Status().Patch(ctx, ing, patch)
}

// UpdateGatewayClassStatus sets the Accepted condition on a GatewayClass.
func (u *StatusUpdater) UpdateGatewayClassStatus(ctx context.Context, gc *gatewayv1.GatewayClass, accepted bool, reason, msg string) error {
	status := metav1.ConditionTrue
	if !accepted {
		status = metav1.ConditionFalse
	}
	condition := metav1.Condition{
		Type:               string(gatewayv1.GatewayClassConditionStatusAccepted),
		Status:             status,
		ObservedGeneration: gc.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            msg,
	}

	patch := client.MergeFrom(gc.DeepCopy())
	setCondition(&gc.Status.Conditions, condition)
	return u.client.Status().Patch(ctx, gc, patch)
}

// UpdateGatewayStatus sets conditions on a Gateway resource.
func (u *StatusUpdater) UpdateGatewayStatus(ctx context.Context, gw *gatewayv1.Gateway, accepted bool, reason, msg string) error {
	status := metav1.ConditionTrue
	if !accepted {
		status = metav1.ConditionFalse
	}
	condition := metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionAccepted),
		Status:             status,
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            msg,
	}

	patch := client.MergeFrom(gw.DeepCopy())
	setCondition(&gw.Status.Conditions, condition)
	return u.client.Status().Patch(ctx, gw, patch)
}

// UpdateHTTPRouteStatus sets the Accepted condition on an HTTPRoute for a given parent.
func (u *StatusUpdater) UpdateHTTPRouteStatus(ctx context.Context, hr *gatewayv1.HTTPRoute, parentRef gatewayv1.ParentReference, accepted bool, reason, msg string) error {
	condStatus := metav1.ConditionTrue
	if !accepted {
		condStatus = metav1.ConditionFalse
	}
	condition := metav1.Condition{
		Type:               string(gatewayv1.RouteConditionAccepted),
		Status:             condStatus,
		ObservedGeneration: hr.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            msg,
	}

	patch := client.MergeFrom(hr.DeepCopy())

	controllerName := gatewayv1.GatewayController("apigw.dev/ingress-controller")

	// Find or create RouteParentStatus for this parent
	found := false
	for i := range hr.Status.Parents {
		if isSameParentRef(hr.Status.Parents[i].ParentRef, parentRef) {
			hr.Status.Parents[i].ControllerName = controllerName
			setCondition(&hr.Status.Parents[i].Conditions, condition)
			found = true
			break
		}
	}
	if !found {
		hr.Status.Parents = append(hr.Status.Parents, gatewayv1.RouteParentStatus{
			ParentRef:      parentRef,
			ControllerName: controllerName,
			Conditions:     []metav1.Condition{condition},
		})
	}

	return u.client.Status().Patch(ctx, hr, patch)
}

// setCondition adds or updates a condition in the list.
func setCondition(conditions *[]metav1.Condition, c metav1.Condition) {
	for i, existing := range *conditions {
		if existing.Type == c.Type {
			(*conditions)[i] = c
			return
		}
	}
	*conditions = append(*conditions, c)
}

func isSameParentRef(a, b gatewayv1.ParentReference) bool {
	aGroup := gatewayv1.GroupName
	if a.Group != nil {
		aGroup = string(*a.Group)
	}
	bGroup := gatewayv1.GroupName
	if b.Group != nil {
		bGroup = string(*b.Group)
	}
	if aGroup != bGroup {
		return false
	}
	return a.Name == b.Name
}

// isIP is a simplistic check — if the string contains a colon (IPv6) or
// consists only of digits and dots (IPv4), treat it as an IP.
func isIP(s string) bool {
	for _, c := range s {
		if c != '.' && c != ':' && (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// IngressStatusError is returned when a status update fails.
type IngressStatusError struct {
	Resource string
	Err      error
}

func (e *IngressStatusError) Error() string {
	return fmt.Sprintf("status update for %s: %v", e.Resource, e.Err)
}
