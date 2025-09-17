// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package statussync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	depclient "github.com/stolostron/kubernetes-dependency-watches/client"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
func (r *PolicyReconciler) SetupWithManager(mgr ctrl.Manager, additionalSources ...source.Source) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&policiesv1.Policy{}).
		Watches(
			&corev1.Event{},
			handler.EnqueueRequestsFromMapFunc(eventMapper),
			builder.WithPredicates(eventPredicateFuncs),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.ConcurrentReconciles}).
		Named(ControllerName)

	for _, addlSource := range additionalSources {
		if addlSource != nil {
			builder = builder.WatchesRawSource(addlSource)
		}
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
	DynamicWatcher        depclient.DynamicWatcher
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

	policyObjID := policyID(request.Name, request.Namespace)

	// Start query batch for caching and watching related objects
	err := r.DynamicWatcher.StartQueryBatch(policyObjID)
	if err != nil {
		reqLogger.Error(err, "Could not start query batch for the watcher", "objectID", policyObjID)

		return reconcile.Result{}, err
	}

	defer func() {
		err := r.DynamicWatcher.EndQueryBatch(policyObjID)
		if err != nil {
			reqLogger.Error(err, "Could not end query batch for the watcher", "objectID", policyObjID)
		}
	}()

	instance, hubInstance, err := r.getInstances(ctx, request)
	if err != nil || instance == nil || hubInstance == nil {
		return reconcile.Result{}, err
	}

	reqLogger.Info("Recalculating details for policy templates")

	oldStatus := *instance.Status.DeepCopy()

	instance.Status.Details, err = r.getDetails(ctx, instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = r.updateStatuses(ctx, instance, hubInstance, oldStatus)
	if err != nil {
		return reconcile.Result{}, err
	}

	reqLogger.V(1).Info("Reconciling complete")

	return reconcile.Result{}, nil
}

// getInstances retrieves both the managed cluster and hub cluster instances of
// a Policy. It handles Policy deletion, missing Policies, and Policy mismatches
// by triggering spec synchronization when inconsistencies are detected.
func (r *PolicyReconciler) getInstances(
	ctx context.Context, request reconcile.Request,
) (managedInstance, hubInstance *policiesv1.Policy, err error) {
	reqLogger := logFromCtx(ctx).WithValues("HubNamespace", r.ClusterNamespaceOnHub)
	managedInstance = &policiesv1.Policy{}

	err = r.ManagedClient.Get(ctx, request.NamespacedName, managedInstance)
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

					return nil, nil, nil
				}

				reqLogger.Error(err, "Failed to get the policy, will requeue the request")

				return nil, nil, err
			}

			if r.SpecSyncRequests != nil {
				reqLogger.Info("Policy is missing on the managed cluster. Triggering the spec-sync to recreate it.")

				r.triggerSpecSyncReconcile(request)
			}

			return nil, nil, nil
		}

		reqLogger.Error(err, "Error reading the policy object, will requeue the request")

		return nil, nil, err
	}

	hubInstance = &policiesv1.Policy{}
	nn := types.NamespacedName{Namespace: r.ClusterNamespaceOnHub, Name: request.Name}

	err = r.HubClient.Get(ctx, nn, hubInstance)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			if r.SpecSyncRequests != nil {
				reqLogger.Info(
					"Policy is missing on the hub. Triggering the spec-sync to delete the replicated policy.",
				)

				r.triggerSpecSyncReconcile(request)
			}

			return nil, nil, nil
		}

		reqLogger.Error(err, "Failed to get policy on hub")

		return nil, nil, err
	}

	if !utils.EquivalentReplicatedPolicies(managedInstance, hubInstance) {
		if r.SpecSyncRequests != nil {
			reqLogger.Info("Found a mismatch with the hub and managed policies. Triggering the spec-sync to handle it.")

			r.triggerSpecSyncReconcile(request)
		}

		return nil, nil, nil
	}

	return managedInstance, hubInstance, nil
}

// getEventsInCluster retrieves and filters compliance events for a policy from
// the managed cluster, organizing them by template name. If an event name has
// the conventional hexadecimal timestamp suffix, that will be used for a
// higher-precision timestamp in the returned history.
func (r *PolicyReconciler) getEventsInCluster(
	ctx context.Context, instance *policiesv1.Policy,
) (map[string][]policiesv1.ComplianceHistory, error) {
	eventList := &corev1.EventList{}

	err := r.ManagedClient.List(ctx, eventList, client.InNamespace(instance.GetNamespace()))
	if err != nil {
		return nil, err
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

			// Use the timestamp from the name if possible
			ts, err := parseTimestampFromEventName(event.GetName())
			if err != nil {
				// `LastTimestamp` only has precision down to seconds
				ts = event.LastTimestamp
			}

			histEvent := policiesv1.ComplianceHistory{
				LastTimestamp: ts,
				Message: strings.TrimSpace(strings.TrimPrefix(
					event.Message, "(combined from similar events):")),
				EventName: event.GetName(),
			}

			eventForPolicyMap[templateName] = append(eventForPolicyMap[templateName], histEvent)
		}
	}

	return eventForPolicyMap, nil
}

