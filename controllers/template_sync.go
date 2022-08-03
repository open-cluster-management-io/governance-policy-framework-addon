// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/record"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName string = "policy-template-sync"
	policyFmtStr   string = "policy: %s/%s"
)

var log = ctrl.Log.WithName(ControllerName)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=*,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&policiesv1.Policy{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
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

	// Do not exit early from the loop - store an error to return later and `continue`. Be careful
	// not to overwrite the error in a way that it becomes nil, which would prevent a requeue.
	// As a quirk of the error handling, only the last occurring error is "returned" by Reconcile.
	var resultError error

	// PolicyTemplates is not empty
	// loop through policy templates
	for tIndex, policyT := range instance.Spec.PolicyTemplates {
		object, gvk, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			resultError = err
			errMsg := fmt.Sprintf("Failed to decode policy template with err: %s", err)

			r.emitTemplateError(instance, tIndex, fmt.Sprintf("[template %v]", tIndex), errMsg)
			reqLogger.Error(resultError, "Failed to decode the policy template", "templateIndex", tIndex)

			continue
		}

		var tName string
		if tMetaObj, ok := object.(metav1.Object); ok {
			tName = tMetaObj.GetName()
		}

		if tName == "" {
			errMsg := fmt.Sprintf("Failed to get name from policy template at index %v", tIndex)
			resultError = errors.NewBadRequest(errMsg)

			r.emitTemplateError(instance, tIndex, fmt.Sprintf("[template %v]", tIndex), errMsg)
			reqLogger.Error(resultError, "Failed to process the policy template", "templateIndex", tIndex)

			continue
		}

		tLogger := reqLogger.WithValues("template", tName)

		var rsrc schema.GroupVersionResource

		mapping, err := rMapper.RESTMapping(gvk.GroupKind(), gvk.Version)

		if mapping != nil {
			rsrc = mapping.Resource
		} else {
			resultError = err
			errMsg := fmt.Sprintf("Mapping not found, please check if you have CRD deployed: %s", err)

			r.emitTemplateError(instance, tIndex, tName, errMsg)
			tLogger.Error(err, "Could not find an API mapping for the object definition",
				"group", gvk.Group,
				"version", gvk.Version,
				"kind", gvk.Kind,
			)

			continue
		}

		// reject if not configuration policy and has templates
		if gvk.Kind != "ConfigurationPolicy" {
			// if not configuration policies ,do a simple check for templates {{hub and reject
			// only checking for hub and not {{ as they could be valid cases where they are valid chars.
			if strings.Contains(string(policyT.ObjectDefinition.Raw), "{{hub ") {
				errMsg := fmt.Sprintf("Templates are not supported for kind : %s", gvk.Kind)
				resultError = errors.NewBadRequest(errMsg)

				r.emitTemplateError(instance, tIndex, tName, errMsg)
				tLogger.Error(resultError, "Failed to process the policy template")

				continue
			}
		}

		// fetch resource
		res := dClient.Resource(rsrc).Namespace(instance.GetNamespace())
		tObjectUnstructured := &unstructured.Unstructured{}
		err = json.Unmarshal(policyT.ObjectDefinition.Raw, tObjectUnstructured)

		if err != nil {
			resultError = err
			errMsg := fmt.Sprintf("Failed to unmarshal the policy template: %s", err)

			r.emitTemplateError(instance, tIndex, tName, errMsg)
			tLogger.Error(resultError, "Failed to unmarshal the policy template")

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
					resultError = err
					errMsg := fmt.Sprintf("Failed to create policy template: %s", err)

					r.emitTemplateError(instance, tIndex, tName, errMsg)
					tLogger.Error(resultError, "Failed to create policy template")

					continue
				}

				successMsg := fmt.Sprintf("Policy template %s created successfully", tName)
				tLogger.Info("Policy template created successfully", "PolicyTemplateName", tName)

				err = r.handleSyncSuccess(ctx, instance, tIndex, tName, successMsg, res)
				if err != nil {
					resultError = err
					tLogger.Error(resultError, "Error after creating template (will requeue)")
				}

				continue
			} else {
				// a different error getting template object from cluster
				resultError = err
				errMsg := fmt.Sprintf("Failed to get the object in the policy template: %s", err)

				r.emitTemplateError(instance, tIndex, tName, errMsg)
				tLogger.Error(err, "Failed to get the object in the policy template",
					"namespace", instance.GetNamespace(),
					"kind", gvk.Kind,
				)

				continue
			}
		}

		refName := eObject.GetOwnerReferences()[0].Name
		// violation if object reference and policy don't match
		if instance.GetName() != refName {
			errMsg := fmt.Sprintf(
				"Template name must be unique. Policy template with kind: %s name: %s already exists in policy %s",
				tObjectUnstructured.Object["kind"],
				tName,
				refName)
			resultError = errors.NewBadRequest(errMsg)

			r.emitTemplateError(instance, tIndex, tName, errMsg)
			tLogger.Error(resultError, "Failed to create the policy template")

			continue
		}

		overrideRemediationAction(instance, tObjectUnstructured)
		// got object, need to compare both spec and annotation and update
		eObjectUnstructured := eObject.UnstructuredContent()
		if (!equality.Semantic.DeepEqual(eObjectUnstructured["spec"], tObjectUnstructured.Object["spec"])) ||
			(!equality.Semantic.DeepEqual(eObject.GetAnnotations(), tObjectUnstructured.GetAnnotations())) {
			// doesn't match
			tLogger.Info("Existing object and template didn't match, will update")

			eObjectUnstructured["spec"] = tObjectUnstructured.Object["spec"]

			eObject.SetAnnotations(tObjectUnstructured.GetAnnotations())

			_, err = res.Update(ctx, eObject, metav1.UpdateOptions{})
			if err != nil {
				resultError = err
				errMsg := fmt.Sprintf("Failed to update policy template %s: %s", tName, err)

				r.emitTemplateError(instance, tIndex, tName, errMsg)
				tLogger.Error(err, "Failed to update the policy template")

				continue
			}

			successMsg := fmt.Sprintf("Policy template %s was updated successfully", tName)

			err = r.handleSyncSuccess(ctx, instance, tIndex, tName, successMsg, res)
			if err != nil {
				resultError = err
				tLogger.Error(resultError, "Error after updating template (will requeue)")
			}

			tLogger.Info("Existing object has been updated")
		} else {
			err = r.handleSyncSuccess(ctx, instance, tIndex, tName, "", res)
			if err != nil {
				resultError = err
				tLogger.Error(resultError, "Error after confirming template matches (will requeue)")
			}

			tLogger.Info("Existing object matches the policy template")
		}
	}

	reqLogger.Info("Completed the reconciliation")

	return reconcile.Result{}, resultError
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

