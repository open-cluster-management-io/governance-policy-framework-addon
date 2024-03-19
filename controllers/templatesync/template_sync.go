// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package templatesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	gktemplatesv1 "github.com/open-policy-agent/frameworks/constraint/pkg/apis/templates/v1"
	gktemplatesv1beta1 "github.com/open-policy-agent/frameworks/constraint/pkg/apis/templates/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	depclient "github.com/stolostron/kubernetes-dependency-watches/client"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	extensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/uninstall"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

const (
	ControllerName string = "policy-template-sync"
)

var (
	log                        = ctrl.Log.WithName(ControllerName)
	errResourceDiscoveryFailed *apiutil.ErrResourceDiscoveryFailed
)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=*,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=templates.gatekeeper.sh,resources=constrainttemplates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=constraints.gatekeeper.sh,resources=*,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=list;watch

// Setup sets up the controller
func (r *PolicyReconciler) Setup(mgr ctrl.Manager, depEvents *source.Channel) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&policiesv1.Policy{}).
		WithEventFilter(templatePredicates()).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.ConcurrentReconciles}).
		WatchesRawSource(depEvents, &handler.EnqueueRequestForObject{}).
		Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &PolicyReconciler{}

// PolicyReconciler reconciles a Policy object
type PolicyReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client.Client
	DynamicWatcher       depclient.DynamicWatcher
	Scheme               *runtime.Scheme
	Config               *rest.Config
	Recorder             record.EventRecorder
	ClusterNamespace     string
	Clientset            *kubernetes.Clientset
	InstanceName         string
	DisableGkSync        bool
	createdGkConstraint  *bool
	ConcurrentReconciles int
	// EventsQueue is a queue that accepts ComplianceAPIEventRequest to then be recorded in the compliance events
	// API by StartComplianceEventsSyncer. If the compliance events API is disabled, this will be nil.
	EventsQueue workqueue.RateLimitingInterface
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *PolicyReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.V(1).Info("Reconciling the Policy")

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
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned namespaced objects are automatically garbage collected. Additional cleanup logic uses
			// finalizers.
			reqLogger.Info("Policy not found, may have been deleted, reconciliation completed")

			_ = policyUserErrorsCounter.DeletePartialMatch(prometheus.Labels{"policy": request.Name})
			_ = policySystemErrorsCounter.DeletePartialMatch(prometheus.Labels{"policy": request.Name})

			err := r.DynamicWatcher.RemoveWatcher(policyObjectID)
			if err != nil {
				reqLogger.Error(err, "Error updating dependency watcher. Ignoring the failure.")
			}

			// Return and don't requeue
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Failed to get the policy, will requeue the request")

		policySystemErrorsCounter.WithLabelValues(request.Name, "", "get-error").Inc()

		return reconcile.Result{}, err
	}

	var discoveryClient discovery.DiscoveryInterface
	var dClient dynamic.Interface

	if len(instance.Spec.PolicyTemplates) > 0 {
		discoveryClient = r.Clientset.Discovery()

		// initialize dynamic client
		dClient, err = dynamic.NewForConfig(r.Config)
		if err != nil {
			reqLogger.Error(err, "Failed to create dynamic client")

			policySystemErrorsCounter.WithLabelValues(instance.Name, "", "client-error").Inc()

			return reconcile.Result{}, err
		}
	} else {
		reqLogger.Info("Spec.PolicyTemplates is empty, nothing to reconcile")

		// With no templates, ensure there's no finalizer
		if hasClusterwideFinalizer(instance) {
			removeFinalizer(instance, utils.ClusterwideFinalizer)

			err = r.Update(ctx, instance)
			if err != nil {
				reqLogger.Error(err, "Failed to update policy when removing finalizers")

				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Check duplicate names in configuration-policies
	has := hasDupName(instance)
	if has {
		msg := "There are duplicate names in configurationpolicies, please check the policy"
		reqLogger.Info(msg)

		for tIndex := range instance.Spec.PolicyTemplates {
			_ = r.emitTemplateError(ctx, instance, tIndex, fmt.Sprintf("template-%v", tIndex), false, msg)
		}

		return reconcile.Result{}, nil
	}

	// Whether policy needs a finalizer to handle cleanup
	var addFinalizer bool

	// Policy set for deletion--handle any finalizer cleanup
	if instance.DeletionTimestamp != nil {
		// No finalizer--skip reconcile while waiting for deletion
		if !hasClusterwideFinalizer(instance) {
			return reconcile.Result{}, nil
		}

		reqLogger.Info("Policy marked for deletion--proceeding with finalizer cleanup")

		err := finalizerCleanup(ctx, instance, discoveryClient, dClient)
		if err != nil {
			reqLogger.Error(err, "Failure during finalizer cleanup")

			return reconcile.Result{}, err
		}

		// Cleanup succeeded--remove finalizer
		reqLogger.Info("Cleanup complete--removing clusterwide cleanup finalizer")
		removeFinalizer(instance, utils.ClusterwideFinalizer)

		err = r.Update(ctx, instance)
		if err != nil {
			reqLogger.Error(err, "Failed to update policy when removing finalizers")

			policySystemErrorsCounter.WithLabelValues(instance.Name, "", "patch-error").Inc()

			return reconcile.Result{}, err
		}

		reqLogger.Info("Finalizer cleanup complete")

		return reconcile.Result{}, nil
	}

	if uninstall.DeploymentIsUninstalling {
		reqLogger.Info("Skipping reconcile because the deployment is in uninstallation mode")

		return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Handle dependencies that apply to the parent policy
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
		if ok && existingDep != string(dep.Compliance) {
			err := fmt.Errorf("dependency on %s has conflicting compliance states", dep.Name)

			reqLogger.Error(err, "Failed to decode the policy dependencies", "policy", instance.GetName())

			policyUserErrorsCounter.WithLabelValues(instance.Name, "", "dependency-error").Inc()

			continue
		}

		allDeps[depID] = string(dep.Compliance)
		topLevelDeps[depID] = string(dep.Compliance)
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
		// Gather raw object definition from the policy template
		object, gvk, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			// If it's missing the Kind, don't requeue since that requires an update to the Policy
			if !runtime.IsMissingKind(err) {
				resultError = err
			}

			errMsg := fmt.Sprintf("Failed to decode policy template with err: %s", err)

			_ = r.emitTemplateError(ctx, instance, tIndex, fmt.Sprintf("template-%v", tIndex), false, errMsg)

			reqLogger.Error(resultError, "Failed to decode the policy template", "templateIndex", tIndex)

			policyUserErrorsCounter.WithLabelValues(instance.Name, "", "format-error").Inc()

			continue
		}

		// Special handling booleans, whether this template is:
		// - ContraintTemplate handled by Gatekeeper
		// - Cluster scoped
		isGkConstraintTemplate := gvk.Group == utils.GvkConstraintTemplate.Group &&
			gvk.Kind == utils.GvkConstraintTemplate.Kind
		isGkConstraint := gvk.Group == utils.GConstraint
		isGkObj := isGkConstraintTemplate || isGkConstraint
		isClusterScoped := isGkObj

		// Handle dependencies that apply to the current policy-template
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
			if ok && existingDep != string(dep.Compliance) {
				// dependency conflict, fire error
				resultError = fmt.Errorf("dependency on %s has conflicting compliance states", dep.Name)
				errMsg := fmt.Sprintf("Failed to decode policy template with err: %s", resultError)

				_ = r.emitTemplateError(ctx, instance, tIndex,
					fmt.Sprintf("template-%v", tIndex), isClusterScoped, errMsg)

				reqLogger.Error(resultError, "Failed to decode the policy template", "templateIndex", tIndex)

				depConflictErr = true

				policyUserErrorsCounter.WithLabelValues(instance.Name, "", "dependency-error").Inc()

				break
			}

			allDeps[depID] = string(dep.Compliance)
			templateDeps[depID] = string(dep.Compliance)
		}

		// skip template if dependencies ask for conflicting compliances
		if depConflictErr {
			continue
		}

		var tName string
		if tMetaObj, ok := object.(metav1.Object); ok {
			tName = tMetaObj.GetName()
		}

		if tName == "" {
			errMsg := fmt.Sprintf("Failed to get name from policy template at index %v", tIndex)
			resultError = k8serrors.NewBadRequest(errMsg)

			_ = r.emitTemplateError(ctx, instance, tIndex,
				fmt.Sprintf("template-%v", tIndex), isClusterScoped, errMsg)

			reqLogger.Error(resultError, "Failed to process the policy template", "templateIndex", tIndex)

			policyUserErrorsCounter.WithLabelValues(instance.Name, "", "format-error").Inc()

			continue
		}

		templateNames = append(templateNames, tName)

		tLogger := reqLogger.WithValues("template", tName)

		rsrc, namespaced, err := utils.GVRFromGVK(discoveryClient, *gvk)
		if errors.Is(err, utils.ErrNoVersionedResource) {
			resultError = err
			errMsg := "Mapping not found, "

			if isGkObj && r.DisableGkSync {
				errMsg = "A Gatekeeper policy-template was provided, but the Gatekeeper integration is disabled"
			} else if isGkConstraintTemplate {
				errMsg += "check if Gatekeeper is installed"
			} else if isGkConstraint {
				errMsg += "check if the required ConstraintTemplate has been deployed"
			} else {
				errMsg += "check if you have the CRD deployed"
			}

			errMsg += fmt.Sprintf(": %s", err)

			_ = r.emitTemplateError(ctx, instance, tIndex, tName, isClusterScoped, errMsg)

			tLogger.Error(err, "Could not find an API mapping for the object definition",
				"group", gvk.Group,
				"version", gvk.Version,
				"kind", gvk.Kind,
			)

			policyUserErrorsCounter.WithLabelValues(instance.Name, tName, "crd-error").Inc()

			continue
		} else if err != nil {
			reqLogger.Error(err, "Failed to get the resource version metadata")

			policySystemErrorsCounter.WithLabelValues(instance.Name, "", "get-error").Inc()

			return reconcile.Result{}, err
		}

		// Now that there is a mapping, there is a definitive answer whether it's cluster scoped rather
		// than the previous educated guess based on the provided group and kind.
		isClusterScoped = !namespaced

		// Check for whether the CRD associated with this policy template has a policy-type=template
		// label, signaling that it should be synced
		hasTemplateLabel, err := r.hasPolicyTemplateLabel(ctx, rsrc)
		if err != nil {
			reqLogger.Error(err, fmt.Sprintf("Failed to retrieve CRD %s", rsrc.GroupResource().String()))

			policySystemErrorsCounter.WithLabelValues(request.Name, tName, "get-error").Inc()

			// The CRD should exist since it was found in the mapping previously; Requeue this template
			return reconcile.Result{}, err
		}

		// If no policy-type=template label AND the GroupKind is not on the explicit allow list, don't
		// sync this template
		if !hasTemplateLabel && !utils.IsAllowedPolicy(gvk.GroupKind()) || (isGkObj && r.DisableGkSync) {
			errMsg := fmt.Sprintf("policy-template kind is not supported: %s", gvk.String())

			if r.DisableGkSync && isGkObj {
				errMsg = fmt.Sprintf(
					"not syncing kind %s because the Gatekeeper integration is disabled", gvk.String())
			}

			err := fmt.Errorf(errMsg)

			resultError = err

			_ = r.emitTemplateError(ctx, instance, tIndex, tName, isClusterScoped, errMsg)

			tLogger.Error(err, "Unsupported policy-template kind found in object definition",
				"group", gvk.Group,
				"version", gvk.Version,
				"kind", gvk.Kind,
			)

			policyUserErrorsCounter.WithLabelValues(instance.Name, tName, "crd-error").Inc()

			continue
		}

		// reject if not configuration policy and has template strings, and don't requeue
		if gvk.Kind != "ConfigurationPolicy" {
			// if not configuration policies, do a simple check for templates {{hub and reject
			// only checking for hub and not {{ as they could be valid cases where they are valid chars.
			if strings.Contains(string(policyT.ObjectDefinition.Raw), "{{hub ") {
				errMsg := fmt.Sprintf("Templates are not supported for kind : %s", gvk.Kind)

				_ = r.emitTemplateError(ctx, instance, tIndex, tName, isClusterScoped, errMsg)

				tLogger.Error(k8serrors.NewBadRequest(errMsg), "Failed to process the policy template")

				policyUserErrorsCounter.WithLabelValues(instance.Name, tName, "format-error").Inc()

				continue
			}
		}

		dependencyFailures := r.processDependencies(ctx, dClient, discoveryClient, templateDeps, tLogger)

		// Instantiate a dynamic client -- if it's a clusterwide resource, then leave off the namespace
		var res dynamic.ResourceInterface

		resourceNs := ""

		if !isClusterScoped {
			resourceNs = instance.GetNamespace()
		}

		res = dClient.Resource(rsrc).Namespace(resourceNs)
		tObjectUnstructured := &unstructured.Unstructured{}

		err = json.Unmarshal(policyT.ObjectDefinition.Raw, tObjectUnstructured)
		if err != nil {
			resultError = err
			errMsg := fmt.Sprintf("Failed to unmarshal the policy template: %s", err)

			_ = r.emitTemplateError(ctx, instance, tIndex, tName, isClusterScoped, errMsg)

			tLogger.Error(resultError, "Failed to unmarshal the policy template")

			policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "unmarshal-error").Inc()

			continue
		}

		// For convenience, set the parent policy database ID if set. Then controllers can just use this value directly
		// when sending compliance events.
		if instance.Annotations[utils.ParentDBIDAnnotation] != "" {
			templateAnnotations := tObjectUnstructured.GetAnnotations()
			if templateAnnotations == nil {
				templateAnnotations = map[string]string{}
			}

			templateAnnotations[utils.ParentDBIDAnnotation] = instance.Annotations[utils.ParentDBIDAnnotation]

			tObjectUnstructured.SetAnnotations(templateAnnotations)
		}

		// Collect list of dependent policies
		childTemplates = append(childTemplates, depclient.ObjectIdentifier{
			Group:     gvk.Group,
			Version:   gvk.Version,
			Kind:      gvk.Kind,
			Namespace: resourceNs,
			Name:      tName,
		})

		// Attempt to fetch the resource
		eObject, err := res.Get(ctx, tName, metav1.GetOptions{})
		if err != nil {
			if len(dependencyFailures) > 0 {
				// template must be pending, do not create it
				emitErr := r.emitTemplatePending(ctx, instance, tIndex, tName, isClusterScoped,
					generatePendingMsg(dependencyFailures))
				if emitErr != nil {
					resultError = emitErr

					continue
				}

				tLogger.Info("Dependencies were not satisfied for the policy template",
					"namespace", instance.GetNamespace(),
					"kind", gvk.Kind,
				)

				continue
			}

			// not found should create it
			if k8serrors.IsNotFound(err) {
				// Handle setting the owner reference (this is skipped for clusterwide objects since our
				// namespaced policy can't own a clusterwide object)
				if !isClusterScoped {
					plcOwnerReferences := *metav1.NewControllerRef(instance, schema.GroupVersionKind{
						Group:   policiesv1.SchemeGroupVersion.Group,
						Version: policiesv1.SchemeGroupVersion.Version,
						Kind:    policiesv1.Kind,
					})

					tObjectUnstructured.SetOwnerReferences([]metav1.OwnerReference{plcOwnerReferences})
				}

				// Handle adding metadata labels
				tObjectUnstructured.SetLabels(r.setDefaultTemplateLabels(instance, tObjectUnstructured.GetLabels()))

				overrideRemediationAction(instance, tObjectUnstructured)

				eObject, err = res.Create(ctx, tObjectUnstructured, metav1.CreateOptions{})
				if err != nil {
					multiTemplateRegExp := regexp.MustCompile(
						`spec" must validate one and only one schema \(oneOf\)\. Found 2 valid alternatives$`,
					)

					if multiTemplateRegExp.MatchString(err.Error()) {
						err = fmt.Errorf(
							`ConfigurationPolicy.policy.open-cluster-management.io "%s" is invalid: `+
								`spec may only contain one of object-templates and object-templates-raw`, tName,
						)
					}

					errMsg := fmt.Sprintf("Failed to create policy template: %s", err)

					_ = r.emitTemplateError(ctx, instance, tIndex, tName, isClusterScoped, errMsg)

					tLogger.Error(resultError, "Failed to create policy template")

					// check for syntax error in policy
					if k8serrors.IsInvalid(err) {
						policyUserErrorsCounter.WithLabelValues(instance.Name, tName, "format-error").Inc()
					} else {
						policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "create-error").Inc()

						// Only requeue if the policy template is valid
						resultError = err
					}

					continue
				}

				successMsg := fmt.Sprintf("Policy template %s created successfully", tName)

				tLogger.Info("Policy template created successfully")

				// Handle clusterwide objects
				if isClusterScoped {
					addFinalizer = true

					tLogger.V(2).Info("Finalizer required for " + gvk.Kind)

					if isGkObj {
						r.setCreatedGkConstraint(true)
					}
					// The ConstraintTemplate does not generate status, so we need to generate an event for it.
					if isGkConstraintTemplate {
						tLogger.Info("Emitting status event for " + gvk.Kind)
						msg := fmt.Sprintf("%s %s was created successfully", gvk.Kind, tName)

						emitErr := r.emitTemplateSuccess(ctx, instance, tIndex, tName, isClusterScoped, msg)
						if emitErr != nil {
							resultError = emitErr
						}
					}
				}

				err = r.handleSyncSuccess(ctx, instance, tIndex, tName, successMsg, res, gvk.GroupVersion(), eObject)
				if err != nil {
					resultError = err

					tLogger.Error(resultError, "Error after creating template (will requeue)")

					policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "patch-error").Inc()
				}

				continue
			}

			// a different error getting template object from cluster
			resultError = err
			errMsg := fmt.Sprintf("Failed to get the object in the policy template: %s", err)

			_ = r.emitTemplateError(ctx, instance, tIndex, tName, isClusterScoped, errMsg)

			tLogger.Error(err, "Failed to get the object in the policy template",
				"namespace", instance.GetNamespace(),
				"kind", gvk.Kind,
			)

			policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "get-error").Inc()

			continue
		}

		if len(dependencyFailures) > 0 {
			// template must be pending, need to delete it and error
			tLogger.Info("Dependencies were not satisfied for the policy template",
				"namespace", instance.GetNamespace(),
				"kind", gvk.Kind,
			)

			emitErr := r.emitTemplatePending(ctx, instance, tIndex, tName,
				isClusterScoped, generatePendingMsg(dependencyFailures))
			if emitErr != nil {
				resultError = err
			}

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

		// Handle owner references: Owned objects should be labeled with the parent policy name, and
		// namespaced objects should have the policy for an owner reference (cluster scoped object will
		// not have this owner reference because a namespaced object can't own a cluster scoped object).
		refName := ""

		for _, ownerref := range eObject.GetOwnerReferences() {
			refName = ownerref.Name

			break // just get the first ownerReference, if there are any at all
		}

		parentPolicy := eObject.GetLabels()[utils.ParentPolicyLabel]

		// If the owner reference has been unset but the template is still managed by this policy
		// instance, recover the owner reference (skip this for cluster scoped objects)
		if !isClusterScoped && refName == "" && parentPolicy == instance.GetName() {
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

		// If there's no owner reference name, set it to the current parent policy label on the object
		if refName == "" {
			refName = parentPolicy
		}

		// Violation when object reference (or parent policy label on the object if there's no owner
		// reference) don't match the policy instance
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

			resultError = k8serrors.NewBadRequest(errMsg)

			_ = r.emitTemplateError(ctx, instance, tIndex, tName, isClusterScoped, errMsg)

			tLogger.Error(resultError, "Failed to create the policy template")

			policyUserErrorsCounter.WithLabelValues(instance.Name, tName, "format-error").Inc()

			continue
		}

		// Fill in defaults set by the ConstraintTemplate CRD to ensure the spec comparison below is correct.
		if isGkConstraintTemplate {
			err := utils.ApplyObjectDefaults(*r.Scheme, tObjectUnstructured)
			if err != nil {
				log.Error(err, "Failed to apply defaults to the ConstraintTemplate for comparison. Continuing.")
			}
		}
		// set default labels for template processing on the template object
		tObjectUnstructured.SetLabels(r.setDefaultTemplateLabels(instance, tObjectUnstructured.GetLabels()))

		overrideRemediationAction(instance, tObjectUnstructured)

		// got object, need to compare both spec and annotation and update
		eObjectUnstructured := eObject.UnstructuredContent()

		if !equivalentTemplates(eObject, tObjectUnstructured) {
			// doesn't match
			tLogger.Info("Existing object and template didn't match, will update")

			eObjectUnstructured["spec"] = tObjectUnstructured.Object["spec"]

			eObject.SetAnnotations(tObjectUnstructured.GetAnnotations())

			eObject.SetLabels(tObjectUnstructured.GetLabels())

			eObject.SetOwnerReferences(tObjectUnstructured.GetOwnerReferences())

			_, err = res.Update(ctx, eObject, metav1.UpdateOptions{})
			if err != nil {
				// If the policy template retrieved from the cache has since changed, there will be a conflict error
				// and the reconcile should be retried since this is recoverable.
				if k8serrors.IsConflict(err) {
					return reconcile.Result{}, err
				}

				errMsg := fmt.Sprintf("Failed to update policy template %s: %s", tName, err)

				_ = r.emitTemplateError(ctx, instance, tIndex, tName, isClusterScoped, errMsg)

				tLogger.Error(err, "Failed to update the policy template")

				// check for syntax error in policy
				if k8serrors.IsInvalid(err) {
					policyUserErrorsCounter.WithLabelValues(instance.Name, tName, "format-error").Inc()
				} else {
					policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "patch-error").Inc()

					// Only requeue if the policy template is valid
					resultError = err
				}

				continue
			}

			successMsg := fmt.Sprintf("Policy template %s was updated successfully", tName)

			// Handle cluster scoped objects
			if isClusterScoped {
				addFinalizer = true

				reqLogger.V(2).Info("Finalizer required for " + gvk.Kind)

				if isGkObj {
					r.setCreatedGkConstraint(true)
				}
				// The ConstraintTemplate does not generate status, so we need to generate an event for it
				if isGkConstraintTemplate {
					tLogger.Info("Emitting status event for " + gvk.Kind)
					msg := fmt.Sprintf("%s %s was updated successfully", gvk.Kind, tName)

					emitErr := r.emitTemplateSuccess(ctx, instance, tIndex, tName, isClusterScoped, msg)
					if emitErr != nil {
						resultError = emitErr
					}
				}
			}

			err = r.handleSyncSuccess(ctx, instance, tIndex, tName, successMsg, res, gvk.GroupVersion(), eObject)
			if err != nil {
				resultError = err
				tLogger.Error(resultError, "Error after updating template (will requeue)")

				policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "patch-error").Inc()
			}

			tLogger.Info("Existing object has been updated")
		} else {
			err = r.handleSyncSuccess(ctx, instance, tIndex, tName, "", res, gvk.GroupVersion(), eObject)
			if err != nil {
				resultError = err
				tLogger.Error(resultError, "Error after confirming template matches (will requeue)")

				policySystemErrorsCounter.WithLabelValues(instance.Name, tName, "patch-error").Inc()
			}

			tLogger.V(1).Info("Existing object matches the policy template")
		}

		if isClusterScoped {
			// If we got to this point, the reconcile succeeded and a finalizer would be required for
			// existing clusterwide objects
			addFinalizer = true

			if isGkObj {
				r.setCreatedGkConstraint(true)
			}
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

		if k8serrors.IsNotFound(err) {
			reqLogger.Error(resultError, "Error updating dependency watcher, likely due to a missing CRD.")
			policyUserErrorsCounter.WithLabelValues(instance.Name, "", "crd-error").Inc()
		} else {
			reqLogger.Error(resultError, "Error updating dependency watcher")
			policySystemErrorsCounter.WithLabelValues(instance.Name, "", "client-error").Inc()
		}
	}

	err = r.cleanUpExcessTemplates(ctx, dClient, *instance, templateNames)
	if err != nil {
		resultError = err
		reqLogger.Error(resultError, "Error cleaning up templates")
	}

	// Namespaced objects can't own clusterwide objects, so we'll add a finalizer to the policy if
	// objects were created so that we can handle cleanup before deleting the policy
	if !hasClusterwideFinalizer(instance) {
		if addFinalizer {
			reqLogger.Info("Adding finalizer to handle clusterwide object cleanup")

			instance.Finalizers = append(instance.Finalizers, utils.ClusterwideFinalizer)

			err = r.Update(ctx, instance)
			if err != nil {
				resultError = err
				reqLogger.Error(err, "Failed to update policy when adding finalizers")
			}
		}
	} else if !addFinalizer {
		reqLogger.Info("Cleanup not required--removing clusterwide cleanup finalizer")
		removeFinalizer(instance, utils.ClusterwideFinalizer)

		err = r.Update(ctx, instance)
		if err != nil {
			resultError = err
			reqLogger.Error(err, "Failed to update policy when removing finalizers")
		}
	}

	reqLogger.V(2).Info("Completed the reconciliation")

	return reconcile.Result{}, resultError
}

