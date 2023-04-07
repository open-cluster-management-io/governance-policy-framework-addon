package uninstall

import (
	"context"

	"github.com/stolostron/kubernetes-dependency-watches/client"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"open-cluster-management.io/governance-policy-framework-addon/tool"
)

const (
	ControllerName = "uninstall-watcher"
)

var (
	log                      = logf.Log.WithName(ControllerName)
	DeploymentIsUninstalling = false
)

type reconciler struct {
	*kubernetes.Clientset
}

// +kubebuilder:rbac:groups=apps,resources=deployments,resourceNames=governance-policy-framework-addon,verbs=get;list;watch;patch;update

func (r *reconciler) Reconcile(ctx context.Context, obj client.ObjectIdentifier) (reconcile.Result, error) {
	log := log.WithValues("name", obj.Name, "namespace", obj.Namespace)

	if DeploymentIsUninstalling {
		log.Info("Skipping reconcile because the uninstall flag is already true. " +
			"To reset the flag, stop this container.")

		return reconcile.Result{}, nil
	}

	log.Info("Checking the deployment for the annotation " + AnnotationKey)

	deployment, err := r.AppsV1().Deployments(obj.Namespace).Get(ctx, obj.Name, v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "The deployment was not found. Assuming this means we are being uninstalled.")

			DeploymentIsUninstalling = true

			return reconcile.Result{}, nil
		}

		log.Error(err, "Error getting deployment, requeueing")

		return reconcile.Result{}, err
	}

	if deployment.GetAnnotations()[AnnotationKey] == "true" {
		log.Info("Annotation " + AnnotationKey + " found and was true. Setting the uninstall flag.")

		DeploymentIsUninstalling = true

		return reconcile.Result{}, nil
	}

	log.Info("Annotation " + AnnotationKey + " not found, or not true. No changes.")

	return reconcile.Result{}, nil
}

// StartWatcher starts the uninstall watcher, which watches the controller's Deployment so that when
// the uninstallation annotation is present, the global uninstallation flag will be set to true.
func StartWatcher(ctx context.Context, mgr manager.Manager, namespace string) error {
	config := mgr.GetConfig()

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	// Create the dynamic watcher.
	dynamicWatcher, err := client.New(config, &reconciler{clientset}, nil)
	if err != nil {
		return err
	}

	watcherErr := make(chan error)

	// Start the dynamic watcher in a separate goroutine
	go func() {
		err := dynamicWatcher.Start(ctx)
		if err != nil {
			watcherErr <- err
		}
	}()

	// Wait until the dynamic watcher has started.
	select {
	case <-dynamicWatcher.Started():
		break
	case watchErr := <-watcherErr:
		return watchErr
	}

	self := client.ObjectIdentifier{
		Group:     "apps",
		Version:   "v1",
		Kind:      "Deployment",
		Namespace: namespace,
		Name:      tool.Options.DeploymentName,
	}

	err = dynamicWatcher.AddOrUpdateWatcher(self, self)
	if err != nil {
		return err
	}

	// Run until the context is canceled.
	select {
	case <-ctx.Done():
		break
	case watchErr := <-watcherErr:
		return watchErr
	}

	return nil
}
