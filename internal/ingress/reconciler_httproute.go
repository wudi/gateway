package ingress

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// HTTPRouteReconciler watches HTTPRoute resources and ReferenceGrants.
type HTTPRouteReconciler struct {
	client         client.Client
	store          *Store
	controller     *Controller
	controllerName string
}

// NewHTTPRouteReconciler registers the HTTPRoute reconciler with the manager.
func NewHTTPRouteReconciler(mgr ctrl.Manager, store *Store, controller *Controller, controllerName string) error {
	r := &HTTPRouteReconciler{
		client:         mgr.GetClient(),
		store:          store,
		controller:     controller,
		controllerName: controllerName,
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.HTTPRoute{}).
		Watches(&gatewayv1beta1.ReferenceGrant{}, handler.EnqueueRequestsFromMapFunc(
			r.referenceGrantToHTTPRoutes,
		)).
		Complete(r)
}

func (r *HTTPRouteReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("httproute", req.NamespacedName)

	var hr gatewayv1.HTTPRoute
	if err := r.client.Get(ctx, req.NamespacedName, &hr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("HTTPRoute deleted")
			r.store.DeleteHTTPRoute(req.NamespacedName)
			r.controller.TriggerReload()
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Only process HTTPRoutes that reference one of our Gateways
	if !r.referencesOurGateway(&hr) {
		r.store.DeleteHTTPRoute(req.NamespacedName)
		return reconcile.Result{}, nil
	}

	log.V(1).Info("HTTPRoute updated")
	r.store.SetHTTPRoute(&hr)
	r.controller.TriggerReload()

	// Update status for each parentRef
	if r.controller.IsLeader() {
		for _, ref := range hr.Spec.ParentRefs {
			if err := r.controller.StatusUpdater().UpdateHTTPRouteStatus(
				ctx, &hr, ref, true, "Accepted", "HTTPRoute accepted",
			); err != nil {
				log.Error(err, "Failed to update HTTPRoute status")
			}
		}
	}

	return reconcile.Result{}, nil
}

func (r *HTTPRouteReconciler) referencesOurGateway(hr *gatewayv1.HTTPRoute) bool {
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

		for _, gw := range r.store.ListGateways() {
			if gw.Namespace == ns && gw.Name == string(ref.Name) {
				gcName := string(gw.Spec.GatewayClassName)
				if gc, ok := r.store.GetGatewayClass(gcName); ok {
					if string(gc.Spec.ControllerName) == r.controllerName {
						return true
					}
				}
			}
		}
	}
	return false
}

// referenceGrantToHTTPRoutes maps ReferenceGrant events to HTTPRoutes in the
// granting namespace for re-reconciliation.
func (r *HTTPRouteReconciler) referenceGrantToHTTPRoutes(ctx context.Context, obj client.Object) []reconcile.Request {
	ns := obj.GetNamespace()
	var requests []reconcile.Request
	for _, hr := range r.store.ListHTTPRoutes() {
		for _, rule := range hr.Spec.Rules {
			for _, ref := range rule.BackendRefs {
				refNs := hr.Namespace
				if ref.Namespace != nil {
					refNs = string(*ref.Namespace)
				}
				if refNs == ns {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: hr.Namespace,
							Name:      hr.Name,
						},
					})
				}
			}
		}
	}
	return requests
}