// equivalentTemplates determines whether the template existing on the cluster and the policy template are the same.
// Any missing defaults this function is aware of will be set on tObject.
func equivalentTemplates(eObject *unstructured.Unstructured, tObject *unstructured.Unstructured) bool {
	if tObject.GetKind() == "ConfigurationPolicy" {
		pruneObjectBehavior, _, _ := unstructured.NestedString(tObject.Object, "spec", "pruneObjectBehavior")
		if pruneObjectBehavior == "" {
			err := unstructured.SetNestedField(tObject.Object, "None", "spec", "pruneObjectBehavior")
			if err != nil {
				log.Error(err, "Failed to set the default value of pruneObjectBehavior for")
			}
		}
	}

	if !equality.Semantic.DeepEqual(eObject.UnstructuredContent()["spec"], tObject.Object["spec"]) {
		return false
	}

	if !equality.Semantic.DeepEqual(eObject.GetAnnotations(), tObject.GetAnnotations()) {
		return false
	}

	if !equality.Semantic.DeepEqual(eObject.GetLabels(), tObject.GetLabels()) {
		return false
	}

	if !equality.Semantic.DeepEqual(eObject.GetOwnerReferences(), tObject.GetOwnerReferences()) {
		return false
	}

	return true
}

// setDefaultTemplateLabels ensures the template contains all necessary labels for processing
func (r *PolicyReconciler) setDefaultTemplateLabels(instance *policiesv1.Policy,
	labels map[string]string,
) map[string]string {
	if labels == nil {
		labels = map[string]string{}
	}

	desiredLabels := map[string]string{
		utils.ParentPolicyLabel:      instance.GetName(),
		"cluster-name":               instance.GetLabels()[common.ClusterNameLabel],
		common.ClusterNameLabel:      instance.GetLabels()[common.ClusterNameLabel],
		"cluster-namespace":          r.ClusterNamespace,
		common.ClusterNamespaceLabel: r.ClusterNamespace,
	}

	for key, label := range desiredLabels {
		labels[key] = label
	}

	return labels
}