// emitTemplateError performs actions that ensure correct reporting of template errors in the
// policy framework. If the policy's status already reflects the current error, then no actions
// are taken.
func (r *PolicyReconciler) emitTemplateError(pol *policiesv1.Policy, tIndex int, tName, errMsg string) {
	// check if the error is already present in the policy status - if so, return early
	if strings.Contains(getLatestStatusMessage(pol, tIndex), errMsg) {
		return
	}

	// emit the non-compliance event
	policyComplianceReason := fmt.Sprintf(policyFmtStr, pol.GetNamespace(), tName)
	r.Recorder.Event(pol, "Warning", policyComplianceReason, "NonCompliant; template-error; "+errMsg)

	// emit an informational event
	r.Recorder.Event(pol, "Warning", "PolicyTemplateSync", errMsg)
}

// handleSyncSuccess performs common actions that should be run whenever a template is in sync,
// whether there were changes or not. If no changes occurred, an empty message should be passed in.
// If the given policy template was in a template-error state (determined by checking the status),
// then the template object's `status.compliant` field (complianceState) will be reset. When this
// occurs, the relevant policy controller must re-populate it, and emit a new compliance event for
// the framework to observe.
func (r *PolicyReconciler) handleSyncSuccess(
	ctx context.Context,
	pol *policiesv1.Policy,
	tIndex int,
	tName string,
	msg string,
	resInt dynamic.ResourceInterface,
) error {
	if msg != "" {
		r.Recorder.Event(pol, "Normal", "PolicyTemplateSync", msg)
	}

	// Only do additional steps if a template-error is the most recent status
	if !strings.Contains(getLatestStatusMessage(pol, tIndex), "template-error;") {
		return nil
	}

	jsonPatch := []byte(`[{"op":"remove","path":"/status/compliant"}]`)

	_, err := resInt.Patch(ctx, tName, types.JSONPatchType, jsonPatch, metav1.PatchOptions{}, "status")
	if err != nil {
		return fmt.Errorf("unable to reset the status of policy template %v: %w", tName, err)
	}

	return nil
}

// getLatestStatusMessage examines the policy and returns the most recent status message for
// the given template. Returns an empty string if no status is present for the template.
func getLatestStatusMessage(pol *policiesv1.Policy, tIndex int) string {
	if tIndex >= len(pol.Status.Details) {
		return ""
	}

	tmplDetails := pol.Status.Details[tIndex]
	if tmplDetails == nil {
		return ""
	}

	if len(tmplDetails.History) == 0 {
		return ""
	}

	return tmplDetails.History[0].Message
}
