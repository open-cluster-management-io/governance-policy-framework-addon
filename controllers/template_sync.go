// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	policiesv1 "github.com/stolostron/governance-policy-propagator/api/v1"
	"github.com/stolostron/governance-policy-propagator/controllers/common"
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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName string = "policy-template-sync"
	policyFmtStr   string = "policy: %s/%s"
)

var log = ctrl.Log.WithName(ControllerName)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=*,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policies.ibm.com,resources=*,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&policiesv1.Policy{}).
		Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &PolicyReconciler{}

// PolicyReconciler reconciles a Policy object
type PolicyReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client.Client
	Scheme   *runtime.Scheme
	Config   *rest.Config
	Recorder record.EventRecorder
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *PolicyReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling the Policy")

	// Fetch the Policy instance
	instance := &policiesv1.Policy{}

	err := r.Get(ctx, request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			reqLogger.Info("Policy not found, may have been deleted, reconciliation completed")

			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Failed to get the policy, will requeue the request")

		return reconcile.Result{}, err
	}

	var rMapper meta.RESTMapper
	var dClient dynamic.Interface

	if len(instance.Spec.PolicyTemplates) > 0 {
		// initialize restmapper
		clientset := kubernetes.NewForConfigOrDie(r.Config)
		dd := clientset.Discovery()

		apigroups, err := restmapper.GetAPIGroupResources(dd)
		if err != nil {
			reqLogger.Error(err, "Failed to create restmapper")

			return reconcile.Result{}, err
		}

		rMapper = restmapper.NewDiscoveryRESTMapper(apigroups)

		// initialize dynamic client
		dClient, err = dynamic.NewForConfig(r.Config)
		if err != nil {
			reqLogger.Error(err, "Failed to create dynamic client")

			return reconcile.Result{}, err
		}
	} else {
		reqLogger.Info("Spec.PolicyTemplates is empty, nothing to reconcile")

		return reconcile.Result{}, nil
	}

	// PolicyTemplates is not empty
	// loop through policy templates
	for _, policyT := range instance.Spec.PolicyTemplates {
		object, gvk, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			// failed to decode PolicyTemplate, skipping it, should throw violation
			reqLogger.Error(err, "Failed to decode the policy template")
			r.Recorder.Event(instance, "Warning", "PolicyTemplateSync",
				fmt.Sprintf("Failed to decode policy template with err: %s", err))

			continue
		}
		var rsrc schema.GroupVersionResource

		mapping, err := rMapper.RESTMapping(gvk.GroupKind(), gvk.Version)

		if mapping != nil {
			rsrc = mapping.Resource
		} else {
			// mapping not found, should create a violation event
			reqLogger.Error(
				err,
				"Could not find an API mapping for the object definition",
				"group", gvk.Group,
				"version", gvk.Version,
				"kind", gvk.Kind,
			)
			r.Recorder.Event(instance, "Warning", "PolicyTemplateSync",
				fmt.Sprintf("Mapping not found with err: %s", err))
			mappingErrMsg := fmt.Sprintf("NonCompliant; %s, please check if you have CRD deployed.", err)
			r.Recorder.Event(instance, "Warning",
				fmt.Sprintf(policyFmtStr, instance.GetNamespace(), object.(metav1.Object).GetName()), mappingErrMsg)

			continue
		}

		// reject if not configuration policy and has templates
		if gvk.Kind != "ConfigurationPolicy" {
			// if not configuration policies ,do a simple check for templates {{hub and reject
			// only checking for hub and not {{ as they could be valid cases where they are valid chars.
			if strings.Contains(string(policyT.ObjectDefinition.Raw), "{{hub ") {
				reqLogger.Error(
					errors.NewBadRequest("Templates are not supported for this policy kind"),
					"Failed to process the policy template",
					"kind",
					gvk.Kind,
				)

				templatesErrMsg := fmt.Sprintf("Templates are not supported for kind : %s", gvk.Kind)

				r.Recorder.Event(instance, "Warning", "PolicyTemplateSync", templatesErrMsg)
				r.Recorder.Event(instance, "Warning",
					fmt.Sprintf(
						policyFmtStr, instance.GetNamespace(), object.(metav1.Object).GetName(),
					), "NonCompliant; "+templatesErrMsg)

				// continue to the next policy template
				continue
			}
		}

		// fetch resource
		res := dClient.Resource(rsrc).Namespace(instance.GetNamespace())
		tName := object.(metav1.Object).GetName()
		tObjectUnstructured := &unstructured.Unstructured{}
		err = json.Unmarshal(policyT.ObjectDefinition.Raw, tObjectUnstructured)

		if err != nil {
			// failed to decode PolicyTemplate, skipping it, should throw violation
			reqLogger.Error(err, "Failed to unmarshal the policy template")
			r.Recorder.Event(instance, "Warning", "PolicyTemplateSync",
				fmt.Sprintf("Failed to unmarshal policy template with err: %s", err))

			continue
		}

		eObject, err := res.Get(ctx, tName, metav1.GetOptions{})
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

				_, err = res.Create(ctx, tObjectUnstructured, metav1.CreateOptions{})
				if err != nil {
					// failed to create policy template
					reqLogger.Error(err, "Failed to create the policy template", "PolicyTemplateName", tName)
					r.Recorder.Event(instance, "Warning", "PolicyTemplateSync",
						fmt.Sprintf("Failed to create policy template %s", tName))

					createErrMsg := fmt.Sprintf("NonCompliant; Failed to create policy template %s", err)

					r.Recorder.Event(instance, "Warning",
						fmt.Sprintf(policyFmtStr, instance.GetNamespace(), object.(metav1.Object).GetName()),
						createErrMsg)

					return reconcile.Result{}, err
				}

				reqLogger.Info("Policy template created successfully", "PolicyTemplateName", tName)
				r.Recorder.Event(instance, "Normal", "PolicyTemplateSync",
					fmt.Sprintf("Policy template %s was created successfully", tName))

				// The policy template was created successfully, so requeue for further processing
				// of the other policy templates
				return reconcile.Result{Requeue: true}, nil
			}

			// other error
			reqLogger.Error(
				err,
				"Failed to get the object in the policy template",
				"name", tName,
				"namespace", instance.GetNamespace(),
				"kind", gvk.Kind,
			)
			r.Recorder.Event(instance, "Warning", "PolicyTemplateSync",
				fmt.Sprintf("Failed to create policy template %s", tName))

			return reconcile.Result{}, err
		}

		refName := eObject.GetOwnerReferences()[0].Name
		// violation if object reference and policy don't match
		if instance.GetName() != refName {
			alreadyExistsErrMsg := fmt.Sprintf(
				"Template name must be unique. Policy template with kind: %s name: %s already exists in policy %s",
				tObjectUnstructured.Object["kind"],
				tName,
				refName)
			r.Recorder.Event(instance, "Warning",
				fmt.Sprintf(policyFmtStr, instance.GetNamespace(), tName), "NonCompliant; "+alreadyExistsErrMsg)
			r.Recorder.Event(instance, "Warning", "PolicyTemplateSync", alreadyExistsErrMsg)
			reqLogger.Error(
				errors.NewBadRequest(alreadyExistsErrMsg),
				"Failed to create the policy template",
				"PolicyTemplateName", tName,
			)

			continue
		}

		overrideRemediationAction(instance, tObjectUnstructured)
		// got object, need to compare both spec and annotation and update
		eObjectUnstructured := eObject.UnstructuredContent()
		if (!equality.Semantic.DeepEqual(eObjectUnstructured["spec"], tObjectUnstructured.Object["spec"])) ||
			(!equality.Semantic.DeepEqual(eObject.GetAnnotations(), tObjectUnstructured.GetAnnotations())) {
			// doesn't match
			reqLogger.Info("Existing object and template didn't match, will update", "PolicyTemplateName", tName)

			eObjectUnstructured["spec"] = tObjectUnstructured.Object["spec"]

			eObject.SetAnnotations(tObjectUnstructured.GetAnnotations())

			_, err = res.Update(ctx, eObject, metav1.UpdateOptions{})
			if err != nil {
				reqLogger.Error(err, "Failed to update the policy template", "PolicyTemplateName", tName)
				r.Recorder.Event(instance, "Warning", "PolicyTemplateSync",
					fmt.Sprintf("Failed to update policy template %s", tName))

				return reconcile.Result{}, err
			}

			reqLogger.Info("Existing object has been updated", "PolicyTemplateName", tName)
			r.Recorder.Event(instance, "Normal", "PolicyTemplateSync",
				fmt.Sprintf("Policy template %s was updated successfully", tName))
		} else {
			reqLogger.Info("Existing object matches the policy template", "PolicyTemplateName", tName)
		}
	}

	reqLogger.Info("Completed the reconciliation")

	return reconcile.Result{}, nil
}

func overrideRemediationAction(instance *policiesv1.Policy, tObjectUnstructured *unstructured.Unstructured) {
	// override RemediationAction only when it is set on parent
	if instance.Spec.RemediationAction != "" {
		if spec, ok := tObjectUnstructured.Object["spec"]; ok {
			specObject, ok := spec.(map[string]interface{})
			if ok {
				specObject["remediationAction"] = string(instance.Spec.RemediationAction)
			}
		}
	}
}