// cleanUpExcessTemplates compares existing policy templates on the cluster to those contained in the policy,
// and deletes those that have been renamed or removed from the parent policy
func (r *PolicyReconciler) cleanUpExcessTemplates(
	ctx context.Context,
	dClient dynamic.Interface,
	instance policiesv1.Policy,
	templateNames []string,
) error {
	var errorList utils.ErrList

	reqLogger := log.WithValues("Request.Namespace", instance.Namespace, "Request.Name", instance.Name)

	// GVR with scope specified
	type gvrScoped struct {
		gvr        schema.GroupVersionResource
		namespaced bool
	}

	tmplGVRs := []gvrScoped{}

	// Query for Constraints if one was already synced successfully or the boolean is not yet set
	if r.createdGkConstraint == nil || *r.createdGkConstraint {
		// Query for ConstraintTemplates and collect the GroupVersionResource for each Constraint
		gkConstraintTemplateListv1 := gktemplatesv1.ConstraintTemplateList{}

		err := r.List(ctx, &gkConstraintTemplateListv1, &client.ListOptions{})
		if err == nil {
			// Add the ConstraintTemplate to the GVR list
			if len(gkConstraintTemplateListv1.Items) > 0 {
				tmplGVRs = append(tmplGVRs, gvrScoped{
					gvr: schema.GroupVersionResource{
						Group:    utils.GvkConstraintTemplate.Group,
						Resource: "constrainttemplates",
						Version:  "v1",
					},
					namespaced: false,
				})
			} else {
				// No ConstraintTemplates found -- reset boolean
				r.setCreatedGkConstraint(false)
			}
			// Iterate over the ConstraintTemplates to gather the Constraints on the cluster
			for _, gkCT := range gkConstraintTemplateListv1.Items {
				gkCT := gkCT

				tmplGVRs = append(tmplGVRs, gvrScoped{
					gvr: schema.GroupVersionResource{
						Group:    utils.GConstraint,
						Resource: strings.ToLower(gkCT.Spec.CRD.Spec.Names.Kind),
						Version:  "v1beta1",
					},
					namespaced: false,
				})
			}
		} else if errors.As(err, &errResourceDiscoveryFailed) {
			// If there's no v1 ConstraintTemplate, try the v1beta1 version
			gkConstraintTemplateListv1beta1 := gktemplatesv1beta1.ConstraintTemplateList{}
			err := r.List(ctx, &gkConstraintTemplateListv1beta1, &client.ListOptions{})
			if err == nil {
				// Add the ConstraintTemplate to the GVR list
				if len(gkConstraintTemplateListv1.Items) > 0 {
					tmplGVRs = append(tmplGVRs, gvrScoped{
						gvr: schema.GroupVersionResource{
							Group:    utils.GvkConstraintTemplate.Group,
							Resource: "constrainttemplates",
							Version:  "v1beta1",
						},
						namespaced: false,
					})
				} else {
					// No ConstraintTemplates found -- reset boolean
					r.setCreatedGkConstraint(false)
				}
				// Iterate over the ConstraintTemplates to gather the Constraints on the cluster
				for _, gkCT := range gkConstraintTemplateListv1beta1.Items {
					gkCT := gkCT

					tmplGVRs = append(tmplGVRs, gvrScoped{
						gvr: schema.GroupVersionResource{
							Group:    utils.GConstraint,
							Resource: strings.ToLower(gkCT.Spec.CRD.Spec.Names.Kind),
							Version:  "v1beta1",
						},
						namespaced: false,
					})
				}
				// Log and ignore other errors to allow cleanup to continue since Gatekeeper may not be installed
			} else if errors.As(err, &errResourceDiscoveryFailed) {
				reqLogger.Info("The ConstraintTemplate CRD is not installed")
				r.setCreatedGkConstraint(false)
			} else {
				reqLogger.Info(fmt.Sprintf("Ignoring ConstraintTemplate cleanup error: %s", err.Error()))
			}
		} else {
			reqLogger.Info(fmt.Sprintf("Ignoring ConstraintTemplate cleanup error: %s", err.Error()))
		}
	}

	// Query for CRDs with policy-type label
	crdQuery := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{utils.PolicyTypeLabel: "template"}),
	}

	// Build list of GVRs for objects to check the parent label on, falling back to v1beta1
	crdsv1 := extensionsv1.CustomResourceDefinitionList{}

	err := r.List(ctx, &crdsv1, &crdQuery)
	if err == nil {
		for _, crd := range crdsv1.Items {
			crd := crd

			if len(crd.Spec.Versions) > 0 {
				tmplGVRs = append(tmplGVRs, gvrScoped{
					gvr: schema.GroupVersionResource{
						Group:    crd.Spec.Group,
						Resource: crd.Spec.Names.Plural,
						Version:  crd.Spec.Versions[0].Name,
					},
					namespaced: crd.Spec.Scope == extensionsv1.NamespaceScoped,
				})
			}
		}
	} else if errors.As(err, &errResourceDiscoveryFailed) {
		crdsv1beta1 := extensionsv1beta1.CustomResourceDefinitionList{}
		err := r.List(ctx, &crdsv1beta1, &crdQuery)
		if err != nil {
			return fmt.Errorf("error listing v1beta1 CRDs with query %+v: %w", crdQuery, err)
		}
		for _, crd := range crdsv1beta1.Items {
			crd := crd

			if len(crd.Spec.Versions) > 0 {
				tmplGVRs = append(tmplGVRs, gvrScoped{
					gvr: schema.GroupVersionResource{
						Group:    crd.Spec.Group,
						Resource: crd.Spec.Names.Plural,
						Version:  crd.Spec.Versions[0].Name,
					},
					namespaced: crd.Spec.Scope == extensionsv1beta1.NamespaceScoped,
				})
			}
		}
	} else {
		return fmt.Errorf("error listing v1 CRDs with query %+v: %w", crdQuery, err)
	}

	for _, gvrScoped := range tmplGVRs {
		gvrScoped := gvrScoped

		// Instantiate a dynamic client for the GVR
		resourceNs := ""
		if gvrScoped.namespaced {
			resourceNs = r.ClusterNamespace
		}

		resClient := dClient.Resource(gvrScoped.gvr).Namespace(resourceNs)

		// Iterate through all objects with parent label set to see if they
		// match the templates in the policy
		children, err := resClient.List(ctx, metav1.ListOptions{
			LabelSelector: utils.ParentPolicyLabel + "=" + instance.GetName(),
		})
		if err != nil {
			errorList = append(errorList,
				fmt.Errorf("error listing %s objects: %w", gvrScoped.gvr.String(), err))

			continue
		}

		for _, tmpl := range children.Items {
			tmpl := tmpl

			// delete all templates with policy label that aren't still in the policy
			found := false

			for _, parentTmplName := range templateNames {
				if parentTmplName == tmpl.GetName() {
					found = true

					break
				}
			}

			if !found {
				err := resClient.Delete(ctx, tmpl.GetName(), metav1.DeleteOptions{})
				if err != nil {
					errorList = append(errorList,
						fmt.Errorf("error deleting %s object %s: %w", gvrScoped.gvr.String(), tmpl.GetName(), err))
				} else {
					tmplAnnotations := tmpl.GetAnnotations()

					if r.EventsQueue != nil && tmplAnnotations[utils.PolicyDBIDAnnotation] != "" {
						ce, err := utils.GenerateDisabledEvent(
							&instance, &tmpl, "The policy was removed from the parent policy",
						)
						if err != nil {
							log.Error(err, "Failed to generate a disabled compliance API event")
						} else {
							r.EventsQueue.Add(ce)
						}
					}
				}
			}
		}
	}

	return errorList.Aggregate()
}

