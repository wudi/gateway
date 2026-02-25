package ingress

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// EndpointSliceReconciler watches EndpointSlice and ExternalName Service resources.
type EndpointSliceReconciler struct {
	client     client.Client
	store      *Store
	controller *Controller
}

// NewEndpointSliceReconciler registers the EndpointSlice reconciler with the manager.
func NewEndpointSliceReconciler(mgr ctrl.Manager, store *Store, controller *Controller) error {
	r := &EndpointSliceReconciler{
		client:     mgr.GetClient(),
		store:      store,
		controller: controller,
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&discoveryv1.EndpointSlice{}).
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(r.externalNameServiceToEndpointSlice)).
		Complete(r)
}

func (r *EndpointSliceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("endpointslice", req.NamespacedName)

	var es discoveryv1.EndpointSlice
	if err := r.client.Get(ctx, req.NamespacedName, &es); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("EndpointSlice deleted")
			r.store.DeleteEndpointSlice(req.NamespacedName)
			r.controller.TriggerReload()
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	log.V(1).Info("EndpointSlice updated")
	r.store.SetEndpointSlice(&es)
	r.controller.TriggerReload()

	return reconcile.Result{}, nil
}

// externalNameServiceToEndpointSlice handles ExternalName Service changes.
// EndpointSlice watcher doesn't cover ExternalName Services (they don't generate EndpointSlices),
// so we watch Services of type ExternalName and trigger a reload when they change.
func (r *EndpointSliceReconciler) externalNameServiceToEndpointSlice(ctx context.Context, obj client.Object) []reconcile.Request {
	svc, ok := obj.(*corev1.Service)
	if !ok {
		return nil
	}

	// Only care about ExternalName services
	if svc.Spec.Type != corev1.ServiceTypeExternalName {
		// Also track ClusterIP services so we can resolve their IPs
		r.store.SetService(svc)
		return nil
	}

	r.store.SetService(svc)
	r.controller.TriggerReload()

	// No actual EndpointSlice to reconcile — just trigger a config rebuild
	return nil
}