// getEventsInTemplate retrieves compliance history events from a template's
// status field, converting them to the standard compliance history format. It
// skips events with missing timestamps or messages and generates event names
// using the Policy name and nanosecond timestamp, matching the client-go
// convention. If the template's status field is not found or is not in the
// correct format, it returns an empty list.
func (r *PolicyReconciler) getEventsInTemplate(
	tmplGVK schema.GroupVersionKind, name, namespace string, policyObjID depclient.ObjectIdentifier,
) ([]policiesv1.ComplianceHistory, error) {
	tmplUnstruct, err := r.DynamicWatcher.Get(policyObjID, tmplGVK, namespace, name)
	if err != nil {
		if errors.Is(err, depclient.ErrNoVersionedResource) || errors.Is(err, depclient.ErrResourceUnwatchable) {
			// If the kind isn't found, or isn't compatible with watches, just return an empty list.
			return []policiesv1.ComplianceHistory{}, nil
		}

		return []policiesv1.ComplianceHistory{}, err
	}

	if tmplUnstruct == nil {
		return []policiesv1.ComplianceHistory{}, nil
	}

	events, found, err := unstructured.NestedSlice(tmplUnstruct.Object, "status", "history")
	if !found || err != nil {
		// If there's an error or the field isn't found, just return an empty list.
		return []policiesv1.ComplianceHistory{}, nil //nolint:nilerr
	}

	historyEvents := make([]policiesv1.ComplianceHistory, 0, len(events))

	for _, ev := range events {
		evBytes, err := json.Marshal(ev)
		if err != nil {
			continue
		}

		event := templateHistoryEvent{}

		err = json.Unmarshal(evBytes, &event)
		if err != nil {
			continue
		}

		if event.LastTimestamp.IsZero() || event.Message == "" {
			continue // Skip events that don't have the proper data
		}

		historyEvents = append(historyEvents, policiesv1.ComplianceHistory{
			LastTimestamp: metav1.Time(event.LastTimestamp),
			Message:       strings.TrimSpace(event.Message),
			EventName:     fmt.Sprintf("%v.%x", policyObjID.Name, event.LastTimestamp.UnixNano()),
		})
	}

	return historyEvents, nil
}

// getDetails collects and processes compliance events for each policy template,
// building a history of compliance states and deduplicating similar events.
// It limits history to 10 events per template, sorts by timestamp (most recent
// first), and returns detailed status information for status synchronization.
func (r *PolicyReconciler) getDetails(
	ctx context.Context, instance *policiesv1.Policy,
) (allDetails []*policiesv1.DetailsPerTemplate, err error) {
	reqLogger := logFromCtx(ctx).WithValues("HubNamespace", r.ClusterNamespaceOnHub)

	eventForPolicyMap, err := r.getEventsInCluster(ctx, instance)
	if err != nil {
		reqLogger.Error(err, "Error listing events, will requeue the request")

		return nil, err
	}

	policyObjID := policyID(instance.Name, instance.Namespace)

	for i, policyT := range instance.Spec.PolicyTemplates {
		var tName string

		object, tmplGVK, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			reqLogger.Error(err, "Failed to decode policy template", "TemplateIdx", i)

			tName = fmt.Sprintf("template-%v", i) // template-sync emits this name on error
		} else {
			obj, ok := object.(metav1.Object)
			if !ok || tmplGVK == nil {
				reqLogger.Error(err, "Failed to decode policy template", "TemplateIdx", i)

				tName = fmt.Sprintf("template-%v", i) // template-sync emits this name on error
			} else {
				tName = obj.GetName()

				templateEvents, err := r.getEventsInTemplate(*tmplGVK, tName, instance.Namespace, policyObjID)
				if err != nil {
					reqLogger.Error(err, "Error getting template, will requeue the request",
						"TemplateName", tName, "TemplateIdx", i)

					return nil, err
				}

				eventForPolicyMap[tName] = append(eventForPolicyMap[tName], templateEvents...)
			}
		}

		detailLogger := reqLogger.WithValues("TemplateName", tName, "TemplateIdx", i)
		templateDetails := mergeDetails(eventForPolicyMap[tName], instance.Status.Details, tName, detailLogger)

		allDetails = append(allDetails, templateDetails)

		detailLogger.V(1).Info("Details recalculated")
	}

	return allDetails, nil
}

