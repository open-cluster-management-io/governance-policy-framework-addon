// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package sync

import (
	"context"
	"fmt"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName string = "policy-spec-sync"

var log = logf.Log.WithName(controllerName)

// Add creates a new Policy Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, managedCfg *rest.Config) error {
	managedClient, err := client.New(managedCfg, client.Options{})
	if err != nil {
		log.Error(err, "Failed to generate client to managed cluster")
		return err
	}
	var kubeClient kubernetes.Interface = kubernetes.NewForConfigOrDie(managedCfg)
	eventsScheme := runtime.NewScheme()
	if err = v1.AddToScheme(eventsScheme); err != nil {
		return err
	}

	eventBroadcaster := record.NewBroadcaster()
	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		log.Error(err, "Failed to get watch namespace")
		return err
	}
	eventBroadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events(namespace)})
	managedRecorder := eventBroadcaster.NewRecorder(eventsScheme, v1.EventSource{Component: controllerName})

	return add(mgr, newReconciler(mgr, managedClient, managedRecorder))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, managedClient client.Client,
	managedRecorder record.EventRecorder) reconcile.Reconciler {
	return &ReconcilePolicy{hubClient: mgr.GetClient(), managedClient: managedClient,
		managedRecorder: managedRecorder, scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Policy
	err = c.Watch(&source.Kind{Type: &policiesv1.Policy{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcilePolicy{}

// ReconcilePolicy reconciles a Policy object
type ReconcilePolicy struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	hubClient       client.Client
	managedClient   client.Client
	managedRecorder record.EventRecorder
	scheme          *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePolicy) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Policy...")

	// Fetch the Policy instance
	instance := &policiesv1.Policy{}
	err := r.hubClient.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// repliated policy on hub was deleted, remove policy on managed cluster
			reqLogger.Info("Policy was deleted, removing on managed cluster...")
			err = r.managedClient.Delete(context.TODO(), &policiesv1.Policy{
				TypeMeta: metav1.TypeMeta{
					Kind:       policiesv1.Kind,
					APIVersion: policiesv1.SchemeGroupVersion.Group,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      request.Name,
					Namespace: request.Namespace,
				},
			})
			if err != nil && !errors.IsNotFound(err) {
				reqLogger.Error(err, "Failed to remove policy on managed cluster...")
			}
			reqLogger.Info("Policy has been removed from managed cluster...Reconciliation complete.")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Failed to get policy from hub...")
		return reconcile.Result{}, err
	}
	managedPlc := &policiesv1.Policy{}
	err = r.managedClient.Get(context.TODO(), request.NamespacedName, managedPlc)
	if err != nil {
		if errors.IsNotFound(err) {
			// not found on managed cluster, create it
			reqLogger.Info("Policy not found on managed cluster, creating it...")
			managedPlc = instance.DeepCopy()
			managedPlc.SetOwnerReferences(nil)
			managedPlc.SetResourceVersion("")
			err = r.managedClient.Create(context.TODO(), managedPlc)
			if err != nil {
				reqLogger.Error(err, "Failed to create policy on managed...")
				return reconcile.Result{}, err
			}
			r.managedRecorder.Event(instance, "Normal", "PolicySpecSync",
				fmt.Sprintf("Policy %s was synchronized to cluster namespace %s", instance.GetName(),
					instance.GetNamespace()))
		}
		reqLogger.Error(err, "Failed to get policy from managed...")
		return reconcile.Result{}, err
	}
	// found, then compare and update
	if !common.CompareSpecAndAnnotation(instance, managedPlc) {
		// update needed
		reqLogger.Info("Policy mismatch between hub and managed, updating it...")
		managedPlc.SetAnnotations(instance.GetAnnotations())
		managedPlc.Spec = instance.Spec
		err = r.managedClient.Update(context.TODO(), managedPlc)
		if err != nil && errors.IsNotFound(err) {
			reqLogger.Error(err, "Failed to update policy on managed...")
			return reconcile.Result{}, err
		}
		r.managedRecorder.Event(instance, "Normal", "PolicySpecSync",
			fmt.Sprintf("Policy %s was updated in cluster namespace %s", instance.GetName(),
				instance.GetNamespace()))
	}
	reqLogger.Info("Reconciliation complete.")
	return reconcile.Result{}, nil
}
