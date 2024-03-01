// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package statussync

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/uninstall"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

const (
	ControllerName         string = "policy-status-sync"
	hubComplianceAPISAName string = "open-cluster-management-compliance-history-api-recorder"
)

var (
	clusterClaimGVR = schema.GroupVersionResource{
		Group:    "cluster.open-cluster-management.io",
		Version:  "v1alpha1",
		Resource: "clusterclaims",
	}
	log = ctrl.Log.WithName(ControllerName)
)

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&policiesv1.Policy{}).
		Watches(
			&corev1.Event{},
			handler.EnqueueRequestsFromMapFunc(eventMapper),
			builder.WithPredicates(eventPredicateFuncs),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.ConcurrentReconciles}).
		Named(ControllerName).
		Complete(r)
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
	// EventsQueue is a queue that accepts ComplianceAPIEventRequest to then be recorded in the compliance events
	// API by StartComplianceEventsSyncer. If the compliance events API is disabled, this will be nil.
	EventsQueue workqueue.RateLimitingInterface
}

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/finalizers,verbs=update
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=clusterclaims,resourceNames=id.k8s.io,verbs=get
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
	reqLogger := log.WithValues(
		"Request.Namespace", request.Namespace, "Request.Name", request.Name, "HubNamespace", r.ClusterNamespaceOnHub,
	)

	if uninstall.DeploymentIsUninstalling {
		reqLogger.Info("Skipping reconcile because the deployment is in uninstallation mode")

		return reconcile.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	reqLogger.Info("Reconciling the policy")

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
					// confirmed deleted on hub, doing nothing
					reqLogger.Info("Policy was deleted, no status to update")

					return reconcile.Result{}, nil
				}
				// other error, requeue
				reqLogger.Error(err, "Failed to get the policy, will requeue the request")

				return reconcile.Result{}, err
			}

			// still exist on hub, recover policy on managed
			managedInstance := hubInstance.DeepCopy()
			managedInstance.Namespace = request.Namespace

			if managedInstance.Labels[common.ClusterNamespaceLabel] != "" {
				managedInstance.Labels[common.ClusterNamespaceLabel] = request.Namespace
			}

			managedInstance.SetOwnerReferences(nil)
			managedInstance.SetResourceVersion("")

			reqLogger.Info("Policy missing from managed cluster, creating it.")

			return reconcile.Result{}, r.ManagedClient.Create(ctx, managedInstance)
		}
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Error reading the policy object, will requeue the request")

		return reconcile.Result{}, err
	}

	// get hub policy
	hubPlc := &policiesv1.Policy{}

	err = r.HubClient.Get(ctx, types.NamespacedName{Namespace: r.ClusterNamespaceOnHub, Name: request.Name}, hubPlc)
	if err != nil {
		// hub policy not found, it has been deleted
		if k8serrors.IsNotFound(err) {
			reqLogger.Info("Hub policy not found, it has been deleted")
			// try to delete local one
			err = r.ManagedClient.Delete(ctx, instance)
			if err == nil || k8serrors.IsNotFound(err) {
				// no err or err is not found means local policy has been deleted
				reqLogger.Info("Managed policy was deleted")

				return reconcile.Result{}, nil
			}
			// otherwise requeue to delete again
			reqLogger.Error(err, "Failed to delete the managed policy, will requeue the request")

			return reconcile.Result{}, err
		}

		reqLogger.Error(err, "Failed to get policy on hub")

		return reconcile.Result{}, err
	}
	// found, ensure managed plc matches hub plc
	if !utils.EquivalentReplicatedPolicies(instance, hubPlc) {
		// plc mismatch, update to latest
		instance.SetAnnotations(hubPlc.GetAnnotations())
		instance.Spec = hubPlc.Spec
		// update and stop here
		reqLogger.Info("Found mismatch with hub and managed policies, updating")

		return reconcile.Result{}, r.ManagedClient.Update(ctx, instance)
	}

	// plc matches hub plc, then get events
	eventList := &corev1.EventList{}

	err = r.ManagedClient.List(ctx, eventList, client.InNamespace(instance.GetNamespace()))
	if err != nil {
		// there is an error to list events, requeue
		reqLogger.Error(err, "Error listing events, will requeue the request")

		return reconcile.Result{}, err
	}
	// filter events to current policy instance and build map
	eventForPolicyMap := make(map[string]*[]historyEvent)
	// panic if regexp invalid
	rgx := regexp.MustCompile(`(?i)^policy:\s*(?:([a-z0-9.-]+)\s*\/)?(.+)`)

	for _, event := range eventList.Items {
		// This reassignment is required so that the proper event is stored in eventHistory.
		event := event
		// sample event.Reason -- reason: 'policy: calamari/policy-grc-rbactest-example'
		reason := rgx.FindString(event.Reason)
		// Only handle events that match the UID of the current Policy
		if event.InvolvedObject.UID == instance.UID && reason != "" {
			templateName := rgx.FindStringSubmatch(event.Reason)[2]
			eventHistory := historyEvent{
				ComplianceHistory: policiesv1.ComplianceHistory{
					LastTimestamp: event.LastTimestamp,
					Message: strings.TrimSpace(strings.TrimPrefix(
						event.Message, "(combined from similar events):")),
					EventName: event.GetName(),
				},
				eventTime: *event.EventTime.DeepCopy(),
				event:     &event,
			}

			if eventForPolicyMap[templateName] == nil {
				eventForPolicyMap[templateName] = &[]historyEvent{}
			}

			templateEvents := append(*eventForPolicyMap[templateName], eventHistory)
			eventForPolicyMap[templateName] = &templateEvents
		}
	}

	oldStatus := *instance.Status.DeepCopy()
	newStatus := policiesv1.PolicyStatus{}

	reqLogger.Info("Updating status for policy templates")

	for i, policyT := range instance.Spec.PolicyTemplates {
		existingDpt := &policiesv1.DetailsPerTemplate{}

		var tName string

		object, _, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			// failed to decode PolicyTemplate, skipping it
			reqLogger.Error(err, "Failed to decode policy template, skipping it")

			existingDpt.ComplianceState = policiesv1.NonCompliant
			newStatus.Details = append(newStatus.Details, existingDpt)
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
				existingDpt = dpt
				found = true

				reqLogger.V(1).Info("Found existing status, retrieving it", "PolicyTemplate", tName)

				break
			}
		}
		// no dpt from status field, initialize it
		if !found {
			existingDpt = &policiesv1.DetailsPerTemplate{
				TemplateMeta: metav1.ObjectMeta{
					Name: tName,
				},
				History: []policiesv1.ComplianceHistory{},
			}
		}

		history := []historyEvent{}
		if eventForPolicyMap[tName] != nil {
			history = *eventForPolicyMap[tName]
		}

		// Queue up all new status events to record on the compliance API
		if r.EventsQueue != nil {
			for _, ch := range history {
				// Ignore events from controllers that don't provide the compliance API metadata or if the feature
				// is disabled.
				if ch.event.Annotations[utils.PolicyDBIDAnnotation] == "" {
					continue
				}

				isNew := true

				for _, ech := range existingDpt.History {
					if ch.EventName == ech.EventName {
						isNew = false

						break
					}
				}

				if isNew {
					ce, err := ceRequestFromEvent(ch.event)
					if err != nil {
						log.Error(err, "Failed to format the event to record in the compliance API")

						continue
					}

					r.EventsQueue.Add(ce)
				}
			}
		}

		for _, ech := range existingDpt.History {
			exists := false

			for _, ch := range history {
				if ch.LastTimestamp.Time.Equal(ech.LastTimestamp.Time) && ch.EventName == ech.EventName {
					// do nothing
					exists = true

					break
				}
			}
			// doesn't exist, append to history
			if !exists {
				history = append(history, historyEvent{ComplianceHistory: ech})
			}
		}
		// sort by lasttimestamp, break ties with EventTime (if present) or EventName
		sort.Slice(history, func(i, j int) bool {
			if history[i].LastTimestamp.Equal(&history[j].LastTimestamp) {
				if !history[i].eventTime.IsZero() && !history[j].eventTime.IsZero() {
					reqLogger.V(2).Info("Event timestamp collision, order determined by EventTime",
						"event1Name", history[i].EventName, "event2Name", history[j].EventName)

					return !history[i].eventTime.Before(&history[j].eventTime)
				}

				iTime, err := parseTimestampFromEventName(history[i].EventName)
				if err != nil {
					reqLogger.Error(err, "Can't guarantee ordering of events in this status")

					return false
				}

				jTime, err := parseTimestampFromEventName(history[j].EventName)
				if err != nil {
					reqLogger.Error(err, "Can't guarantee ordering of events in this status")

					return false
				}

				reqLogger.V(2).Info("Event timestamp collision, order determined by hex timestamp in name",
					"event1Name", history[i].EventName, "event2Name", history[j].EventName)

				return iTime.After(jTime.Time)
			}

			return !history[i].LastTimestamp.Time.Before(history[j].LastTimestamp.Time)
		})
		// remove duplicates
		newHistory := []policiesv1.ComplianceHistory{}

		for historyIndex := 0; historyIndex < len(history); historyIndex++ {
			newHistory = append(newHistory, history[historyIndex].ComplianceHistory)

			for j := historyIndex; j < len(history); j++ {
				// Skip over duplicate statuses where the event name and message match the current status
				if history[historyIndex].EventName != history[j].EventName ||
					history[historyIndex].Message != history[j].Message {
					historyIndex = j - 1

					break
				}
			}
		}
		// shorten it to first 10
		size := 10
		if len(newHistory) < 10 {
			size = len(newHistory)
		}

		existingDpt.History = newHistory[0:size]

		// set compliancy at different level
		if len(existingDpt.History) > 0 {
			existingDpt.ComplianceState = parseComplianceFromMessage(existingDpt.History[0].Message)
		}

		// append existingDpt to status
		newStatus.Details = append(newStatus.Details, existingDpt)

		reqLogger.V(1).Info("Status update complete", "PolicyTemplate", tName)
	}

	instance.Status = newStatus
	// one violation found in status of one template, set overall compliancy to NonCompliant
	isCompliant := true

	for _, dpt := range newStatus.Details {
		if dpt.ComplianceState == "NonCompliant" {
			instance.Status.ComplianceState = policiesv1.NonCompliant
			isCompliant = false

			break
		} else if dpt.ComplianceState == "Pending" {
			instance.Status.ComplianceState = policiesv1.Pending
			isCompliant = false
		} else if dpt.ComplianceState == "" {
			isCompliant = false
		}
	}

	// set to compliant only when all templates are compliant
	if isCompliant {
		instance.Status.ComplianceState = policiesv1.Compliant
	}

	// all done, update status on managed and hub
	// instance.Status.Details = nil
	if !equality.Semantic.DeepEqual(newStatus.Details, oldStatus.Details) ||
		instance.Status.ComplianceState != oldStatus.ComplianceState {
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
		reqLogger.Info("status match on managed, nothing to update")
	}

	if os.Getenv("ON_MULTICLUSTERHUB") != "true" {
		// Re-fetch the hub template in case it changed
		err = r.HubClient.Get(ctx, types.NamespacedName{Namespace: r.ClusterNamespaceOnHub, Name: request.Name}, hubPlc)
		if err != nil {
			reqLogger.Error(err, "Failed to refresh the cached policy. Will use existing policy.")
		}

		if !equality.Semantic.DeepEqual(hubPlc.Status, instance.Status) {
			reqLogger.Info("status not in sync, update the hub")

			hubPlc.Status = instance.Status

			err = r.HubClient.Status().Update(ctx, hubPlc)
			if err != nil {
				reqLogger.Error(err, "Failed to update policy status on hub")

				return reconcile.Result{}, err
			}

			r.HubRecorder.Event(hubPlc, "Normal", "PolicyStatusSync",
				fmt.Sprintf("Policy %s status was updated to %s in cluster namespace %s", hubPlc.GetName(),
					hubPlc.Status.ComplianceState, hubPlc.GetNamespace()))
		} else {
			reqLogger.Info("status match on hub, nothing to update")
		}
	}

	reqLogger.Info("Reconciling complete")

	return reconcile.Result{}, nil
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