// mergeDetails combines new compliance events with existing template status
// details, deduplicating events, sorting by timestamp, limiting history to 10
// events, and determining the compliance state from the most recent event. It
// preserves existing status details when available.
func mergeDetails(
	events []policiesv1.ComplianceHistory,
	existingDPTs []*policiesv1.DetailsPerTemplate,
	tName string,
	detailLogger logr.Logger,
) (details *policiesv1.DetailsPerTemplate) {
	details = &policiesv1.DetailsPerTemplate{
		TemplateMeta: metav1.ObjectMeta{
			Name: tName,
		},
		History: []policiesv1.ComplianceHistory{},
	}

	for _, dpt := range existingDPTs {
		if dpt.TemplateMeta.Name == tName {
			// found existing status for policyTemplate - retrieve it
			details = dpt

			detailLogger.V(1).Info("Found existing template status details")

			break
		}
	}

	// Add old events if they were not found in cluster events or the template status
	for _, oldEvent := range details.History {
		found := false

		for _, foundEvent := range events {
			if foundEvent.EventName == oldEvent.EventName {
				found = true

				break
			}
		}

		if !found {
			events = append(events, oldEvent)
		}
	}

	// sort by LastTimestamp. The most recent event is the 0th.
	sort.Slice(events, func(i, j int) bool {
		return !events[i].LastTimestamp.Time.Before(events[j].LastTimestamp.Time)
	})

	dedupedHistory := []policiesv1.ComplianceHistory{}

	for i, event := range events {
		// The most recent event is always saved.
		if i == 0 {
			dedupedHistory = append(dedupedHistory, event)

			continue
		}

		// limit total length to 10
		if len(dedupedHistory) == 10 {
			break
		}

		// Events with the same message and very close timestamps should not be saved.
		tooSimilar := timestampsWithin(event.LastTimestamp, events[i-1].LastTimestamp, 5*time.Microsecond) &&
			event.Message == events[i-1].Message

		if !tooSimilar {
			dedupedHistory = append(dedupedHistory, event)
		}
	}

	details.History = dedupedHistory

	// set compliancy at different level
	if len(details.History) > 0 {
		details.ComplianceState = parseComplianceFromMessage(details.History[0].Message)
	}

	return details
}

// timestampsWithin returns true if the input timestamps are different by less than the threshold
func timestampsWithin(ts1, ts2 metav1.Time, threshold time.Duration) bool {
	low := ts1.Add(-1 * threshold)
	high := ts1.Add(threshold)

	return low.Before(ts2.Time) && ts2.Time.Before(high)
}

// updateStatuses determines the overall compliance state from template details
// and synchronizes policy status between managed and hub clusters. It updates
// the managed cluster first, then propagates changes to the hub cluster, only
// when status changes are detected.
func (r *PolicyReconciler) updateStatuses(
	ctx context.Context, instance, hubInstance *policiesv1.Policy, oldStatus policiesv1.PolicyStatus,
) (err error) {
	reqLogger := logFromCtx(ctx).WithValues("HubNamespace", r.ClusterNamespaceOnHub)

	// one violation found in status of one template, set overall compliancy to NonCompliant
	isCompliant := true

Loop:
	for _, dpt := range instance.Status.Details {
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
	match := equality.Semantic.DeepEqual(instance.Status.Details, oldStatus.Details) &&
		instance.Status.ComplianceState == oldStatus.ComplianceState

	if !match {
		reqLogger.Info("status mismatch on managed, update it")

		err = r.ManagedClient.Status().Update(ctx, instance)
		if err != nil {
			reqLogger.Error(err, "Failed to get update policy status on managed")

			return err
		}

		r.ManagedRecorder.Event(instance, "Normal", "PolicyStatusSync",
			fmt.Sprintf("Policy %s status was updated in cluster namespace %s", instance.GetName(),
				instance.GetNamespace()))
	} else {
		reqLogger.V(1).Info("status match on managed, nothing to update")
	}

	if os.Getenv("ON_MULTICLUSTERHUB") != "true" {
		// Re-fetch the hub template in case it changed
		nn := types.NamespacedName{Namespace: r.ClusterNamespaceOnHub, Name: instance.Name}
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

				return err
			}

			r.HubRecorder.Event(hubInstance, "Normal", "PolicyStatusSync",
				fmt.Sprintf("Policy %s status was updated to %s in cluster namespace %s", hubInstance.GetName(),
					hubInstance.Status.ComplianceState, hubInstance.GetNamespace()))
		} else {
			reqLogger.V(1).Info("status match on hub, nothing to update")
		}
	}

	return nil
}

func (r *PolicyReconciler) triggerSpecSyncReconcile(request reconcile.Request) {
	hubReplicatedPolicy := &unstructured.Unstructured{}
	hubReplicatedPolicy.SetAPIVersion(policiesv1.GroupVersion.String())
	hubReplicatedPolicy.SetKind(policiesv1.Kind)
	hubReplicatedPolicy.SetName(request.Name)
	hubReplicatedPolicy.SetNamespace(r.ClusterNamespaceOnHub)

	r.SpecSyncRequests <- event.GenericEvent{Object: hubReplicatedPolicy}
}

// parseTimestampFromEventName will parse the event name for a hexadecimal
// nanosecond timestamp as a suffix after a period. This is a client-go
// convention that is repeated in the policy framework.
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

type templateHistoryEvent struct {
	LastTimestamp metav1.MicroTime `json:"lastTimestamp,omitempty"`
	Message       string           `json:"message,omitempty"`
}

func policyID(name, namespace string) depclient.ObjectIdentifier {
	return depclient.ObjectIdentifier{
		Group:     policiesv1.GroupVersion.Group,
		Version:   policiesv1.GroupVersion.Version,
		Kind:      "Policy",
		Namespace: namespace,
		Name:      name,
	}
}
