// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package secretsync

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "secret-sync"
	SecretName     = "policy-encryption-key"
)

var log = logf.Log.WithName(ControllerName)

// SetupWithManager sets up the controller with the Manager.
func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &SecretReconciler{}

type SecretReconciler struct {
	client.Client
	ManagedClient client.Client
	Scheme        *runtime.Scheme
}

// WARNING: In production, this should be namespaced to the actual managed cluster namespace.
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=create
//+kubebuilder:rbac:groups=core,resources=secrets,resourceNames=policy-encryption-key,verbs=delete;get;update

// Reconcile handles updates to the "policy-encryption-key" Secret in the managed cluster namespace on the Hub.
// The method is responsible for synchronizing the Secret to the managed cluster namespace on the managed cluster.
func (r *SecretReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.V(1).Info("Reconciling Secret")
	// The cache configuration of SelectorsByObject should prevent this from happening, but add this as a precaution.
	if request.Name != SecretName {
		log.Info("Got a reconciliation request for an unexpected Secret. This should have been filtered out.")

		return reconcile.Result{}, nil
	}

	hubEncryptionSecret := &corev1.Secret{}

	err := r.Get(ctx, request.NamespacedName, hubEncryptionSecret)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Failed to get the Secret on the Hub. Requeueing the request.")

			return reconcile.Result{}, err
		}

		log.Info("The Secret is no longer on the Hub. Deleting the replicated Secret.")

		err := r.ManagedClient.Delete(
			ctx,
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      request.Name,
					Namespace: request.Namespace,
				},
			},
		)
		if err != nil && !errors.IsNotFound(err) {
			log.Error(err, "Failed to delete the replicated Secret. Requeueing the request.")

			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	}

	managedEncryptionSecret := &corev1.Secret{}
	err = r.ManagedClient.Get(ctx, request.NamespacedName, managedEncryptionSecret)

	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Failed to get the replicated Secret. Requeueing the request.")

			return reconcile.Result{}, err
		}

		// Don't completely copy the Hub secret since it isn't desired to have any annotations related to disaster
		// recovery copied over.
		managedEncryptionSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      request.Name,
				Namespace: request.Namespace,
			},
			Data: hubEncryptionSecret.Data,
		}

		err := r.ManagedClient.Create(ctx, managedEncryptionSecret)
		if err != nil {
			log.Error(err, "Failed to replicate the Secret. Requeueing the request.")

			return reconcile.Result{}, err
		}

		reqLogger.V(1).Info("Reconciliation complete")

		return reconcile.Result{}, nil
	}

	if !equality.Semantic.DeepEqual(hubEncryptionSecret.Data, managedEncryptionSecret.Data) {
		log.V(1).Info("Updating the replicated secret due to it not matching the source on the Hub")

		managedEncryptionSecret.Data = hubEncryptionSecret.Data

		err := r.ManagedClient.Update(ctx, managedEncryptionSecret)
		if err != nil {
			log.Error(err, "Failed to update the replicated Secret. Requeueing the request.")

			return reconcile.Result{}, err
		}
	}

	reqLogger.V(1).Info("Reconciliation complete")

	return reconcile.Result{}, nil
}