// ceRequestFromEvent converts a Kubernetes event relating to a policy controller's compliance event to a
// struct representing a POST request to compliance events API. An error is returned if the event can't be converted.
func ceRequestFromEvent(event *corev1.Event) (utils.ComplianceAPIEventRequest, error) {
	ce := utils.ComplianceAPIEventRequest{UID: event.UID}

	pID, err := strconv.ParseInt(event.Annotations[utils.PolicyDBIDAnnotation], 10, 32)
	if err != nil {
		log.Error(err, "The event had an invalid policy ID", "policyID", event.Annotations[utils.PolicyDBIDAnnotation])

		return ce, fmt.Errorf("the event had an invalid policy ID: %s", event.Annotations[utils.PolicyDBIDAnnotation])
	}

	ce.Policy = utils.ComplianceAPIEventPolicyID{ID: int32(pID)}

	if event.Annotations[utils.ParentDBIDAnnotation] != "" {
		// The parent policy ID is optional so continue even if it's invalid
		ppID, err := strconv.ParseInt(event.Annotations[utils.ParentDBIDAnnotation], 10, 32)
		if err == nil {
			ce.ParentPolicy = &utils.ComplianceAPIEventPolicyID{ID: int32(ppID)}
		}
	}

	compliance := parseComplianceFromMessage(event.Message)

	var timestamp metav1.Time

	if timestampFromEvent, err := parseTimestampFromEventName(event.Name); err == nil {
		timestamp = timestampFromEvent
	} else {
		timestamp = event.LastTimestamp
	}

	ce.Event = utils.ComplianceAPIEvent{
		Compliance: compliance,
		Message:    strings.TrimLeft(event.Message[len(compliance):], " ;"),
		Timestamp:  timestamp.Format(time.RFC3339Nano),
		ReportedBy: "governance-policy-framework",
	}

	return ce, nil
}

