// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package sync

import (
	"context"
	"encoding/json"
	"fmt"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName string = "policy-template-sync"
const policyFmtStr string = "policy: %s/%s"

var log = logf.Log.WithName(controllerName)

// Add creates a new Policy Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcilePolicy{client: mgr.GetClient(), scheme: mgr.GetScheme(),
		config: mgr.GetConfig(), recorder: mgr.GetEventRecorderFor(controllerName)}
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
	client   client.Client
	scheme   *runtime.Scheme
	config   *rest.Config
	recorder record.EventRecorder
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

	var rMapper meta.RESTMapper
	var dClient dynamic.Interface
	if len(instance.Spec.PolicyTemplates) > 0 {
		// initialize restmapper
		clientset := kubernetes.NewForConfigOrDie(r.config)
		dd := clientset.Discovery()
		apigroups, err := restmapper.GetAPIGroupResources(dd)
		if err != nil {
			reqLogger.Error(err, "Failed to create restmapper")
			return reconcile.Result{}, err
		}
		rMapper = restmapper.NewDiscoveryRESTMapper(apigroups)

		// initialize dynamic client
		dClient, err = dynamic.NewForConfig(r.config)
		if err != nil {
			reqLogger.Error(err, "Failed to create dynamic client")
			return reconcile.Result{}, err
		}
	} else {
		reqLogger.Info("Spec.PolicyTemplates is empty, nothing to reconcile.")
		return reconcile.Result{}, nil
	}

	// PolicyTemplates is not empty
	// loop through policy templates
	for _, policyT := range instance.Spec.PolicyTemplates {
		object, gvk, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			// failed to decode PolicyTemplate, skipping it, should throw violation
			reqLogger.Error(err, "Failed to decode policy template...")
			r.recorder.Event(instance, "Warning", "PolicyTemplateSync",
				fmt.Sprintf("Failed to decode policy template with err: %s", err))
			continue
		}
		var rsrc schema.GroupVersionResource
		mapping, err := rMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if mapping != nil {
			rsrc = mapping.Resource
		} else {
			// mapping not found, should create a violation event
			reqLogger.Error(err, "Mapping not found...")
			r.recorder.Event(instance, "Warning", "PolicyTemplateSync",
				fmt.Sprintf("Mapping not found with err: %s", err))
			mappingErrMsg := fmt.Sprintf("NonCompliant; %s, please check if you have CRD deployed.", err)
			r.recorder.Event(instance, "Warning",
				fmt.Sprintf(policyFmtStr, instance.GetNamespace(), object.(metav1.Object).GetName()), mappingErrMsg)
			continue
		}
		// fetch resource
		res := dClient.Resource(rsrc).Namespace(instance.GetNamespace())
		tName := object.(metav1.Object).GetName()
		tObjectUnstructured := &unstructured.Unstructured{}
		err = json.Unmarshal(policyT.ObjectDefinition.Raw, tObjectUnstructured)
		if err != nil {
			// failed to decode PolicyTemplate, skipping it, should throw violation
			reqLogger.Error(err, "Failed to unmarshal policy template...")
			r.recorder.Event(instance, "Warning", "PolicyTemplateSync",
				fmt.Sprintf("Failed to unmarshal policy template with err: %s", err))
			continue
		}
		eObject, err := res.Get(context.TODO(), tName, metav1.GetOptions{})
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
						"cluster-name":               instance.GetLabels()[common.ClusterNameLabel],
						common.ClusterNameLabel:      instance.GetLabels()[common.ClusterNameLabel],
						"cluster-namespace":          instance.GetLabels()[common.ClusterNamespaceLabel],
						common.ClusterNamespaceLabel: instance.GetLabels()[common.ClusterNamespaceLabel],
					}
				} else {
					labels["cluster-name"] = instance.GetLabels()[common.ClusterNameLabel]
					labels[common.ClusterNameLabel] = instance.GetLabels()[common.ClusterNameLabel]
					labels["cluster-namespace"] = instance.GetLabels()[common.ClusterNamespaceLabel]
					labels[common.ClusterNamespaceLabel] = instance.GetLabels()[common.ClusterNamespaceLabel]
				}
				tObjectUnstructured.SetLabels(labels)
				tObjectUnstructured.SetOwnerReferences([]metav1.OwnerReference{plcOwnerReferences})

				overrideRemediationAction(instance, tObjectUnstructured)

				_, err = res.Create(context.TODO(), tObjectUnstructured, metav1.CreateOptions{})
				if err != nil {
					// failed to create policy template
					reqLogger.Error(err, "Failed to create policy template...", "PolicyTemplateName", tName)
					r.recorder.Event(instance, "Warning", "PolicyTemplateSync",
						fmt.Sprintf("Failed to create policy template %s", tName))
					createErrMsg := fmt.Sprintf("NonCompliant; Failed to create policy template %s", err)
					r.recorder.Event(instance, "Warning",
						fmt.Sprintf(policyFmtStr, instance.GetNamespace(), object.(metav1.Object).GetName()), createErrMsg)
					return reconcile.Result{}, err
				}
				reqLogger.Info("Policy template created successfully...", "PolicyTemplateName", tName)
				r.recorder.Event(instance, "Normal", "PolicyTemplateSync",
					fmt.Sprintf("Policy template %s was created successfully", tName))

				// The policy template was created successfully, so requeue for further processing
				// of the other policy templates
				return reconcile.Result{Requeue: true}, nil
			}
			// other error
			r.recorder.Event(instance, "Warning", "PolicyTemplateSync",
				fmt.Sprintf("Failed to create policy template %s", tName))
			return reconcile.Result{}, err
		}

		refName := string(eObject.GetOwnerReferences()[0].Name)
		//violation if object reference and policy don't match
		if instance.GetName() != refName {
			alreadyExistsErrMsg := fmt.Sprintf(
				"Template name must be unique. Policy template with kind: %s name: %s already exists in policy %s",
				tObjectUnstructured.Object["kind"],
				tName,
				refName)
			r.recorder.Event(instance, "Warning",
				fmt.Sprintf(policyFmtStr, instance.GetNamespace(), tName), "NonCompliant; "+alreadyExistsErrMsg)
			r.recorder.Event(instance, "Warning", "PolicyTemplateSync", alreadyExistsErrMsg)
			reqLogger.Error(errors.NewBadRequest(alreadyExistsErrMsg), "Failed to create policy template...",
				"PolicyTemplateName", tName)
			continue
		}

		overrideRemediationAction(instance, tObjectUnstructured)
		// got object, need to compare both spec and annotation and update
		eObjectUnstructured := eObject.UnstructuredContent()
		if (!equality.Semantic.DeepEqual(eObjectUnstructured["spec"], tObjectUnstructured.Object["spec"])) ||
			(!equality.Semantic.DeepEqual(eObject.GetAnnotations(), tObjectUnstructured.GetAnnotations())) {
			// doesn't match
			reqLogger.Info("existing object and template don't match, updating...", "PolicyTemplateName", tName)
			eObjectUnstructured["spec"] = tObjectUnstructured.Object["spec"]
			eObject.SetAnnotations(tObjectUnstructured.GetAnnotations())
			_, err = res.Update(context.TODO(), eObject, metav1.UpdateOptions{})
			if err != nil {
				reqLogger.Error(err, "Failed to update policy template...", "PolicyTemplateName", tName)
				r.recorder.Event(instance, "Warning", "PolicyTemplateSync",
					fmt.Sprintf("Failed to update policy template %s", tName))
				return reconcile.Result{}, err
			}
			reqLogger.Info("existing object has been updated...", "PolicyTemplateName", tName)
			r.recorder.Event(instance, "Normal", "PolicyTemplateSync",
				fmt.Sprintf("Policy template %s was updated successfully", tName))
		}
	}
	reqLogger.Info("Reconciliation complete.")
	return reconcile.Result{}, nil
}

func overrideRemediationAction(instance *policiesv1.Policy, tObjectUnstructured *unstructured.Unstructured) {
	// override RemediationAction only when it is set on parent
	if instance.Spec.RemediationAction != "" {
		if spec, ok := tObjectUnstructured.Object["spec"]; ok {
			specObject := spec.(map[string]interface{})
			specObject["remediationAction"] = string(instance.Spec.RemediationAction)
		}
	}
}
