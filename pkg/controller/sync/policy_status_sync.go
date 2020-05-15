// Copyright (c) 2020 Red Hat, Inc.
package sync

import (
	"context"
	"regexp"
	"sort"
	"strings"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("policy-status-sync")

// Add creates a new Policy Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, hubCfg *rest.Config) error {
	hubClient, err := client.New(hubCfg, client.Options{})
	if err != nil {
		log.Error(err, "Failed to generate client to managed cluster")
		return err
	}
	return add(mgr, newReconciler(mgr, hubClient))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, hubClient client.Client) reconcile.Reconciler {
	return &ReconcilePolicy{hubClient: hubClient, managedClient: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("policy-status-sync", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Policy
	err = c.Watch(&source.Kind{Type: &policiesv1.Policy{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	err = c.Watch(
		&source.Kind{Type: &corev1.Event{}},
		&handler.EnqueueRequestsFromMapFunc{ToRequests: &eventMapper{mgr.GetClient()}}, eventPredicateFuncs)
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
	hubClient     client.Client
	managedClient client.Client
	scheme        *runtime.Scheme
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
	err := r.managedClient.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// repliated policy on hub was deleted
			// check if it was deleted by user by checking if it still exists on hub
			hubInstance := &policiesv1.Policy{}
			err = r.hubClient.Get(context.TODO(), request.NamespacedName, hubInstance)
			if err != nil {
				if errors.IsNotFound(err) {
					// confirmed deleted on hub, doing nothing
					reqLogger.Info("Policy was deleted, no status to update...")
					return reconcile.Result{}, nil
				}
				// other error, requeue
				return reconcile.Result{}, err
			}
			// still exist on hub, recover policy on managed
			hubInstance.SetOwnerReferences(nil)
			hubInstance.SetResourceVersion("")
			return reconcile.Result{}, r.managedClient.Create(context.TODO(), hubInstance)
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}
	// get hub policy
	hubPlc := &policiesv1.Policy{}
	err = r.hubClient.Get(context.TODO(), request.NamespacedName, hubPlc)
	if err != nil {
		// hub policy not found, it has been deleted
		if errors.IsNotFound(err) {
			// try to delete local one
			err = r.managedClient.Delete(context.TODO(), instance)
			if err == nil || errors.IsNotFound(err) {
				// no err or err is not found means local policy has been deleted
				return reconcile.Result{}, nil
			}
			// otherwise requeue to delete again
			return reconcile.Result{}, err
		}
		reqLogger.Error(err, "Failed to get policy on hub")
		return reconcile.Result{}, err
	}
	// found, ensure managed plc matches hub plc
	if !common.CompareSpecAndAnnotation(instance, hubPlc) {
		// plc mismatch, update to latest
		instance.SetAnnotations(hubPlc.GetAnnotations())
		instance.Spec = hubPlc.Spec
		// update and stop here
		return reconcile.Result{}, r.managedClient.Update(context.TODO(), instance)
	}

	// plc matches hub plc, then get events
	eventList := &corev1.EventList{}
	err = r.managedClient.List(context.TODO(), eventList, client.InNamespace(instance.GetNamespace()))
	if err != nil {
		// there is an error to list events, requeue
		return reconcile.Result{}, err
	}
	// filter events to current policy instance and build map
	eventForPolicyMap := make(map[string]*[]policiesv1.ComplianceHistory)
	rgx, err := regexp.Compile(`(?i)^policy:\s*([A-Za-z0-9.-]+)\s*\/([A-Za-z0-9.-]+)`)
	if err != nil {
		// regexp is wrong, how?
		return reconcile.Result{}, err
	}
	for _, event := range eventList.Items {
		// sample event.Reason -- reason: 'policy: calamari/policy-grc-rbactest-example'
		reason := rgx.FindString(event.Reason)
		if event.InvolvedObject.Kind == policiesv1.Kind && event.InvolvedObject.APIVersion == policiesv1APIVersion &&
			event.InvolvedObject.Name == instance.GetName() && reason != "" {
			templateName := rgx.FindStringSubmatch(event.Reason)[2]
			eventHistory := policiesv1.ComplianceHistory{
				LastTimestamp: event.LastTimestamp,
				Message:       event.Message,
				EventName:     event.GetName(),
			}
			if eventForPolicyMap[templateName] == nil {
				eventForPolicyMap[templateName] = &[]policiesv1.ComplianceHistory{}
			}
			templateEvents := append(*eventForPolicyMap[templateName], eventHistory)
			eventForPolicyMap[templateName] = &templateEvents
		}
	}
	newStatus := policiesv1.PolicyStatus{}
	for _, policyT := range instance.Spec.PolicyTemplates {
		object, _, err := unstructured.UnstructuredJSONScheme.Decode(policyT.ObjectDefinition.Raw, nil, nil)
		if err != nil {
			// failed to decode PolicyTemplate, skipping it
			break
		}
		tName := object.(metav1.Object).GetName()
		var existingDpt = &policiesv1.DetailsPerTemplate{}
		// retrieve existingDpt from instance.status.details field
		found := false
		for _, dpt := range instance.Status.Details {
			if dpt.TemplateMeta.Name == tName {
				// found existing status for curently policyTemplate
				// retreive it
				existingDpt = dpt
				found = true
				break
			}
		}
		// no dpt from status field, intialzie it
		if !found {
			existingDpt = &policiesv1.DetailsPerTemplate{
				TemplateMeta: metav1.ObjectMeta{
					Name: tName,
				},
				History: []policiesv1.ComplianceHistory{},
			}
		}

		history := []policiesv1.ComplianceHistory{}
		if eventForPolicyMap[tName] != nil {
			history = *eventForPolicyMap[tName]
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
			// doesn't exists, append to history
			if !exists {
				history = append(history, ech)
			}
		}
		// sort by lasttimestamp
		sort.Slice(history, func(i, j int) bool {
			return history[i].LastTimestamp.Time.After(history[j].LastTimestamp.Time)
		})
		// remove duplicates
		newHistory := []policiesv1.ComplianceHistory{}
		for i := 0; i < len(history); i++ {
			newHistory = append(newHistory, history[i])
			for j := i; j < len(history); j++ {
				if history[i].EventName == history[j].EventName &&
					history[i].Message == history[j].Message {
					// same event, filter it
				} else {
					i = j - 1
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
			s := strings.Split(existingDpt.History[0].Message, ";")
			if len(s) > 1 && strings.ToLower(strings.TrimSpace(
				strings.TrimPrefix(s[0], "(combined from similar events):"))) == "compliant" {
				existingDpt.ComplianceState = policiesv1.Compliant
			} else {
				existingDpt.ComplianceState = policiesv1.NonCompliant
			}
		}
		// append existingDpt to status
		newStatus.Details = append(newStatus.Details, existingDpt)
		reqLogger.Info("status update complete... ", "PolicyTemplate", tName)
	}

	instance.Status = newStatus
	// one violation found in status of one template, set overall compliancy to NonCompliant
	for _, dpt := range newStatus.Details {
		if dpt.ComplianceState == "NonCompliant" || dpt.ComplianceState == "" {
			instance.Status.ComplianceState = policiesv1.NonCompliant
			break
		}
		instance.Status.ComplianceState = policiesv1.Compliant
	}

	// all done, update status on managed and hub
	// instance.Status.Details = nil
	err = r.managedClient.Status().Update(context.TODO(), instance)
	if err != nil {
		reqLogger.Error(err, "Failed to get update policy status on managed")
		return reconcile.Result{}, err
	}
	hubPlc.Status = instance.Status
	err = r.hubClient.Status().Update(context.TODO(), hubPlc)
	if err != nil {
		reqLogger.Error(err, "Failed to get update policy status on hub")
		return reconcile.Result{}, err
	}
	reqLogger.Info("Reconciling complete...")
	return reconcile.Result{}, nil
}
