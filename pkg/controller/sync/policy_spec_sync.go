package sync

import (
	"context"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("policy-spec-sync")

// Add creates a new Policy Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, managedCfg *rest.Config) error {
	managedClient, err := client.New(managedCfg, client.Options{})
	if err != nil {
		log.Error(err, "Failed to generate client to managed cluster")
		return err
	}
	return add(mgr, newReconciler(mgr, managedClient))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, managedClient client.Client) reconcile.Reconciler {
	return &ReconcilePolicy{hubClient: mgr.GetClient(), managedClient: managedClient, scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("policy-spec-sync", mgr, controller.Options{Reconciler: r})
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
	hubClient     client.Client
	managedClient client.Client
	scheme        *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
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
			if err != nil {
				if errors.IsNotFound(err) {
					reqLogger.Info("Failed to remove policy on managed cluster...it has been already deleted.")
					return reconcile.Result{}, nil
				}
			}
			return reconcile.Result{}, err
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}
	managedPlc := &policiesv1.Policy{}
	err = r.managedClient.Get(context.TODO(), request.NamespacedName, managedPlc)
	if err != nil {
		if errors.IsNotFound(err) {
			// not found on managed cluster, create it
			reqLogger.Info("Policy not found on managed cluster, creating it...")
			instance.SetOwnerReferences(nil)
			instance.SetResourceVersion("")
			return reconcile.Result{}, r.managedClient.Create(context.TODO(), instance)
		}
	}
	// found, then compare and update
	if !equality.Semantic.DeepEqual(instance.GetAnnotations(), managedPlc.GetAnnotations()) || !equality.Semantic.DeepEqual(instance.Spec, managedPlc.Spec) {
		// update needed
		reqLogger.Info("Policy needs update...")
		managedPlc.SetAnnotations(instance.GetAnnotations())
		managedPlc.Spec = instance.Spec
		err = r.managedClient.Update(context.TODO(), managedPlc)
		if err != nil {
			return reconcile.Result{}, err
		}
	}
	reqLogger.Info("Reconciling complete...")
	return reconcile.Result{}, nil
}
