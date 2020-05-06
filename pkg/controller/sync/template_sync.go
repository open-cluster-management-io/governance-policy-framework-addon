// Copyright (c) 2020 Red Hat, Inc.
package sync

import (
	"context"
	"encoding/json"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("policy-template-sync")

// Add creates a new Policy Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	// dclient, err := dynamic.NewForConfig(mgr.GetConfig())
	// if err != nil {
	// 	log.Error(err, "")
	// 	os.Exit(1)
	// }
	return &ReconcilePolicy{client: mgr.GetClient(), scheme: mgr.GetScheme(), config: mgr.GetConfig()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("policy-template-sync", mgr, controller.Options{Reconciler: r})
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
	client client.Client
	scheme *runtime.Scheme
	config *rest.Config
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
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			reqLogger.Info("Policy not found, may have been deleted, reconciliation completed.")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// found
	// loop throught policy templates
	for _, policyT := range instance.Spec.PolicyTemplates {
		clientset := kubernetes.NewForConfigOrDie(r.config)
		dd := clientset.Discovery()
		apigroups, err := restmapper.GetAPIGroupResources(dd)
		if err != nil {
			// throw err
		}
		restmapper := restmapper.NewDiscoveryRESTMapper(apigroups)
		object, gvk, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			// failed to decode PolicyTemplate, skipping it, should throw violation
			reqLogger.Error(err, "Failed to decode policy template...")
			break
		}
		var rsrc schema.GroupVersionResource
		mapping, err := restmapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if mapping != nil {
			rsrc = mapping.Resource
		} else {
			// mapping not found, should create a violation event
			reqLogger.Error(err, "Mapping not found")
			return reconcile.Result{}, nil

		}
		dClient, err := dynamic.NewForConfig(r.config)
		// if err != nil {
		// 	log.Error(err, "")
		// 	os.Exit(1)
		// }
		res := dClient.Resource(rsrc).Namespace(instance.GetNamespace())
		tName := object.(metav1.Object).GetName()
		tObjectUnstructured := &unstructured.Unstructured{}
		json.Unmarshal(policyT.ObjectDefinition.Raw, tObjectUnstructured)
		eObject, err := res.Get(tName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				// not found should create it
				plcOwnerReferences := *metav1.NewControllerRef(instance, schema.GroupVersionKind{
					Group:   policiesv1.SchemeGroupVersion.Group,
					Version: policiesv1.SchemeGroupVersion.Version,
					Kind:    policiesv1.Kind,
				})
				labels := tObjectUnstructured.GetLabels()
				if labels == nil {
					labels = map[string]string{
						"cluster-name":      instance.GetLabels()["cluster-name"],
						"cluster-namespace": instance.GetLabels()["cluster-namespace"],
					}
				} else {
					labels["cluster-name"] = instance.GetLabels()["cluster-name"]
					labels["cluster-namespace"] = instance.GetLabels()["cluster-namespace"]
				}
				tObjectUnstructured.SetLabels(labels)
				tObjectUnstructured.SetOwnerReferences([]metav1.OwnerReference{plcOwnerReferences})
				if spec, ok := tObjectUnstructured.Object["spec"]; ok {
					specObject := spec.(map[string]interface{})
					if _, ok := specObject["remediationAction"]; ok {
						specObject["remediationAction"] = instance.Spec.RemediationAction
					}
				}
				_, err = res.Create(tObjectUnstructured, metav1.CreateOptions{})
				if err != nil {
					// failed to create policy template
					reqLogger.Error(err, "Failed to create policy template...", "PolicyTemplateName", tName)
					return reconcile.Result{}, err
				}
				reqLogger.Info("Policy template created successfully...", "PolicyTemplateName", tName)

			}
			// other error
			return reconcile.Result{}, err
		}
		// got object, need to compare and update
		eObjectUnstructured := eObject.UnstructuredContent()
		if !equality.Semantic.DeepEqual(eObjectUnstructured["spec"], tObjectUnstructured.Object["spec"]) {
			// doesn't match
			reqLogger.Info("existing object and template don't match, updating...", "PolicyTemplateName", tName)
			eObjectUnstructured["spec"] = tObjectUnstructured.Object["spec"]
			_, err = res.Update(eObject, metav1.UpdateOptions{})
			if err != nil {
				reqLogger.Error(err, "Failed to update policy template...", "PolicyTemplateName", tName)
				return reconcile.Result{}, err
			}
			reqLogger.Info("existing object has been updated...", "PolicyTemplateName", tName)
		}
	}
	reqLogger.Info("Reconciliation complete.")
	return reconcile.Result{}, nil
}