const (
	DepFailNoAPIMapping    = "Could not find an API mapping for the dependency"
	DepFailObjNotFound     = "Dependency object not found"
	DepFailGet             = "Failed to get the dependency object"
	DepFailCompNotFound    = "Failed to find complianceState on the dependency object"
	DepFailWrongCompliance = "Compliance mismatch on the dependency object"
)

// processDependencies iterates through all dependencies of a template and returns an map of
// unmet dependencies to the reason that dependency was not satisfied.
func (r *PolicyReconciler) processDependencies(
	ctx context.Context,
	dClient dynamic.Interface,
	discoveryClient discovery.DiscoveryInterface,
	templateDeps map[depclient.ObjectIdentifier]string,
	tLogger logr.Logger,
) map[depclient.ObjectIdentifier]string {
	dependencyFailures := make(map[depclient.ObjectIdentifier]string)

	for dep := range templateDeps {
		rsrc, namespaced, err := utils.GVRFromGVK(discoveryClient, dep.GroupVersionKind())
		if err != nil {
			dependencyFailures[dep] = DepFailNoAPIMapping
			tLogger.Error(err, dependencyFailures[dep], "object", dep)

			continue
		}

		var res dynamic.ResourceInterface

		if namespaced {
			ns := dep.Namespace
			if ns == "" && dep.Group == policiesv1.GroupVersion.Group {
				// ocm policies should always be in the cluster namespace
				ns = r.ClusterNamespace
			}

			res = dClient.Resource(rsrc).Namespace(ns)
		} else {
			res = dClient.Resource(rsrc)
		}

		depObj, err := res.Get(ctx, dep.Name, metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			if dep.Group == utils.GvkConstraintTemplate.Group && templateDeps[dep] != "Compliant" {
				tLogger.V(1).Info("ConstraintTemplate 'NonCompliant' dependency satisfied", "object", dep)

				continue
			}

			dependencyFailures[dep] = DepFailObjNotFound

			tLogger.V(1).Info("Dependency not satisfied", "reason", DepFailObjNotFound, "object", dep)

			continue
		} else if err != nil {
			dependencyFailures[dep] = DepFailGet

			tLogger.Error(err, DepFailGet, "object", dep)

			continue
		}

		switch dep.Group {
		case utils.GvkConstraintTemplate.Group:
			if templateDeps[dep] != "Compliant" {
				// The ConstraintTemplate was found, but the policy wants it to not be found
				dependencyFailures[dep] = DepFailWrongCompliance
			}
		case utils.GConstraint:
			violations, found, err := unstructured.NestedInt64(depObj.Object, "status", "totalViolations")
			if err != nil || !found {
				// Note that not finding the field is *not* considered "Compliant"
				dependencyFailures[dep] = DepFailCompNotFound
			} else if (violations == 0) != (templateDeps[dep] == "Compliant") {
				dependencyFailures[dep] = DepFailWrongCompliance
			}
		default:
			depCompliance, found, err := unstructured.NestedString(depObj.Object, "status", "compliant")
			if err != nil || !found {
				dependencyFailures[dep] = DepFailCompNotFound
			} else if depCompliance != templateDeps[dep] {
				dependencyFailures[dep] = DepFailWrongCompliance
			}
		}

		if reason, failed := dependencyFailures[dep]; failed {
			tLogger.V(1).Info("Dependency not satisfied", "reason", reason, "object", dep)
		} else {
			tLogger.V(1).Info("Dependency satisfied", "object", dep)
		}
	}

	return dependencyFailures
}

