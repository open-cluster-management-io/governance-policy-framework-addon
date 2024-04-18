// Copyright Contributors to the Open Cluster Management project

package gatekeepersync

import (
	"context"
	// #nosec G505
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	depclient "github.com/stolostron/kubernetes-dependency-watches/client"
	admissionregistration "k8s.io/api/admissionregistration/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/uninstall"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

const (
	ControllerName        = "gatekeeper-constraint-status-sync"
	GatekeeperWebhookName = "gatekeeper-validating-webhook-configuration"
)

var (
	log    = logf.Log.WithName(ControllerName)
	crdGVK = schema.GroupVersionKind{
		Group:   "apiextensions.k8s.io",
		Version: "v1",
		Kind:    "CustomResourceDefinition",
	}
)

// SetupWithManager sets up the controller with the Manager.
func (r *GatekeeperConstraintReconciler) SetupWithManager(mgr ctrl.Manager, constraintEvents *source.Channel) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&policyv1.Policy{}).
		WithEventFilter(policyPredicates()).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.ConcurrentReconciles}).
		WatchesRawSource(constraintEvents, &handler.EnqueueRequestForObject{}).
		Named(ControllerName).
		Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &GatekeeperConstraintReconciler{}

// Used to track sent messages for a particular Gatekeeper constraint.
type policyKindName struct {
	Policy string
	Kind   string
	Name   string
}

// GatekeeperConstraintReconciler is responsible for relaying Gatekeeper constraint audit results as policy status
// events.
type GatekeeperConstraintReconciler struct {
	client.Client
	utils.ComplianceEventSender
	Scheme             *runtime.Scheme
	ConstraintsWatcher depclient.DynamicWatcher
	// A cache of sent messages to avoid repeating status events due to race conditions. Each value is a SHA1
	// digest.
	lastSentMessages     sync.Map
	ConcurrentReconciles int
}

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch
//+kubebuilder:rbac:groups=constraints.gatekeeper.sh,resources=*,verbs=create;get;list;watch
//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,resourceNames=gatekeeper-validating-webhook-configuration,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;delete;get;list;patch;update;watch

