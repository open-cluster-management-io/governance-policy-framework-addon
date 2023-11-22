package uninstall

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/go-logr/zapr"
	"github.com/spf13/pflag"
	"github.com/stolostron/go-log-utils/zaputil"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	deploymentName      string
	deploymentNamespace string
	policyNamespace     string
	timeoutSeconds      uint
)

var triggerLog = ctrl.Log.WithName("trigger-uninstall")

const AnnotationKey = "policy.open-cluster-management.io/uninstalling"

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=deletecollection;

// Trigger adds the uninstallation annotation to the Deployment, then deletes all the policies.
// It will return nil only when all the policies are gone.
// It takes command line arguments to configure itself.
func Trigger(args []string) error {
	if err := parseUninstallFlags(args); err != nil {
		return err
	}

	triggerLog.Info("Triggering uninstallation preparation")

	terminatingCtx := ctrl.SetupSignalHandler()
	ctx, cancelCtx := context.WithTimeout(terminatingCtx, time.Duration(timeoutSeconds)*time.Second)

	defer cancelCtx()

	// Get a config to talk to the apiserver
	config, err := config.GetConfig()
	if err != nil {
		return err
	}

	client := kubernetes.NewForConfigOrDie(config)
	dynamicClient := dynamic.NewForConfigOrDie(config)

	err = setUninstallAnnotation(ctx, client)
	if err != nil {
		return err
	}

	// Give the running controllers some time to switch to uninstall mode,
	// to try and reduce the number of conflicts and retries while deleting policies.
	time.Sleep(5 * time.Second)

	err = deletePolicies(ctx, dynamicClient)
	if err != nil {
		return err
	}

	triggerLog.Info("Uninstallation preparation complete")

	return nil
}

func parseUninstallFlags(args []string) error {
	triggerUninstallFlagSet := pflag.NewFlagSet("trigger-uninstall", pflag.ExitOnError)

	triggerUninstallFlagSet.StringVar(
		&deploymentName,
		"deployment-name",
		"governance-policy-framework-addon",
		"The name of the controller Deployment object",
	)
	triggerUninstallFlagSet.StringVar(
		&deploymentNamespace,
		"deployment-namespace",
		"open-cluster-management-agent-addon",
		"The namespace of the controller Deployment object",
	)
	triggerUninstallFlagSet.StringVar(
		&policyNamespace, "policy-namespace", "", "The namespace where the Policy objects are stored",
	)
	triggerUninstallFlagSet.UintVar(
		&timeoutSeconds, "timeout-seconds", 300, "The number of seconds before the operation is canceled",
	)
	triggerUninstallFlagSet.AddGoFlagSet(flag.CommandLine)

	err := triggerUninstallFlagSet.Parse(args)
	if err != nil {
		return err
	}

	zflags := zaputil.FlagConfig{
		LevelName:   "log-level",
		EncoderName: "log-encoder",
	}

	zflags.Bind(flag.CommandLine)
	klog.InitFlags(flag.CommandLine)

	ctrlZap, err := zflags.BuildForCtrl()
	if err != nil {
		panic(fmt.Sprintf("Failed to build zap logger for controller: %v", err))
	}

	ctrl.SetLogger(zapr.NewLogger(ctrlZap))

	klogZap, err := zaputil.BuildForKlog(zflags.GetConfig(), flag.CommandLine)
	if err != nil {
		triggerLog.Error(err, "Failed to build zap logger for klog, those logs will not go through zap")
	} else {
		klog.SetLogger(zapr.NewLogger(klogZap).WithName("klog"))
	}

	if deploymentName == "" || deploymentNamespace == "" || policyNamespace == "" {
		return errors.New("--deployment-name, --deployment-namespace, --policy-namespace must all have values")
	}

	if timeoutSeconds < 30 {
		return errors.New("--timeout-seconds must be set to at least 30 seconds")
	}

	return nil
}

func setUninstallAnnotation(ctx context.Context, client *kubernetes.Clientset) error {
	triggerLog.Info("Annotating the deployment with " + AnnotationKey + " = true")

	for {
		var err error

		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled before the uninstallation preparation was complete")
		default:
		}

		deploymentRsrc := client.AppsV1().Deployments(deploymentNamespace)

		deployment, err := deploymentRsrc.Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		annotations := deployment.GetAnnotations()
		annotations[AnnotationKey] = "true"
		deployment.SetAnnotations(annotations)

		_, err = deploymentRsrc.Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			if k8serrors.IsServerTimeout(err) || k8serrors.IsTimeout(err) || k8serrors.IsConflict(err) {
				triggerLog.Error(err, "Retrying setting the Deployment uninstall annotation due to error")

				continue
			}

			return err
		}

		break
	}

	return nil
}

func deletePolicies(ctx context.Context, dynamicClient dynamic.Interface) error {
	policyGVR := schema.GroupVersionResource{
		Group:    policyv1.GroupVersion.Group,
		Version:  policyv1.GroupVersion.Version,
		Resource: "policies",
	}

	triggerLog.Info("Deleting all policies from the cluster")

	policyInterface := dynamicClient.Resource(policyGVR).Namespace(policyNamespace)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled before the uninstallation preparation was complete")
		default:
		}

		policies, err := policyInterface.List(ctx, metav1.ListOptions{})
		if err != nil {
			if k8serrors.IsServerTimeout(err) || k8serrors.IsTimeout(err) {
				triggerLog.Error(err, "Unable to list policy objects, retrying.")

				continue
			}

			return err
		}

		switch len(policies.Items) {
		case 0:
			triggerLog.Info("No policies found.")

			return nil
		case 1:
			triggerLog.Info("Found 1 policy.")
		default:
			triggerLog.Info(fmt.Sprintf("Found %v policies.", len(policies.Items)))
		}

		err = policyInterface.DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{})
		if err != nil {
			triggerLog.Error(err, "Unable to delete all policies. Will retry.")
		}

		triggerLog.Info("The uninstall preparation is not complete. Sleeping two seconds before checking again.")
		time.Sleep(2 * time.Second)
	}
}
