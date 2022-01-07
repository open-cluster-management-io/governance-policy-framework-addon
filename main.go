// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	"github.com/spf13/pflag"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	synccontrollers "github.com/open-cluster-management/governance-policy-template-sync/controllers"
	"github.com/open-cluster-management/governance-policy-template-sync/version"
)

var (
	log    = ctrl.Log.WithName("setup")
	scheme = k8sruntime.NewScheme()
)

func printVersion() {
	log.Info(
		"Using",
		"OperatorVersion", version.Version,
		"GoVersion", runtime.Version(),
		"GOOS", runtime.GOOS,
		"GOARCH", runtime.GOARCH,
	)
}

func init() {
	utilruntime.Must(policiesv1.AddToScheme(scheme))
}

func main() {
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	var enableLeaderElection, legacyLeaderElect bool

	pflag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	pflag.BoolVar(&legacyLeaderElect, "legacy-leader-elect", false,
		"Use a legacy leader election method for controller manager instead of the lease API.")

	pflag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	printVersion()

	namespace, err := getWatchNamespace()
	if err != nil {
		log.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}

	// Get a config to talk to the apiserver
	managedCfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "Failed to get the Kubernetes configuration")
		os.Exit(1)
	}

	// Set default manager options
	options := manager.Options{
		Scheme:             scheme,
		Namespace:          namespace,
		MetricsBindAddress: "0",
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "governance-policy-template-sync.open-cluster-management.io",
	}

	// Add support for MultiNamespace set in WATCH_NAMESPACE (e.g ns1,ns2)
	// Note that this is not intended to be used for excluding namespaces, this is better done via a Predicate
	// Also note that you may face performance issues when using this with a high number of namespaces.
	// More Info: https://godoc.org/github.com/kubernetes-sigs/controller-runtime/pkg/cache#MultiNamespacedCacheBuilder
	if strings.Contains(namespace, ",") {
		options.Namespace = ""
		options.NewCache = cache.MultiNamespacedCacheBuilder(strings.Split(namespace, ","))
	}

	if legacyLeaderElect {
		// If legacyLeaderElection is enabled, then that means the lease API is not available.
		// In this case, use the legacy leader election method of a ConfigMap.
		options.LeaderElectionResourceLock = "configmaps"
	}

	// Create a new manager to provide shared dependencies and start components
	mgr, err := manager.New(managedCfg, options)
	if err != nil {
		log.Error(err, "Unable to start the manager")
		os.Exit(1)
	}

	log.Info("Registering components")

	// Setup all Controllers
	if err := (&synccontrollers.PolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Config:   mgr.GetConfig(),
		Recorder: mgr.GetEventRecorderFor(synccontrollers.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "Unable to create the controller", "controller", synccontrollers.ControllerName)
		os.Exit(1)
	}

	log.Info("Starting the manager")

	// Start the Cmd
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Error(err, "Errored while running the manager")
		os.Exit(1)
	}
}

// WatchNamespaceEnvVar is the constant for env variable WATCH_NAMESPACE
// which specifies the Namespace to watch.
// An empty value means the operator is running with cluster scope.
const watchNamespaceEnvVar = "WATCH_NAMESPACE"

// GetWatchNamespace returns the Namespace the operator should be watching for changes
func getWatchNamespace() (string, error) {
	ns, found := os.LookupEnv(watchNamespaceEnvVar)
	if !found {
		return "", fmt.Errorf("%s must be set", watchNamespaceEnvVar)
	}

	return ns, nil
}