// Reconcile handles Policy objects that contain a Gatekeeper constraint and relays status messages from Gatekeeper
// audit results. Every time a Gatekeeper constraint in a Policy is updated, a reconcile on the Policy is triggered.
func (r *GatekeeperConstraintReconciler) Reconcile(
	ctx context.Context, request reconcile.Request,
) (
	reconcile.Result, error,
) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	if uninstall.DeploymentIsUninstalling {
		log.Info("Skipping reconcile because the deployment is in uninstallation mode")

		return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	log.V(1).Info("Reconciling a Policy with one or more Gatekeeper constraints")

	policyObjID := depclient.ObjectIdentifier{
		Group:     policyv1.GroupVersion.Group,
		Version:   policyv1.GroupVersion.Version,
		Kind:      "Policy",
		Namespace: request.Namespace,
		Name:      request.Name,
	}
	policy := &policyv1.Policy{}

	err := r.Get(ctx, request.NamespacedName, policy)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("The Policy was deleted. Cleaning up watchers and status message cache.")

			r.lastSentMessages.Range(func(key, value any) bool {
				keyTyped := key.(policyKindName)
				if keyTyped.Policy == request.Name {
					r.lastSentMessages.Delete(keyTyped)
				}

				return true
			})

			err := r.ConstraintsWatcher.RemoveWatcher(policyObjID)
			if errors.Is(err, depclient.ErrInvalidInput) {
				log.Error(err, "Could not construct a valid object identifier for the policy. Will not retry.")

				return reconcile.Result{}, nil
			}

			return reconcile.Result{}, err
		}

		log.Error(err, "Failed to get the Policy from the cache. Will retry the reconcile request.")

		return reconcile.Result{}, err
	}

	// Start query batch for caching and watching related objects
	err = r.ConstraintsWatcher.StartQueryBatch(policyObjID)
	if err != nil {
		log.Error(err, "Could not start query batch for the watcher", "objectID", policyObjID)

		return reconcile.Result{}, err
	}

	defer func() {
		err := r.ConstraintsWatcher.EndQueryBatch(policyObjID)
		if err != nil {
			log.Error(err, "Could not end query batch for the watcher", "objectID", policyObjID)
		}
	}()

	constraintsSet := map[policyKindName]bool{}

	for templateIndex, template := range policy.Spec.PolicyTemplates {
		templateMap := map[string]interface{}{}

		err := json.Unmarshal(template.ObjectDefinition.Raw, &templateMap)
		if err != nil {
			log.Error(
				err,
				"The policy template is invalid. Skipping this policy template.",
				"policyTemplateIndex", fmt.Sprintf("%d", templateIndex),
			)

			continue
		}

		templateUnstructured := unstructured.Unstructured{Object: templateMap}
		templateGVK := templateUnstructured.GroupVersionKind()

		if templateGVK.Group != utils.GConstraint {
			continue
		}

		constraintName := templateUnstructured.GetName()

		pkn := policyKindName{Policy: policy.Name, Kind: templateGVK.Kind, Name: constraintName}
		constraintsSet[pkn] = true

		constraintGVR := schema.GroupVersionResource{
			Group: utils.GConstraint,
			// https://github.com/open-policy-agent/frameworks/blob/v0.9.0/constraint/pkg/client/crds/crds.go#L34
			Resource: strings.ToLower(templateGVK.Kind),
			// This uses v1beta1 even if the constraint in the template may have a newer version in the future so that
			// the schema is consistent for status parsing. At the time of this writing, v1beta1 is the latest.
			Version: "v1beta1",
		}

		crdName := fmt.Sprintf("%s.%s", constraintGVR.Resource, constraintGVR.Group)

		// Getting the CRD first creates a watch so that if the constraint template gets cleaned up and thus the
		// CRD is deleted, the watch on the constraint can get cleaned up.
		crd, err := r.ConstraintsWatcher.Get(policyObjID, crdGVK, "", crdName)
		if err != nil {
			log.Error(err, "Failed to create a watch for the constraint CRD", "name", crdName)

			return reconcile.Result{}, err
		}

		if crd == nil {
			log.Info(
				"The Gatekeeper ConstraintTemplate is not initialized on the cluster yet. "+
					"Will retry the reconcile when the CRD is created.",
				"constraint", constraintName,
			)

			continue
		}

		constraint, err := r.ConstraintsWatcher.Get(
			policyObjID, templateGVK, templateUnstructured.GetNamespace(), constraintName,
		)
		if err != nil {
			log.Error(
				err, "Failed to get the constraint. Will retry the reconcile request.", "constraint", constraintName,
			)

			return reconcile.Result{}, err
		}

		if constraint == nil {
			log.Info(
				"The Gatekeeper constraint does not exist on the cluster yet. Will retry the reconcile request once "+
					"it's created.",
				"constraint", constraintName,
			)

			continue
		}

		_, auditRan, _ := unstructured.NestedInt64(constraint.Object, "status", "totalViolations")
		if !auditRan {
			log.V(1).Info("The constraint audit results haven't yet been posted. Skipping status update.")

			continue
		}

		violations, _, err := unstructured.NestedSlice(constraint.Object, "status", "violations")
		if err != nil {
			log.Error(err, "The constraint status is invalid", "constraint", constraintName)

			err := r.sendComplianceEvent(
				ctx, policy, constraint, templateIndex, "The constraint status is invalid", policyv1.NonCompliant,
			)
			if err != nil {
				log.Error(err, "Failed to send the compliance event")

				return reconcile.Result{}, err
			}

			continue
		}

		if len(violations) == 0 {
			err := r.sendComplianceEvent(
				ctx, policy, constraint, templateIndex, "The constraint has no violations", policyv1.Compliant,
			)
			if err != nil {
				log.Error(err, "Failed to send the compliance event")

				return reconcile.Result{}, err
			}

			continue
		}

		msg := ""

		for _, violation := range violations {
			violation, ok := violation.(map[string]interface{})
			if !ok {
				log.Info(
					"The Gatekeeper constraint's status.violations field is in an invalid format. Skipping for now.",
				)

				continue
			}

			if msg != "" {
				msg += "; "
			}

			var name string
			if ns, ok := violation["namespace"]; ok {
				name = ns.(string) + "/" + violation["name"].(string)
			} else {
				name = violation["name"].(string)
			}

			msg += fmt.Sprintf(
				"%s - %s (on %s %s)",
				violation["enforcementAction"],
				violation["message"],
				violation["kind"],
				name,
			)
		}

		err = r.sendComplianceEvent(ctx, policy, constraint, templateIndex, msg, policyv1.NonCompliant)
		if err != nil {
			log.Error(err, "Failed to send the compliance event")

			return reconcile.Result{}, err
		}
	}

	// Clear the status message cache for any removed constraints in the policy since the last reconcile
	r.lastSentMessages.Range(func(key, value any) bool {
		keyTyped := key.(policyKindName)
		if keyTyped.Policy == policy.Name && !constraintsSet[keyTyped] {
			r.lastSentMessages.Delete(keyTyped)
		}

		return true
	})

	return reconcile.Result{}, nil
}