// generatePendingMsg formats the list of failed dependencies into a readable error.
// Example: `Dependencies were not satisfied: 1 is still pending (FooPolicy foo)`
func generatePendingMsg(dependencyFailures map[depclient.ObjectIdentifier]string) string {
	names := make([]string, 0, len(dependencyFailures))
	for dep := range dependencyFailures {
		names = append(names, fmt.Sprintf("%s %s", dep.Kind, dep.Name))
	}

	sort.Strings(names)

	nameStr := strings.Join(names, ", ")

	fmtStr := "Dependencies were not satisfied: %d are still pending (%s)"
	if len(dependencyFailures) == 1 {
		fmtStr = "Dependencies were not satisfied: %d is still pending (%s)"
	}

	return fmt.Sprintf(fmtStr, len(dependencyFailures), nameStr)
}

func overrideRemediationAction(instance *policiesv1.Policy, tObjectUnstructured *unstructured.Unstructured) {
	// override RemediationAction only when it is set on parent
	// or when a policy is set to informonly
	if tObjectUnstructured.GroupVersionKind().Group == utils.GConstraint {
		var enforcementAction string

		switch strings.ToLower(string(instance.Spec.RemediationAction)) {
		case strings.ToLower(string(policiesv1.Inform)):
			enforcementAction = "warn"
		case strings.ToLower(string(policiesv1.Enforce)):
			enforcementAction = "deny"
		default:
			return
		}

		if spec, ok := tObjectUnstructured.Object["spec"]; ok {
			if specObject, ok := spec.(map[string]interface{}); ok {
				specObject["enforcementAction"] = enforcementAction
			}
		}

		return
	} else if tObjectUnstructured.GroupVersionKind().Group == utils.GvkConstraintTemplate.Group {
		// Don't override anything if it's a ConstraintTemplate
		return
	}

	spec, ok := tObjectUnstructured.Object["spec"]
	if !ok {
		return
	}

	specObject, ok := spec.(map[string]interface{})
	if !ok {
		return
	}

	if remediationAction, ok := specObject["remediationAction"]; ok {
		if _, ok := remediationAction.(string); ok {
			if strings.EqualFold(specObject["remediationAction"].(string), "informonly") {
				specObject["remediationAction"] = strings.ToLower(string(policiesv1.Inform))

				return
			}
		}
	}

	if instance.Spec.RemediationAction != "" {
		specObject["remediationAction"] = string(instance.Spec.RemediationAction)
	}
}

