// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package statussync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/uninstall"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

const (
	ControllerName string = "policy-status-sync"
)

func logFromCtx(ctx context.Context) logr.Logger {
	l, err := logr.FromContext(ctx)
	if err != nil {
		// fallback to the controller-runtime logger, which will have less info
		l = ctrl.Log
	}

	return l.WithName(ControllerName)
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyReconciler) SetupWithManager(mgr ctrl.Manager, additionalSource source.Source) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&policiesv1.Policy{}).
		Watches(
			&corev1.Event{},
			handler.EnqueueRequestsFromMapFunc(eventMapper),
			builder.WithPredicates(eventPredicateFuncs),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.ConcurrentReconciles}).
		Named(ControllerName)

	if additionalSource != nil {
		builder = builder.WatchesRawSource(additionalSource)
	}

	return builder.Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &PolicyReconciler{}

// ReconcilePolicy reconciles a Policy object
type PolicyReconciler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	HubClient             client.Client
	ManagedClient         client.Client
	HubRecorder           record.EventRecorder
	ManagedRecorder       record.EventRecorder
	Scheme                *runtime.Scheme
	ClusterNamespaceOnHub string
	ConcurrentReconciles  int
	SpecSyncRequests      chan<- event.GenericEvent
}

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get,resourceNames="open-cluster-management-compliance-history-api-recorder"
// This is required for the status lease for the addon framework
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *PolicyReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	reqLogger := logFromCtx(ctx).WithValues("HubNamespace", r.ClusterNamespaceOnHub)

	if uninstall.DeploymentIsUninstalling {
		reqLogger.Info("Skipping reconcile because the deployment is in uninstallation mode")

		return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	reqLogger.V(1).Info("Reconciling the policy")

	// Fetch the Policy instance
	instance := &policiesv1.Policy{}

	err := r.ManagedClient.Get(ctx, request.NamespacedName, instance)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// The replicated policy on the managed cluster was deleted.
			// check if it was deleted by user by checking if it still exists on hub
			hubInstance := &policiesv1.Policy{}

			err = r.HubClient.Get(
				ctx, types.NamespacedName{Namespace: r.ClusterNamespaceOnHub, Name: request.Name}, hubInstance,
			)
			if err != nil {
				if k8serrors.IsNotFound(err) {
					reqLogger.Info("Policy was deleted, no status to update")

					return reconcile.Result{}, nil
				}

				reqLogger.Error(err, "Failed to get the policy, will requeue the request")

				return reconcile.Result{}, err
			}

			if r.SpecSyncRequests != nil {
				reqLogger.Info("Policy is missing on the managed cluster. Triggering the spec-sync to recreate it.")

				r.triggerSpecSyncReconcile(request)
			}

			return reconcile.Result{}, nil
		}

		reqLogger.Error(err, "Error reading the policy object, will requeue the request")

		return reconcile.Result{}, err
	}

	hubInstance := &policiesv1.Policy{}

	err = r.HubClient.Get(ctx, types.NamespacedName{Namespace: r.ClusterNamespaceOnHub, Name: request.Name}, hubInstance)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			if r.SpecSyncRequests != nil {
				reqLogger.Info(
					"Policy is missing on the hub. Triggering the spec-sync to delete the replicated policy.",
				)

				r.triggerSpecSyncReconcile(request)
			}

			return reconcile.Result{}, nil
		}

		reqLogger.Error(err, "Failed to get policy on hub")

		return reconcile.Result{}, err
	}

	if !utils.EquivalentReplicatedPolicies(instance, hubInstance) {
		if r.SpecSyncRequests != nil {
			reqLogger.Info("Found a mismatch with the hub and managed policies. Triggering the spec-sync to handle it.")

			r.triggerSpecSyncReconcile(request)
		}

		return reconcile.Result{}, nil
	}

	// managed Policy matches Hub policy, now get the status events
	eventList := &corev1.EventList{}

	err = r.ManagedClient.List(ctx, eventList, client.InNamespace(instance.GetNamespace()))
	if err != nil {
		reqLogger.Error(err, "Error listing events, will requeue the request")

		return reconcile.Result{}, err
	}

	// filter events to current policy instance and build map
	eventForPolicyMap := make(map[string][]policiesv1.ComplianceHistory)
	rgx := regexp.MustCompile(`(?i)^policy:\s*(?:([a-z0-9.-]+)\s*\/)?(.+)`)

	for _, event := range eventList.Items {
		// sample event.Reason -- reason: 'policy: calamari/policy-grc-rbactest-example'
		reason := rgx.FindString(event.Reason)
		// Only handle events that match the UID of the current Policy
		if event.InvolvedObject.UID == instance.UID && reason != "" {
			templateName := rgx.FindStringSubmatch(event.Reason)[2]
			histEvent := policiesv1.ComplianceHistory{
				LastTimestamp: event.LastTimestamp,
				Message: strings.TrimSpace(strings.TrimPrefix(
					event.Message, "(combined from similar events):")),
				EventName: event.GetName(),
			}

			eventForPolicyMap[templateName] = append(eventForPolicyMap[templateName], histEvent)
		}
	}

	oldStatus := *instance.Status.DeepCopy()
	newStatus := policiesv1.PolicyStatus{}

	reqLogger.Info("Recalculating details for policy templates")

	for i, policyT := range instance.Spec.PolicyTemplates {
		details := &policiesv1.DetailsPerTemplate{}

		var tName string

		object, _, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			reqLogger.Error(err, "Failed to decode policy template", "TemplateIdx", i)

			tName = fmt.Sprintf("template-%v", i) // template-sync emits this name on error
		} else {
			tName = object.(metav1.Object).GetName()
		}

		// retrieve existingDpt from instance.status.details field
		found := false

		for _, dpt := range instance.Status.Details {
			if dpt.TemplateMeta.Name == tName {
				// found existing status for policyTemplate
				// retrieve it
				details = dpt
				found = true

				reqLogger.V(1).Info("Found existing template status details", "TemplateName", tName, "TemplateIdx", i)

				break
			}
		}

		// no dpt from status field, initialize it
		if !found {
			reqLogger.V(1).Info("No existing template status details", "TemplateName", tName, "TemplateIdx", i)

			details = &policiesv1.DetailsPerTemplate{
				TemplateMeta: metav1.ObjectMeta{
					Name: tName,
				},
				History: []policiesv1.ComplianceHistory{},
			}
		}

		// Add new events if they are not yet in the history
	EventLoop:
		for _, newEvent := range eventForPolicyMap[tName] {
			for _, existingEvent := range details.History {
				match := existingEvent.LastTimestamp.Time.Equal(newEvent.LastTimestamp.Time) &&
					existingEvent.EventName == newEvent.EventName

				if match {
					continue EventLoop
				}
			}

			details.History = append(details.History, newEvent)
		}

		// sort by LastTimestamp, break ties with EventName. The most recent event is the 0th.
		sort.Slice(details.History, func(i, j int) bool {
			if details.History[i].LastTimestamp.Equal(&details.History[j].LastTimestamp) {
				iTime, iErr := parseTimestampFromEventName(details.History[i].EventName)
				jTime, jErr := parseTimestampFromEventName(details.History[j].EventName)

				if iErr != nil || jErr != nil {
					reqLogger.Error(err, "Can't guarantee ordering of events in this status")

					return false
				}

				reqLogger.V(2).Info("Event timestamp collision, order determined by hex timestamp in name",
					"event1Name", details.History[i].EventName, "event2Name", details.History[j].EventName)

				return iTime.After(jTime.Time)
			}

			return !details.History[i].LastTimestamp.Time.Before(details.History[j].LastTimestamp.Time)
		})

		dedupedHistory := []policiesv1.ComplianceHistory{}

		for i, event := range details.History {
			// The most recent event is always saved.
			if i == 0 {
				dedupedHistory = append(dedupedHistory, event)

				continue
			}

			// limit total length to 10
			if len(dedupedHistory) == 10 {
				break
			}

			// Otherwise, only save it if the message and name do not equal the next most recent event.
			match := event.EventName == details.History[i-1].EventName &&
				event.Message == details.History[i-1].Message

			if !match {
				dedupedHistory = append(dedupedHistory, event)
			}
		}

		details.History = dedupedHistory

		// set compliancy at different level
		if len(details.History) > 0 {
			details.ComplianceState = parseComplianceFromMessage(details.History[0].Message)
		}

		// append details to status
		newStatus.Details = append(newStatus.Details, details)

		reqLogger.V(1).Info("Details recalculated", "TemplateName", tName, "TemplateIdx", i)
	}

	instance.Status = newStatus
	// one violation found in status of one template, set overall compliancy to NonCompliant
	isCompliant := true

