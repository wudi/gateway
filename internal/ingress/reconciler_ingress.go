package ingress

import (
	"context"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// IngressReconciler watches Ingress resources.
type IngressReconciler struct {
	client            client.Client
	store             *Store
	controller        *Controller
	ingressClass      string
	watchWithoutClass bool
}

// NewIngressReconciler registers the Ingress reconciler with the manager.
func NewIngressReconciler(mgr ctrl.Manager, store *Store, controller *Controller, ingressClass string, watchWithoutClass bool) error {
	r := &IngressReconciler{
		client:            mgr.GetClient(),
		store:             store,
		controller:        controller,
		ingressClass:      ingressClass,
		watchWithoutClass: watchWithoutClass,
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}

func (r *IngressReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("ingress", req.NamespacedName)

	var ing networkingv1.Ingress
	if err := r.client.Get(ctx, req.NamespacedName, &ing); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Deleted
			log.V(1).Info("Ingress deleted")
			r.store.DeleteIngress(req.NamespacedName)
			r.controller.TriggerReload()
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Filter by ingress class
	if !r.shouldProcess(&ing) {
		// Not ours — remove from store if it was previously tracked
		r.store.DeleteIngress(req.NamespacedName)
		return reconcile.Result{}, nil
	}

	log.V(1).Info("Ingress updated")
	r.store.SetIngress(&ing)
	r.controller.TriggerReload()

	// Update status (leader only)
	if r.controller.IsLeader() {
		if err := r.controller.StatusUpdater().UpdateIngressStatus(ctx, &ing); err != nil {
			log.Error(err, "Failed to update Ingress status")
		}
	}

	return reconcile.Result{}, nil
}

func (r *IngressReconciler) shouldProcess(ing *networkingv1.Ingress) bool {
	if ing.Spec.IngressClassName != nil {
		return *ing.Spec.IngressClassName == r.ingressClass
	}
	if v, ok := ing.Annotations["kubernetes.io/ingress.class"]; ok {
		return v == r.ingressClass
	}
	return r.watchWithoutClass
}

// IngressesForSecret returns all Ingress resources that reference the given secret.
func IngressesForSecret(store *Store, key types.NamespacedName) []types.NamespacedName {
	var result []types.NamespacedName
	for _, ing := range store.ListIngresses() {
		for _, tls := range ing.Spec.TLS {
			if tls.SecretName == key.Name && ing.Namespace == key.Namespace {
				result = append(result, types.NamespacedName{
					Namespace: ing.Namespace,
					Name:      ing.Name,
				})
				break
			}
		}
	}
	return result
}