// emitTemplateSuccess performs actions that ensure correct reporting of template success in the
// policy framework. If the policy's status already reflects the current message, then no actions
// are taken.
func (r *PolicyReconciler) emitTemplateSuccess(
	ctx context.Context, pol *policiesv1.Policy, tIndex int, tName string, clusterScoped bool, msg string,
) error {
	err := r.emitTemplateEvent(ctx, pol, tIndex, tName, clusterScoped, "Normal", policiesv1.Compliant, msg)
	if err != nil {
		tlog := log.WithValues("Policy.Namespace", pol.Namespace, "Policy.Name", pol.Name, "template", tName)
		tlog.Error(err, "Failed to emit template success event")
	}

	return err
}

// emitTemplateError performs actions that ensure correct reporting of template errors in the
// policy framework. If the policy's status already reflects the current error, then no actions
// are taken.
func (r *PolicyReconciler) emitTemplateError(
	ctx context.Context, pol *policiesv1.Policy, tIndex int, tName string, clusterScoped bool, errMsg string,
) error {
	err := r.emitTemplateEvent(ctx, pol, tIndex, tName, clusterScoped,
		"Warning", policiesv1.NonCompliant, "template-error; "+errMsg)
	if err != nil {
		tlog := log.WithValues("Policy.Namespace", pol.Namespace, "Policy.Name", pol.Name, "template", tName)
		tlog.Error(err, "Failed to emit template error event")
	}

	return err
}

