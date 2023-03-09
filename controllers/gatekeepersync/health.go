// Copyright Contributors to the Open Cluster Management project

package gatekeepersync

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiWatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/watch"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

// GatekeeperInstallationChecker is a health checker for a health endpoint that fails if Gatekeeper's installation
// status changes or the passed in health checker functions fail. This is useful for Kubernetes to trigger a restart
// to either enable or disable the gatekeeper-constraint-status-sync controller based on the Gatekeeper installation
// status.
func GatekeeperInstallationChecker(
	ctx context.Context, dynamicClient dynamic.Interface, checkers ...healthz.Checker,
) (
	healthz.Checker, bool, error,
) {
	fieldSelector := fmt.Sprintf("metadata.name=constrainttemplates.%s", utils.GvkConstraintTemplate.Group)
	timeout := int64(30)
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}

	listResult, err := dynamicClient.Resource(crdGVR).List(
		ctx, metav1.ListOptions{FieldSelector: fieldSelector, TimeoutSeconds: &timeout},
	)
	if err != nil {
		return nil, false, err
	}

	gatekeeperInstalled := len(listResult.Items) > 0
	resourceVersion := listResult.GetResourceVersion()

	watchFunc := func(options metav1.ListOptions) (apiWatch.Interface, error) {
		options.FieldSelector = fieldSelector

		return dynamicClient.Resource(crdGVR).Watch(ctx, options)
	}

	w, err := watch.NewRetryWatcher(resourceVersion, &cache.ListWatch{WatchFunc: watchFunc})
	if err != nil {
		return nil, false, err
	}

	var lastHealthErr error

	return func(req *http.Request) error {
		if lastHealthErr != nil {
			return lastHealthErr
		}

		select {
		case <-w.Done():
			select {
			case <-ctx.Done():
				// Stop the retry watcher if the parent context is canceled.
				w.Stop()

				lastHealthErr = errors.New("the context is closed so the health check can no longer be performed")
			default:
				lastHealthErr = errors.New("the watch used by the Gatekeeper installation checker ended prematurely")
			}
		case result := <-w.ResultChan():
			if !gatekeeperInstalled && result.Type == apiWatch.Added {
				lastHealthErr = errors.New("the controller needs to restart because Gatekeeper has been installed")
			}

			if gatekeeperInstalled && result.Type == apiWatch.Deleted {
				lastHealthErr = errors.New("the controller needs to restart because Gatekeeper has been uninstalled")
			}
		default:
			for _, checker := range checkers {
				err := checker(req)
				if err != nil {
					return err
				}
			}
		}

		return lastHealthErr
	}, gatekeeperInstalled, nil
}
