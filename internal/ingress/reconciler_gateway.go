package ingress

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GatewayReconciler watches GatewayClass and Gateway resources.
type GatewayReconciler struct {
	client         client.Client
	store          *Store
	controller     *Controller
	controllerName string
}

// NewGatewayReconciler registers the Gateway reconciler with the manager.
func NewGatewayReconciler(mgr ctrl.Manager, store *Store, controller *Controller, controllerName string) error {
	r := &GatewayReconciler{
		client:         mgr.GetClient(),
		store:          store,
		controller:     controller,
		controllerName: controllerName,
	}

	// Watch GatewayClass
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.GatewayClass{}).
		Complete(&gatewayClassReconciler{r: r}); err != nil {
		return err
	}

	// Watch Gateway
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.Gateway{}).
		Complete(r)
}

// gatewayClassReconciler handles GatewayClass events.
type gatewayClassReconciler struct {
	r *GatewayReconciler
}

func (r *gatewayClassReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("gatewayclass", req.Name)

	var gc gatewayv1.GatewayClass
	if err := r.r.client.Get(ctx, req.NamespacedName, &gc); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("GatewayClass deleted")
			r.r.store.DeleteGatewayClass(req.Name)
			r.r.controller.TriggerReload()
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Only accept GatewayClasses targeting our controller
	if string(gc.Spec.ControllerName) != r.r.controllerName {
		return reconcile.Result{}, nil
	}

	log.V(1).Info("GatewayClass updated")
	r.r.store.SetGatewayClass(&gc)
	r.r.controller.TriggerReload()

	// Update status
	if r.r.controller.IsLeader() {
		if err := r.r.controller.StatusUpdater().UpdateGatewayClassStatus(
			ctx, &gc, true, "Accepted", "GatewayClass accepted by "+r.r.controllerName,
		); err != nil {
			log.Error(err, "Failed to update GatewayClass status")
		}
	}

	return reconcile.Result{}, nil
}

func (r *GatewayReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("gateway", req.NamespacedName)

	var gw gatewayv1.Gateway
	if err := r.client.Get(ctx, req.NamespacedName, &gw); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("Gateway deleted")
			r.store.DeleteGateway(req.NamespacedName)
			r.controller.TriggerReload()
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Only process Gateways referencing our GatewayClass
	gcName := string(gw.Spec.GatewayClassName)
	gc, ok := r.store.GetGatewayClass(gcName)
	if !ok || string(gc.Spec.ControllerName) != r.controllerName {
		return reconcile.Result{}, nil
	}

	log.V(1).Info("Gateway updated")
	r.store.SetGateway(&gw)
	r.controller.TriggerReload()

	// Update status
	if r.controller.IsLeader() {
		if err := r.controller.StatusUpdater().UpdateGatewayStatus(
			ctx, &gw, true, "Accepted", "Gateway accepted",
		); err != nil {
			log.Error(err, "Failed to update Gateway status")
		}
	}

	return reconcile.Result{}, nil
}
