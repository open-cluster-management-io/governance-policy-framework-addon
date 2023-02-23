// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package templatesync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	depclient "github.com/stolostron/kubernetes-dependency-watches/client"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	extensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	ControllerName    string = "policy-template-sync"
	policyFmtStr      string = "policy: %s/%s"
	PolicyTypeLabel   string = "policy.open-cluster-management.io/policy-type"
	parentPolicyLabel string = "policy.open-cluster-management.io/policy"
)

var log = ctrl.Log.WithName(ControllerName)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=*,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=list;watch

// Setup sets up the controller
func (r *PolicyReconciler) Setup(mgr ctrl.Manager, depEvents *source.Channel) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&policiesv1.Policy{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Watches(depEvents, &handler.EnqueueRequestForObject{}).
		Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &PolicyReconciler{}

// PolicyReconciler reconciles a Policy object
type PolicyReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client.Client
	DynamicWatcher   depclient.DynamicWatcher
	Scheme           *runtime.Scheme
	Config           *rest.Config
	Recorder         record.EventRecorder
	ClusterNamespace string
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
	policyObjectID := depclient.ObjectIdentifier{
		Group:     policiesv1.GroupVersion.Group,
		Version:   policiesv1.GroupVersion.Version,
		Kind:      "Policy",
		Namespace: request.Namespace,
		Name:      request.Name,
	}

	err := r.Get(ctx, request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			reqLogger.Info("Policy not found, may have been deleted, reconciliation completed")

			_ = policyUserErrorsCounter.DeletePartialMatch(prometheus.Labels{"policy": request.Name})
			_ = policySystemErrorsCounter.DeletePartialMatch(prometheus.Labels{"policy": request.Name})

			err := r.DynamicWatcher.RemoveWatcher(policyObjectID)
			if err != nil {
				reqLogger.Error(err, "Error updating dependency watcher. Ignoring the failure.")
			}

			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Failed to get the policy, will requeue the request")

		policySystemErrorsCounter.WithLabelValues(request.Name, "", "get-error").Inc()

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

			policySystemErrorsCounter.WithLabelValues(instance.Name, "", "get-error").Inc()

			return reconcile.Result{}, err
		}

		rMapper = restmapper.NewDiscoveryRESTMapper(apigroups)

		// initialize dynamic client
		dClient, err = dynamic.NewForConfig(r.Config)
		if err != nil {
			reqLogger.Error(err, "Failed to create dynamic client")

			policySystemErrorsCounter.WithLabelValues(instance.Name, "", "client-error").Inc()

			return reconcile.Result{}, err
		}
	} else {
		reqLogger.Info("Spec.PolicyTemplates is empty, nothing to reconcile")

		return reconcile.Result{}, nil
	}

	allDeps := make(map[depclient.ObjectIdentifier]string)
	topLevelDeps := make(map[depclient.ObjectIdentifier]string)

	for _, dep := range instance.Spec.Dependencies {
		depID := depclient.ObjectIdentifier{
			Group:     dep.GroupVersionKind().Group,
			Version:   dep.GroupVersionKind().Version,
			Kind:      dep.GroupVersionKind().Kind,
			Namespace: dep.Namespace,
			Name:      dep.Name,
		}

		// Use cluster namespace for known policy types when the namespace is blank
		if depID.Namespace == "" && depID.Group == policiesv1.GroupVersion.Group &&
			depID.Version == policiesv1.GroupVersion.Version &&
			strings.HasSuffix(depID.Kind, "Policy") {
			depID.Namespace = request.Namespace
		}

		existingDep, ok := topLevelDeps[depID]
		if ok && existingDep != dep.Compliance {
			err := fmt.Errorf("dependency on %s has conflicting compliance states", dep.Name)

			reqLogger.Error(err, "Failed to decode the policy dependencies", "policy", instance.GetName())

			policyUserErrorsCounter.WithLabelValues(instance.Name, "", "dependency-error").Inc()

			continue
		}

		allDeps[depID] = dep.Compliance
		topLevelDeps[depID] = dep.Compliance
	}

	// Do not exit early from the loop - store an error to return later and `continue`. Be careful
	// not to overwrite the error in a way that it becomes nil, which would prevent a requeue.
	// As a quirk of the error handling, only the last occurring error is "returned" by Reconcile.
	var resultError error

	var templateNames []string

	// Array of templates managed by this policy to watch
	var childTemplates []depclient.ObjectIdentifier

	// PolicyTemplates is not empty
	// loop through policy templates
	for tIndex, policyT := range instance.Spec.PolicyTemplates {
		depConflictErr := false

		// use copy of dependencies scoped only to this template
		templateDeps := make(map[depclient.ObjectIdentifier]string)
		for k, v := range topLevelDeps {
			templateDeps[k] = v
		}

		for _, dep := range policyT.ExtraDependencies {
			depID := depclient.ObjectIdentifier{
				Group:     dep.GroupVersionKind().Group,
				Version:   dep.GroupVersionKind().Version,
				Kind:      dep.GroupVersionKind().Kind,
				Namespace: dep.Namespace,
				Name:      dep.Name,
			}

			// Use cluster namespace for known policy types when the namespace is blank
			if depID.Namespace == "" && depID.Group == policiesv1.GroupVersion.Group &&
				depID.Version == policiesv1.GroupVersion.Version &&
				strings.HasSuffix(depID.Kind, "Policy") {
				depID.Namespace = request.Namespace
			}

			existingDep, ok := templateDeps[depID]
			if ok && existingDep != dep.Compliance {
				// dependency conflict, fire error
				resultError = fmt.Errorf("dependency on %s has conflicting compliance states", dep.Name)
				errMsg := fmt.Sprintf("Failed to decode policy template with err: %s", resultError)

				r.emitTemplateError(instance, tIndex, fmt.Sprintf("[template %v]", tIndex), errMsg)
				reqLogger.Error(resultError, "Failed to decode the policy template", "templateIndex", tIndex)

				depConflictErr = true

				policyUserErrorsCounter.WithLabelValues(instance.Name, "", "dependency-error").Inc()

				break
			}

			allDeps[depID] = dep.Compliance
			templateDeps[depID] = dep.Compliance
		}

		// skip template if dependencies ask for conflicting compliances
		if depConflictErr {
			continue
		}

		object, gvk, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			resultError = err
			errMsg := fmt.Sprintf("Failed to decode policy template with err: %s", err)

			r.emitTemplateError(instance, tIndex, fmt.Sprintf("[template %v]", tIndex), errMsg)
			reqLogger.Error(resultError, "Failed to decode the policy template", "templateIndex", tIndex)

			policyUserErrorsCounter.WithLabelValues(instance.Name, "", "format-error").Inc()

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

			policyUserErrorsCounter.WithLabelValues(instance.Name, "", "format-error").Inc()

			continue
		}

		childTemplates = append(childTemplates, depclient.ObjectIdentifier{
			Group:     gvk.Group,
			Version:   gvk.Version,
			Kind:      gvk.Kind,
			Namespace: instance.GetNamespace(),
			Name:      tName,
		})

		templateNames = append(templateNames, tName)

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

			policyUserErrorsCounter.WithLabelValues(instance.Name, tName, "crd-error").Inc()

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

				policyUserErrorsCounter.WithLabelValues(instance.Name, tName, "format-error").Inc()

				continue
			}
		}

		dependencyFailures := r.processDependencies(ctx, dClient, rMapper, templateDeps, tLogger)

		// fetch resource
		res := dClient.Resource(rsrc).Namespace(instance.GetNamespace())
		tObjectUnstructured := &unstructured.Unstructured{}
		err = json.Unmarshal(policyT.ObjectDefinition.Raw, tObjectUnstructured)

		if err != nil {
			resultError = err
			errMsg := fmt.Sprintf("Failed to unmarshal the policy template: %s", err)

			r.emitTemplateError(instance, tIndex, tName, errMsg)
			tLogger.Error(resultError, "Failed to unmarshal the policy template")

			policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "unmarshal-error").Inc()

			continue
		}

		eObject, err := res.Get(ctx, tName, metav1.GetOptions{})
		if err != nil {
			if len(dependencyFailures) > 0 {
				// template must be pending, do not create it
				r.emitTemplatePending(instance, tIndex, tName, generatePendingMsg(dependencyFailures))
				tLogger.Info("Dependencies were not satisfied for the policy template",
					"namespace", instance.GetNamespace(),
					"kind", gvk.Kind,
				)

				continue
			}

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

				// set label to identify parent policy for this template
				labels[parentPolicyLabel] = instance.GetName()

				tObjectUnstructured.SetLabels(labels)
				tObjectUnstructured.SetOwnerReferences([]metav1.OwnerReference{plcOwnerReferences})

				overrideRemediationAction(instance, tObjectUnstructured)

				_, err = res.Create(ctx, tObjectUnstructured, metav1.CreateOptions{})
				if err != nil {
					resultError = err
					errMsg := fmt.Sprintf("Failed to create policy template: %s", err)

					r.emitTemplateError(instance, tIndex, tName, errMsg)
					tLogger.Error(resultError, "Failed to create policy template")

					policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "create-error").Inc()

					continue
				}

				successMsg := fmt.Sprintf("Policy template %s created successfully", tName)
				tLogger.Info("Policy template created successfully", "PolicyTemplateName", tName)

				err = r.handleSyncSuccess(ctx, instance, tIndex, tName, successMsg, res)
				if err != nil {
					resultError = err
					tLogger.Error(resultError, "Error after creating template (will requeue)")

					policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "patch-error").Inc()
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

				policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "get-error").Inc()

				continue
			}
		}

		if len(dependencyFailures) > 0 {
			// template must be pending, need to delete it and error
			r.emitTemplatePending(instance, tIndex, tName, generatePendingMsg(dependencyFailures))
			tLogger.Info("Dependencies were not satisfied for the policy template",
				"namespace", instance.GetNamespace(),
				"kind", gvk.Kind,
			)

			err = res.Delete(ctx, tName, metav1.DeleteOptions{})
			if err != nil {
				tLogger.Error(err, "Failed to delete a template that entered pending state",
					"namespace", instance.GetNamespace(),
					"name", tName,
				)
				policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "delete-error").Inc()

				resultError = err
			}

			continue
		}

		refName := ""

		for _, ownerref := range eObject.GetOwnerReferences() {
			refName = ownerref.Name

			break // just get the first ownerReference, if there are any at all
		}

		parentPolicyLabel, ok := eObject.GetLabels()["policy.open-cluster-management.io/policy"]

		if refName == "" && ok && parentPolicyLabel == instance.GetName() {
			// if owner ref has been unset but the template is still managed by this policy instance,
			// recover the owner reference
			plcOwnerReferences := *metav1.NewControllerRef(instance, schema.GroupVersionKind{
				Group:   policiesv1.SchemeGroupVersion.Group,
				Version: policiesv1.SchemeGroupVersion.Version,
				Kind:    policiesv1.Kind,
			})

			tObjectUnstructured.SetOwnerReferences([]metav1.OwnerReference{plcOwnerReferences})

			refName = instance.GetName()
		} else {
			// otherwise, leave the owner reference as-is
			tObjectUnstructured.SetOwnerReferences(eObject.GetOwnerReferences())
		}

		// violation if object reference and policy don't match
		if instance.GetName() != refName {
			var errMsg string

			if refName == "" {
				errMsg = fmt.Sprintf(
					"Template name must be unique. Policy template with "+
						"kind: %s name: %s already exists outside of a Policy",
					tObjectUnstructured.Object["kind"],
					tName)
			} else {
				errMsg = fmt.Sprintf(
					"Template name must be unique. Policy template with "+
						"kind: %s name: %s already exists in policy %s",
					tObjectUnstructured.Object["kind"],
					tName,
					refName)
			}

			resultError = errors.NewBadRequest(errMsg)

			r.emitTemplateError(instance, tIndex, tName, errMsg)
			tLogger.Error(resultError, "Failed to create the policy template")

			policyUserErrorsCounter.WithLabelValues(instance.Name, tName, "format-error").Inc()

			continue
		}

		overrideRemediationAction(instance, tObjectUnstructured)
		// got object, need to compare both spec and annotation and update
		eObjectUnstructured := eObject.UnstructuredContent()
		if (!equality.Semantic.DeepEqual(eObjectUnstructured["spec"], tObjectUnstructured.Object["spec"])) ||
			(!equality.Semantic.DeepEqual(eObject.GetAnnotations(), tObjectUnstructured.GetAnnotations())) ||
			(!equality.Semantic.DeepEqual(eObject.GetOwnerReferences(), tObjectUnstructured.GetOwnerReferences())) {
			// doesn't match
			tLogger.Info("Existing object and template didn't match, will update")

			eObjectUnstructured["spec"] = tObjectUnstructured.Object["spec"]

			eObject.SetAnnotations(tObjectUnstructured.GetAnnotations())

			eObject.SetOwnerReferences(tObjectUnstructured.GetOwnerReferences())

			_, err = res.Update(ctx, eObject, metav1.UpdateOptions{})
			if err != nil {
				// If the policy template retrieved from the cache has since changed, there will be a conflict error
				// and the reconcile should be retried since this is recoverable.
				if errors.IsConflict(err) {
					return reconcile.Result{}, err
				}

				resultError = err
				errMsg := fmt.Sprintf("Failed to update policy template %s: %s", tName, err)

				r.emitTemplateError(instance, tIndex, tName, errMsg)
				tLogger.Error(err, "Failed to update the policy template")

				policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "patch-error").Inc()

				continue
			}

			successMsg := fmt.Sprintf("Policy template %s was updated successfully", tName)

			err = r.handleSyncSuccess(ctx, instance, tIndex, tName, successMsg, res)
			if err != nil {
				resultError = err
				tLogger.Error(resultError, "Error after updating template (will requeue)")

				policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "patch-error").Inc()
			}

			tLogger.Info("Existing object has been updated")
		} else {
			err = r.handleSyncSuccess(ctx, instance, tIndex, tName, "", res)
			if err != nil {
				resultError = err
				tLogger.Error(resultError, "Error after confirming template matches (will requeue)")

				policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "patch-error").Inc()
			}

			tLogger.Info("Existing object matches the policy template")
		}
	}

	if len(allDeps) != 0 || len(childTemplates) != 0 {
		objectsToWatch := make([]depclient.ObjectIdentifier, 0, len(allDeps)+len(childTemplates))
		for depID := range allDeps {
			objectsToWatch = append(objectsToWatch, depID)
		}

		objectsToWatch = append(objectsToWatch, childTemplates...)

		err = r.DynamicWatcher.AddOrUpdateWatcher(policyObjectID, objectsToWatch...)
	} else {
		err = r.DynamicWatcher.RemoveWatcher(policyObjectID)
	}

	if err != nil {
		resultError = err
		reqLogger.Error(resultError, "Error updating dependency watcher")

		policySystemErrorsCounter.WithLabelValues(instance.Name, "", "client-error").Inc()
	}

	err = r.cleanUpExcessTemplates(ctx, dClient, *instance, templateNames)
	if err != nil {
		resultError = err
		reqLogger.Error(resultError, "Error cleaning up templates")
	}

	reqLogger.Info("Completed the reconciliation")

	return reconcile.Result{}, resultError
}