// emitTemplatePending performs actions that ensure correct reporting of pending dependencies in the
// policy framework. If the policy's status already reflects the current status, then no actions
// are taken.
func (r *PolicyReconciler) emitTemplatePending(
	ctx context.Context, pol *policiesv1.Policy, tIndex int, tName string, clusterScoped bool, msg string,
) error {
	compliance := policiesv1.Pending
	eventType := "Warning"

	if pol.Spec.PolicyTemplates[tIndex].IgnorePending {
		compliance = policiesv1.Compliant
		msg += " but ignorePending is true"
		eventType = "Normal"
	}

	err := r.emitTemplateEvent(ctx, pol, tIndex, tName, clusterScoped, eventType, compliance, msg)
	if err != nil {
		tlog := log.WithValues("Policy.Namespace", pol.Namespace, "Policy.Name", pol.Name, "template", tName)
		tlog.Error(err, "Failed to emit template pending event")
	}

	return err
}

// emitTemplateEvent performs actions that ensure correct reporting of template sync events. If the
// policy's status already reflects the current status, then no actions are taken.
func (r *PolicyReconciler) emitTemplateEvent(
	ctx context.Context, pol *policiesv1.Policy, tIndex int, tName string, clusterScoped bool,
	eventType string, compliance policiesv1.ComplianceState, msg string,
) error {
	// check if the error is already present in the policy status - if so, return early
	if strings.Contains(getLatestStatusMessage(pol, tIndex), string(compliance)+"; "+msg) {
		return nil
	}

	// emit an informational event
	r.Recorder.Event(pol, eventType, "PolicyTemplateSync", msg)

	// emit the compliance event
	var policyComplianceReason string
	if clusterScoped {
		policyComplianceReason = utils.EventReason("", tName)
	} else {
		policyComplianceReason = utils.EventReason(pol.GetNamespace(), tName)
	}

	sender := utils.ComplianceEventSender{
		ClusterNamespace: pol.Namespace,
		InstanceName:     r.InstanceName,
		ClientSet:        r.Clientset,
		ControllerName:   ControllerName,
	}

	ownerref := metav1.OwnerReference{
		APIVersion: pol.APIVersion,
		Kind:       pol.Kind,
		Name:       pol.Name,
		UID:        pol.UID,
	}

	var instance client.Object

	instanceUnstructured := &unstructured.Unstructured{}

	err := instanceUnstructured.UnmarshalJSON(pol.Spec.PolicyTemplates[tIndex].ObjectDefinition.Raw)
	if err == nil {
		if pol.Annotations[utils.ParentDBIDAnnotation] != "" {
			annotations := instanceUnstructured.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}

			annotations[utils.ParentDBIDAnnotation] = pol.Annotations[utils.ParentDBIDAnnotation]

			instanceUnstructured.SetAnnotations(annotations)
		}

		instance = instanceUnstructured
	}

	return sender.SendEvent(ctx, instance, ownerref, policyComplianceReason, msg, compliance)
}

