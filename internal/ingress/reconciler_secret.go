package ingress

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// SecretReconciler watches Secrets referenced by Ingress TLS and Gateway listeners.
type SecretReconciler struct {
	client     client.Client
	store      *Store
	controller *Controller
}

// NewSecretReconciler registers the Secret reconciler with the manager.
func NewSecretReconciler(mgr ctrl.Manager, store *Store, controller *Controller) error {
	r := &SecretReconciler{
		client:     mgr.GetClient(),
		store:      store,
		controller: controller,
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		Complete(r)
}

func (r *SecretReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("secret", req.NamespacedName)

	var secret corev1.Secret
	if err := r.client.Get(ctx, req.NamespacedName, &secret); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("Secret deleted")
			r.store.DeleteSecret(req.NamespacedName)
			r.controller.TriggerReload()
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Only store TLS secrets
	if secret.Type != corev1.SecretTypeTLS && secret.Type != corev1.SecretTypeOpaque {
		return reconcile.Result{}, nil
	}

	// Check if this secret is referenced by any Ingress or Gateway
	if !r.isReferenced(&secret) {
		return reconcile.Result{}, nil
	}

	log.V(1).Info("Referenced TLS Secret updated")
	r.store.SetSecret(&secret)
	r.controller.TriggerReload()

	return reconcile.Result{}, nil
}

// isReferenced checks whether any Ingress TLS entry or Gateway listener references this secret.
func (r *SecretReconciler) isReferenced(secret *corev1.Secret) bool {
	// Check Ingress TLS references
	for _, ing := range r.store.ListIngresses() {
		if ing.Namespace != secret.Namespace {
			continue
		}
		for _, tls := range ing.Spec.TLS {
			if tls.SecretName == secret.Name {
				return true
			}
		}
	}

	// Check Gateway TLS references
	for _, gw := range r.store.ListGateways() {
		for _, l := range gw.Spec.Listeners {
			if l.TLS == nil {
				continue
			}
			for _, ref := range l.TLS.CertificateRefs {
				ns := gw.Namespace
				if ref.Namespace != nil {
					ns = string(*ref.Namespace)
				}
				if ns == secret.Namespace && string(ref.Name) == secret.Name {
					return true
				}
			}
		}
	}

	return false
}