// cleanUpExcessTemplates compares existing policy templates on the cluster to those contained in the policy,
// and deletes those that have been renamed or removed from the parent policy
func (r *PolicyReconciler) cleanUpExcessTemplates(
	ctx context.Context,
	dClient dynamic.Interface,
	instance policiesv1.Policy,
	templateNames []string,
) error {
	crdLabelSelector := labels.SelectorFromSet(map[string]string{PolicyTypeLabel: "template"})
	crdsv1 := extensionsv1.CustomResourceDefinitionList{}
	tmplGVRs := []schema.GroupVersionResource{}

	// build list of GVRs for templates to check the parent label on
	err := r.List(ctx, &crdsv1, &client.ListOptions{LabelSelector: crdLabelSelector})
	if err == nil {
		for _, crd := range crdsv1.Items {
			if len(crd.Spec.Versions) > 0 {
				tmplGVRs = append(tmplGVRs, schema.GroupVersionResource{
					Group:    crd.Spec.Group,
					Resource: crd.Spec.Names.Plural,
					Version:  crd.Spec.Versions[0].Name,
				})
			}
		}
	} else if meta.IsNoMatchError(err) {
		crdsv1beta1 := extensionsv1beta1.CustomResourceDefinitionList{}
		err := r.List(ctx, &crdsv1beta1, &client.ListOptions{LabelSelector: crdLabelSelector})
		if err != nil {
			return fmt.Errorf("error listing v1beta1 CRDs with %s label: %w", crdLabelSelector, err)
		}
		for _, crd := range crdsv1beta1.Items {
			if len(crd.Spec.Versions) > 0 {
				tmplGVRs = append(tmplGVRs, schema.GroupVersionResource{
					Group:    crd.Spec.Group,
					Resource: crd.Spec.Names.Plural,
					Version:  crd.Spec.Versions[0].Name,
				})
			}
		}
	} else {
		return fmt.Errorf("error listing v1 CRDs with %s label: %w", crdLabelSelector, err)
	}

	for _, gvr := range tmplGVRs {
		// iterate through all templates with parent label set to see if they match
		children, err := dClient.Resource(gvr).Namespace(r.ClusterNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: parentPolicyLabel + "=" + instance.GetName(),
		})
		if err != nil {
			return fmt.Errorf("error listing %s objects: %w", gvr.String(), err)
		}

		for _, tmpl := range children.Items {
			// delete all templates with label that aren't still in the policy
			found := false

			for _, parentTmplName := range templateNames {
				if parentTmplName == tmpl.GetName() {
					found = true

					break
				}
			}

			if !found {
				err := dClient.Resource(schema.GroupVersionResource{
					Group:    gvr.Group,
					Version:  gvr.Version,
					Resource: gvr.Resource,
				}).Namespace(r.ClusterNamespace).Delete(ctx, tmpl.GetName(), metav1.DeleteOptions{})
				if err != nil {
					return fmt.Errorf("error deleting %s object %s: %w", gvr.String(), tmpl.GetName(), err)
				}
			}
		}
	}

	return nil
}