// handleSyncSuccess performs common actions that should be run whenever a template is in sync,
// whether there were changes or not. If no changes occurred, an empty message should be passed in.
// The template object's `status.compliant` field (complianceState) will be reset if the policy
// controller needs to create a new compliance event - for example after a template-error is
// resolved, or when the policy is no longer Pending.
func (r *PolicyReconciler) handleSyncSuccess(
	ctx context.Context,
	pol *policiesv1.Policy,
	tIndex int,
	tName string,
	msg string,
	resInt dynamic.ResourceInterface,
	gv schema.GroupVersion,
	template *unstructured.Unstructured,
) error {
	if msg != "" {
		r.Recorder.Event(pol, "Normal", "PolicyTemplateSync", msg)
	}

	if gv.Group != policiesv1.GroupVersion.Group {
		// Skip if this isn't an OCM policy
		return nil
	}

	latestMessage := getLatestStatusMessage(pol, tIndex)
	if !(strings.Contains(latestMessage, "template-error;") || strings.Contains(latestMessage, "Pending;")) {
		// A status reset isn't necessary when the last status is a 'normal' compliant or noncompliant state.
		return nil
	}

	// Don't need to check the error because if it's invalid or not set, an empty string is returned. In this case, we
	// don't want to remove the field value.
	compliantStatus, _, _ := unstructured.NestedString(template.Object, "status", "compliant")
	if compliantStatus == "" {
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

// hasPolicyTemplateLabel queries the CRD for a given GroupVersionResource and returns whether it
// has the policy-type=template label and an error if the CRD could not be retrieved.
func (r *PolicyReconciler) hasPolicyTemplateLabel(
	ctx context.Context, rsrc schema.GroupVersionResource,
) (bool, error) {
	crd := extensionsv1.CustomResourceDefinition{}
	crdName := types.NamespacedName{
		Name: rsrc.GroupResource().String(),
	}

	err := r.Get(ctx, crdName, &crd)
	if err == nil {
		return crd.GetLabels()[utils.PolicyTypeLabel] == "template", nil
	} else if errors.As(err, &errResourceDiscoveryFailed) {
		betaCrd := extensionsv1beta1.CustomResourceDefinition{}

		err = r.Get(ctx, crdName, &betaCrd)
		if err == nil {
			return betaCrd.GetLabels()[utils.PolicyTypeLabel] == "template", nil
		}
	}

	// If it wasn't found, then it wasn't in the cache and doesn't have the label
	if k8serrors.IsNotFound(err) {
		return false, nil
	}

	return false, err
}

// hasClusterwideFinalizer returns a boolean for whether a policy has a clusterwide finalizer,
// signaling that the controller needs to handle manual cleanup of clusterwide objects.
func hasClusterwideFinalizer(pol *policiesv1.Policy) bool {
	for _, finalizer := range pol.Finalizers {
		if finalizer == utils.ClusterwideFinalizer {
			return true
		}
	}

	return false
}

// removeFinalizer iterates over the finalizers and removes the finalizer specified from the policy.
func removeFinalizer(pol *policiesv1.Policy, finalizer string) {
	i := 0
	finalizersLength := len(pol.Finalizers)

	for i < finalizersLength {
		if pol.Finalizers[i] == finalizer {
			pol.Finalizers = append(pol.Finalizers[:i], pol.Finalizers[i+1:]...)
			finalizersLength--
		} else {
			i++
		}
	}
}

// finalizerCleanup handles any steps required for cleaning up objects on the cluster prior to
// removing the policy finalizer.
func finalizerCleanup(
	ctx context.Context,
	pol *policiesv1.Policy,
	discoveryClient discovery.DiscoveryInterface,
	dClient dynamic.Interface,
) error {
	var errorList utils.ErrList

	for _, policyT := range pol.Spec.PolicyTemplates {
		object, gvk, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			// Ignore the error if the deployment is uninstalling, because in this situation the controller
			// should only be concerned with the valid templates.
			if !uninstall.DeploymentIsUninstalling {
				errorList = append(errorList, fmt.Errorf("failed to decode policy template with error: %w", err))
			}

			policyUserErrorsCounter.WithLabelValues(pol.Name, "", "format-error").Inc()

			continue
		}

		// Skip template if the name isn't found
		var tName string
		if tMetaObj, ok := object.(metav1.Object); ok {
			tName = tMetaObj.GetName()
		} else {
			continue
		}

		// If there was an error getting the mapping, it's likely because the CRD is gone--skip this
		// template since Kubernetes garbage collection will take care of it
		rsrc, namespaced, err := utils.GVRFromGVK(discoveryClient, *gvk)
		if errors.Is(err, utils.ErrNoVersionedResource) {
			continue
		} else if err != nil {
			policySystemErrorsCounter.WithLabelValues(pol.Name, tName, "get-error").Inc()
			errorList = append(errorList, err)

			continue
		}

		// Delete clusterwide objects
		if !namespaced {
			// Delete object, ignoring not found errors
			err := dClient.Resource(rsrc).Delete(ctx, tName, metav1.DeleteOptions{})
			if err != nil && !k8serrors.IsNotFound(err) {
				policySystemErrorsCounter.WithLabelValues(pol.Name, tName, "delete-error").Inc()

				errorList = append(errorList, fmt.Errorf("failed to delete "+gvk.Kind+" with error: %w", err))
			}
		}
	}

	return errorList.Aggregate()
}

func (r *PolicyReconciler) setCreatedGkConstraint(b bool) {
	r.createdGkConstraint = &b
}

// Check duplicate names in policy-templates(configurationPolicies)
func hasDupName(pol *policiesv1.Policy) bool {
	templates := pol.Spec.PolicyTemplates

	foundNames := make(map[string]struct{})

	for _, v := range templates {
		unstructured, err := unmarshalFromJSON(v.ObjectDefinition.Raw)
		if err != nil {
			// Skip unmarshal error here, template error should appear later
			return false
		}

		name := unstructured.GetName()

		if _, has := foundNames[name]; has {
			return true
		}

		foundNames[name] = struct{}{}
	}

	return false
}