Loop:
	for _, dpt := range newStatus.Details {
		switch dpt.ComplianceState {
		case policiesv1.NonCompliant:
			instance.Status.ComplianceState = policiesv1.NonCompliant
			isCompliant = false

			break Loop
		case policiesv1.Pending:
			instance.Status.ComplianceState = policiesv1.Pending
			isCompliant = false
		case policiesv1.Compliant: // Continue if the status is compliant, no need to update the compliance state
			continue Loop
		case "":
			isCompliant = false
		}
	}

	// set to compliant only when all templates are compliant
	if isCompliant {
		instance.Status.ComplianceState = policiesv1.Compliant
	}

	// Update status on managed cluster if needed.
	match := equality.Semantic.DeepEqual(newStatus.Details, oldStatus.Details) &&
		instance.Status.ComplianceState == oldStatus.ComplianceState

	if !match {
		reqLogger.Info("status mismatch on managed, update it")

		err = r.ManagedClient.Status().Update(ctx, instance)
		if err != nil {
			reqLogger.Error(err, "Failed to get update policy status on managed")

			return reconcile.Result{}, err
		}

		r.ManagedRecorder.Event(instance, "Normal", "PolicyStatusSync",
			fmt.Sprintf("Policy %s status was updated in cluster namespace %s", instance.GetName(),
				instance.GetNamespace()))
	} else {
		reqLogger.V(1).Info("status match on managed, nothing to update")
	}

	if os.Getenv("ON_MULTICLUSTERHUB") != "true" {
		// Re-fetch the hub template in case it changed
		nn := types.NamespacedName{Namespace: r.ClusterNamespaceOnHub, Name: request.Name}
		updatedHubInstance := &policiesv1.Policy{}

		err = r.HubClient.Get(ctx, nn, updatedHubInstance)
		if err != nil {
			reqLogger.Error(err, "Failed to refresh the cached policy. Will use existing policy.")
		} else {
			hubInstance = updatedHubInstance
		}

		if !equality.Semantic.DeepEqual(hubInstance.Status, instance.Status) {
			reqLogger.Info("status not in sync, update the hub")

			hubInstance.Status = instance.Status

			err = r.HubClient.Status().Update(ctx, hubInstance)
			if err != nil {
				reqLogger.Error(err, "Failed to update policy status on hub")

				return reconcile.Result{}, err
			}

			r.HubRecorder.Event(hubInstance, "Normal", "PolicyStatusSync",
				fmt.Sprintf("Policy %s status was updated to %s in cluster namespace %s", hubInstance.GetName(),
					hubInstance.Status.ComplianceState, hubInstance.GetNamespace()))
		} else {
			reqLogger.V(1).Info("status match on hub, nothing to update")
		}
	}

	reqLogger.V(2).Info("Reconciling complete")

	return reconcile.Result{}, nil
}