// processDependencies iterates through all dependencies of a template and returns an array of any that are not met
func (r *PolicyReconciler) processDependencies(ctx context.Context, dClient dynamic.Interface, rMapper meta.RESTMapper,
	templateDeps map[depclient.ObjectIdentifier]string, tLogger logr.Logger,
) []depclient.ObjectIdentifier {
	var dependencyFailures []depclient.ObjectIdentifier

	for dep := range templateDeps {
		depGvk := schema.GroupVersionKind{
			Group:   dep.Group,
			Version: dep.Version,
			Kind:    dep.Kind,
		}

		var rsrc schema.GroupVersionResource

		depMapping, err := rMapper.RESTMapping(depGvk.GroupKind(), depGvk.Version)

		if depMapping != nil {
			rsrc = depMapping.Resource
		} else {
			tLogger.Error(err, "Could not find an API mapping for the dependency", "object", dep)

			dependencyFailures = append(dependencyFailures, dep)

			continue
		}

		// set up namespace for replicated policy dependencies
		ns := dep.Namespace
		if ns == "" {
			ns = r.ClusterNamespace
		}

		// query object and compare compliance status to desired
		res := dClient.Resource(rsrc).Namespace(ns)

		depObj, err := res.Get(ctx, dep.Name, metav1.GetOptions{})
		if err != nil {
			tLogger.Info("Failed to get dependency object", "object", dep)

			dependencyFailures = append(dependencyFailures, dep)
		} else {
			depCompliance, found, err := unstructured.NestedString(depObj.Object, "status", "compliant")
			if err != nil || !found {
				tLogger.Info("Failed to get compliance for dependency object", "object", dep)

				dependencyFailures = append(dependencyFailures, dep)
			} else if depCompliance != templateDeps[dep] {
				tLogger.Info("Compliance mismatch for dependency object", "object", dep)

				dependencyFailures = append(dependencyFailures, dep)
			}
		}
	}

	return dependencyFailures
}

