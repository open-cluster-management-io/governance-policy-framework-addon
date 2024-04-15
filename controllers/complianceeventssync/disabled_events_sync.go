package complianceeventssync

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiWatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	apiCache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/watch"
	"k8s.io/client-go/util/workqueue"
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

var (
	log       = ctrl.Log.WithName("disabled-events-recorder")
	GVRPolicy = schema.GroupVersionResource{
		Group:    "policy.open-cluster-management.io",
		Version:  "v1",
		Resource: "policies",
	}
)

// DisabledEventsRecorder watches for deleted policies and records disabled compliance events on the compliance history
// API. Set initialResourceVersion to the list query of policies in the managed cluster namespace from before the
// spec-sync starting to avoid missing events.
func DisabledEventsRecorder(
	ctx context.Context,
	managedClient dynamic.Interface,
	clusterNamespace string,
	// EventsQueue is a queue that accepts ComplianceAPIEventRequest to then be recorded in the compliance events
	// API by StartComplianceEventsSyncer.
	eventsQueue workqueue.RateLimitingInterface,
	initialResourceVersion string,
) {
	timeout := int64(30)
	var watcher *watch.RetryWatcher

	resourceVersion := initialResourceVersion

	for {
		if watcher == nil {
			if resourceVersion == "" {
				listResult, err := managedClient.Resource(GVRPolicy).Namespace(clusterNamespace).List(
					ctx, metav1.ListOptions{TimeoutSeconds: &timeout},
				)
				if err != nil {
					log.Error(err, "Failed to list the policies for recording disabled events. Will retry.")

					time.Sleep(time.Second)

					continue
				}

				resourceVersion = listResult.GetResourceVersion()
			}

			watchFunc := func(options metav1.ListOptions) (apiWatch.Interface, error) {
				return managedClient.Resource(GVRPolicy).Namespace(clusterNamespace).Watch(ctx, options)
			}

			var err error

			watcher, err = watch.NewRetryWatcher(resourceVersion, &apiCache.ListWatch{WatchFunc: watchFunc})
			if err != nil {
				log.Error(err, "Failed to watch the policies for recording disabled events. Will retry.")

				time.Sleep(time.Second)

				continue
			}

			// Set the resourceVersion to an empty string after a successful start of the watcher so that if the
			// watcher unexpectedly stops, it will just start from the latest.
			resourceVersion = ""
		}

		select {
		case <-ctx.Done():
			// Stop the retry watcher if the parent context is canceled. It likely already is stopped, but this is not
			// documented behavior.
			watcher.Stop()

			return
		case <-watcher.Done():
			// Restart the watcher on the next loop since the context wasn't closed which indicates it was not stopped
			// on purpose.
			watcher = nil
		case result := <-watcher.ResultChan():
			if result.Type != apiWatch.Deleted {
				break
			}

			unstructuredPolicy, ok := result.Object.(*unstructured.Unstructured)
			if !ok {
				break
			}

			managedPolicy := &policyv1.Policy{}

			err := k8sruntime.DefaultUnstructuredConverter.FromUnstructured(unstructuredPolicy.Object, managedPolicy)
			if err != nil {
				log.Error(err, "Failed to convert the unstructured object to a typed policy")

				break
			}

			for _, tmplEntry := range managedPolicy.Spec.PolicyTemplates {
				tmpl := &unstructured.Unstructured{}

				err := tmpl.UnmarshalJSON(tmplEntry.ObjectDefinition.Raw)
				if err != nil {
					continue
				}

				if tmpl.GetAnnotations()[utils.PolicyDBIDAnnotation] == "" {
					continue
				}

				ce, err := utils.GenerateDisabledEvent(
					managedPolicy,
					tmpl,
					"The policy was removed because the parent policy no longer applies to this cluster",
				)
				if err != nil {
					log.Error(err, "Failed to generate a disabled compliance API event")
				} else {
					eventsQueue.Add(ce)
				}
			}
		}
	}
}