// StartComplianceEventsSyncer will monitor the events queue and record compliance events on the compliance events
// API. It uses either certificate or token authentication to authenticate with the compliance events API using the
// configuration in hubCfg. Note that apiURL is the base URL to the API. It should not contain the path to the
// POST API endpoint.
func StartComplianceEventsSyncer(
	ctx context.Context,
	clusterName string,
	hubCfg *rest.Config,
	managedCfg *rest.Config,
	apiURL string,
	events workqueue.RateLimitingInterface,
) error {
	var hubToken string

	if hubCfg.BearerToken != "" {
		hubToken = hubCfg.BearerToken
	}

	managedClient, err := dynamic.NewForConfig(managedCfg)
	if err != nil {
		return err
	}

	var clusterID string

	idClusterClaim, err := managedClient.Resource(clusterClaimGVR).Get(ctx, "id.k8s.io", metav1.GetOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return err
	}

	if err == nil {
		clusterID, _, _ = unstructured.NestedString(idClusterClaim.Object, "spec", "value")
	}

	if clusterID == "" {
		log.Info("The id.k8s.io cluster claim is not set. Using the cluster ID of unknown.")

		clusterID = "unknown"
	}

	processedEvents := map[types.UID]bool{}

	caCertPool, err := x509.SystemCertPool()
	if err != nil {
		log.Error(err, "Failed to detect the default system CAs")

		caCertPool = x509.NewCertPool()
	}

	// Append the Kubernete API Server CAs in case the Hub cluster's ingress CA is not trusted by the system pool.
	if hubCfg.CAData != nil {
		_ = caCertPool.AppendCertsFromPEM(hubCfg.CAData)
	} else if hubCfg.CAFile != "" {
		caData, err := os.ReadFile(hubCfg.CAFile)
		if err == nil {
			log.Info("The hub kubeconfig CA file can't be read. Ignoring it.", "path", hubCfg.CAFile)
		}

		_ = caCertPool.AppendCertsFromPEM(caData)
	}

	httpClient := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    caCertPool,
			},
		},
	}

	for {
		ceUntyped, shutdown := events.Get()
		if shutdown {
			return nil
		}

		ce, ok := ceUntyped.(utils.ComplianceAPIEventRequest)
		if !ok {
			events.Forget(ceUntyped)
			events.Done(ceUntyped)

			continue
		}

		if ce.UID != "" && processedEvents[ce.UID] {
			events.Forget(ceUntyped)
			events.Done(ceUntyped)

			continue
		}

		ce.Cluster = utils.ComplianceAPIEventCluster{
			Name:      clusterName,
			ClusterID: clusterID,
		}

		requestJSON, err := json.Marshal(ce)
		if err != nil {
			log.Error(err, "Failed to record the event with the compliance API due to an invalid format")

			events.Forget(ceUntyped)
			events.Done(ceUntyped)

			continue
		}

		httpRequest, err := http.NewRequestWithContext(
			ctx, http.MethodPost, apiURL+"/api/v1/compliance-events", bytes.NewBuffer(requestJSON),
		)
		if err != nil {
			log.Error(err, "Failed to record the event with the compliance API")

			events.AddRateLimited(ceUntyped)
			events.Done(ceUntyped)

			continue
		}

		httpRequest.Header.Set("Content-Type", "application/json")

		if hubToken == "" {
			var err error

			hubToken, err = getHubComplianceAPIToken(ctx, hubCfg, clusterName)
			if err != nil || hubToken == "" {
				var msg string

				if err != nil {
					msg = err.Error()
				} else {
					msg = "the token was not set on the secret"
				}

				log.Info(
					"Failed to get the compliance API hub token. Will requeue in 10 seconds.", "error", msg,
				)

				events.AddAfter(ceUntyped, 10*time.Second)
				events.Done(ceUntyped)

				continue
			}
		}

		httpRequest.Header.Set("Authorization", "Bearer "+hubToken)

		httpResponse, err := httpClient.Do(httpRequest)
		if err != nil {
			log.Info(
				"Failed to record the compliance event with the compliance API. Will requeue in 10 seconds.",
				"error", err.Error(),
			)

			events.AddAfter(ceUntyped, 10*time.Second)
			events.Done(ceUntyped)

			continue
		}

		// A conflict indicates the compliance event already exists
		if httpResponse.StatusCode != http.StatusCreated && httpResponse.StatusCode != http.StatusConflict {
			var message string

			body, err := io.ReadAll(httpResponse.Body)
			if err == nil {
				rv := map[string]interface{}{}

				err := json.Unmarshal(body, &rv)
				if err == nil {
					message, _ = rv["message"].(string)
				}
			}

			// If it's a bad request, then the database experienced data loss and the database ID is no longer valid.
			if httpResponse.StatusCode == http.StatusBadRequest {
				log.V(0).Info(
					"Failed to record the compliance event with the compliance API. Will not requeue.",
					"statusCode", httpResponse.StatusCode,
					"message", message,
				)
				events.Forget(ceUntyped)
				events.Done(ceUntyped)

				continue
			}

			log.Info(
				"Failed to record the compliance event with the compliance API. Will requeue.",
				"statusCode", httpResponse.StatusCode,
				"message", message,
			)

			if httpResponse.StatusCode == http.StatusUnauthorized || httpResponse.StatusCode == http.StatusForbidden {
				// Wipe out the hubToken so that the token is fetched again on the next try.
				hubToken = ""
			}

			events.AddRateLimited(ceUntyped)
			events.Done(ceUntyped)

			continue
		}

		// The compliance event has been recorded
		events.Forget(ceUntyped)
		events.Done(ceUntyped)

		log.Info("Recorded a compliance event with the compliance history API", "eventUID", ce.UID)

		if ce.UID != "" {
			processedEvents[ce.UID] = true
		}
	}
}

// getHubComplianceAPIToken retrieves the token associated with the service account with compliance history API
// recording permssions in the cluster namespace.
func getHubComplianceAPIToken(ctx context.Context, hubCfg *rest.Config, clusterNamespace string) (string, error) {
	client, err := kubernetes.NewForConfig(hubCfg)
	if err != nil {
		return "", err
	}

	saTokenSecret, err := client.CoreV1().Secrets(clusterNamespace).Get(
		ctx, hubComplianceAPISAName, metav1.GetOptions{},
	)
	if err != nil {
		return "", err
	}

	return string(saTokenSecret.Data["token"]), nil
}

type historyEvent struct {
	policiesv1.ComplianceHistory
	eventTime metav1.MicroTime
	event     *corev1.Event
}