// generatePendingMsg formats the list of failed dependencies into a readable error.
// Example: `Dependencies were not satisfied: 1 is still pending (FooPolicy foo)`
func generatePendingMsg(dependencyFailures []depclient.ObjectIdentifier) string {
	names := make([]string, len(dependencyFailures))
	for i, dep := range dependencyFailures {
		names[i] = fmt.Sprintf("%s %s", dep.Kind, dep.Name)
	}

	nameStr := strings.Join(names, ", ")

	fmtStr := "Dependencies were not satisfied: %d are still pending (%s)"
	if len(dependencyFailures) == 1 {
		fmtStr = "Dependencies were not satisfied: %d is still pending (%s)"
	}

	return fmt.Sprintf(fmtStr, len(dependencyFailures), nameStr)
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

// emitTemplatePending performs actions that ensure correct reporting of pending dependencies in the
// policy framework. If the policy's status already reflects the current status, then no actions
// are taken.
func (r *PolicyReconciler) emitTemplatePending(pol *policiesv1.Policy, tIndex int, tName, msg string) {
	statusMsg := "Pending; " + msg
	eventType := "Warning"

	if pol.Spec.PolicyTemplates[tIndex].IgnorePending {
		statusMsg = "Compliant; " + msg + " but ignorePending is true"
		eventType = "Normal"
	}

	// check if the error is already present in the policy status - if so, return early
	if strings.Contains(getLatestStatusMessage(pol, tIndex), statusMsg) {
		return
	}

	// emit the non-compliance event
	policyComplianceReason := fmt.Sprintf(policyFmtStr, pol.GetNamespace(), tName)
	r.Recorder.Event(pol, eventType, policyComplianceReason, statusMsg)

	// emit an informational event
	r.Recorder.Event(pol, eventType, "PolicyTemplateSync", statusMsg)
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