// sendComplianceEvent wraps SendComplianceEvent and only sends an event if it isn't already set in the policy.
// Additionally, it adjust the compliance and message if the validating webhook is disabled and the constraint's
// enforcementAction is set to deny.
func (r *GatekeeperConstraintReconciler) sendComplianceEvent(
	ctx context.Context,
	policy *policyv1.Policy,
	constraint *unstructured.Unstructured,
	templateIndex int,
	msg string,
	compliance policyv1.ComplianceState,
) error {
	enforcementAction, err := getEnforcementAction(constraint)
	if err != nil {
		log.Error(err, "The enforcementAction is invalid. Assuming it's not deny.")
	}

	if enforcementAction == "deny" {
		webhookEnabled, err := r.webhookEnabled(ctx)
		if err != nil {
			log.Error(err, "Failed to determine if the Gatekeeper webhook is enabled")

			return err
		}

		if !webhookEnabled {
			compliance = policyv1.NonCompliant
			msg = fmt.Sprintf(
				"The Gatekeeper validating webhook is disabled but the constraint's spec.enforcementAction is %s. %s",
				enforcementAction,
				msg,
			)
		}
	}

	refreshedPolicy := &policyv1.Policy{}

	err = r.Get(ctx, types.NamespacedName{Namespace: policy.Namespace, Name: policy.Name}, refreshedPolicy)
	if err != nil {
		log.Error(err, "Failed to refresh the cached policy. Will use potentially stale policy for history comparison.")

		refreshedPolicy = policy
	}

	owner := metav1.OwnerReference{
		APIVersion: refreshedPolicy.APIVersion,
		Kind:       refreshedPolicy.Kind,
		Name:       refreshedPolicy.Name,
		UID:        refreshedPolicy.UID,
	}
	kn := policyKindName{Policy: policy.Name, Kind: constraint.GetKind(), Name: constraint.GetName()}

	if len(refreshedPolicy.Status.Details) < templateIndex+1 ||
		len(refreshedPolicy.Status.Details[templateIndex].History) == 0 ||
		refreshedPolicy.Status.Details[templateIndex].History[0].Message != fmt.Sprintf("%s; %s", compliance, msg) {
		//#nosec G401
		msgSHA1 := sha1.Sum([]byte(msg))
		if existingMsgSHA1, ok := r.lastSentMessages.Load(kn); ok && existingMsgSHA1.([20]byte) == msgSHA1 {
			// The message was already sent.
			return nil
		}

		reason := utils.EventReason(constraint.GetNamespace(), constraint.GetName())

		err := r.SendEvent(ctx, constraint, owner, reason, msg, compliance)
		if err != nil {
			return err
		}

		log.Info(
			"Sent a compliance message for the Gatekeeper constraint",
			"policy", refreshedPolicy.Name,
			"constraintKind", constraint.GetKind(),
			"constraintName", constraint.GetName(),
			"msg", msg,
		)

		r.lastSentMessages.Store(kn, msgSHA1)
	} else {
		// The message is already recorded in the Policy status so the sent message in the cache can be removed. This
		// way if the status message on the Policy is overwritten/deleted, a new status event is sent.
		r.lastSentMessages.Delete(kn)
	}

	return nil
}

// webhookEnabled verifies that the Gatekeeper validating webhook is enabled.
func (r *GatekeeperConstraintReconciler) webhookEnabled(ctx context.Context) (bool, error) {
	webhookConfig := admissionregistration.ValidatingWebhookConfiguration{}

	err := r.Get(ctx, types.NamespacedName{Name: GatekeeperWebhookName}, &webhookConfig)
	if err == nil {
		for _, webhook := range webhookConfig.Webhooks {
			if webhook.Name == "validation.gatekeeper.sh" {
				return true, nil
			}
		}
	} else if k8serrors.IsNotFound(err) {
		return false, nil
	}

	return false, err
}

// Copied from github.com/open-policy-agent/frameworks/constraint/pkg/apis/constraints
// getEnforcementAction returns a Constraint's enforcementAction, which indicates
// what should be done if a review violates a Constraint, or the Constraint fails
// to run.
//
// Returns an error if spec.enforcementAction is defined and is not a string.
func getEnforcementAction(constraint *unstructured.Unstructured) (string, error) {
	action, found, err := unstructured.NestedString(constraint.Object, "spec", "enforcementAction")
	if err != nil {
		return "", fmt.Errorf("invalid spec.enforcementAction: %w", err)
	}

	if !found {
		return "deny", nil
	}

	return action, nil
}

// hasGatekeeperConstraints checks a policy's policy-templates array to determine if it contains a Gatekeeper
// Constraint.
func hasGatekeeperConstraints(policy *policyv1.Policy) bool {
	for _, template := range policy.Spec.PolicyTemplates {
		templateMap := map[string]interface{}{}

		err := json.Unmarshal(template.ObjectDefinition.Raw, &templateMap)
		if err != nil {
			continue
		}

		templateUnstructured := unstructured.Unstructured{Object: templateMap}
		templateGVK := templateUnstructured.GroupVersionKind()

		if templateGVK.Group == utils.GConstraint {
			return true
		}
	}

	return false
}