func (r *PolicyReconciler) triggerSpecSyncReconcile(request reconcile.Request) {
	hubReplicatedPolicy := &unstructured.Unstructured{}
	hubReplicatedPolicy.SetAPIVersion(policiesv1.GroupVersion.String())
	hubReplicatedPolicy.SetKind(policiesv1.Kind)
	hubReplicatedPolicy.SetName(request.Name)
	hubReplicatedPolicy.SetNamespace(r.ClusterNamespaceOnHub)

	r.SpecSyncRequests <- event.GenericEvent{Object: hubReplicatedPolicy}
}

// parseTimestampFromEventName will parse the event name for a hexadecimal nanosecond timestamp as a suffix after a
// period. This is a client-go convention that is repeated in the policy framework.
func parseTimestampFromEventName(eventName string) (metav1.Time, error) {
	nameParts := strings.Split(eventName, ".")

	nanos, err := strconv.ParseInt(nameParts[len(nameParts)-1], 16, 64)
	if err != nil {
		return metav1.Time{}, errors.New("Unable to find a valid hexadecimal timestamp in event name: " + eventName)
	}

	return metav1.Unix(0, nanos), nil
}

func parseComplianceFromMessage(message string) policiesv1.ComplianceState {
	cleanMsg := strings.ToLower(
		strings.TrimSpace(
			strings.TrimPrefix(message, "(combined from similar events):"),
		),
	)

	if strings.HasPrefix(cleanMsg, "compliant") {
		return policiesv1.Compliant
	} else if strings.HasPrefix(cleanMsg, "pending") {
		return policiesv1.Pending
	}

	return policiesv1.NonCompliant
}
