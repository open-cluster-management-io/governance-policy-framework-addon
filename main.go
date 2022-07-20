// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/go-logr/zapr"
	"github.com/spf13/pflag"
	"github.com/stolostron/go-log-utils/zaputil"

	// to ensure that exec-entrypoint and run can make use of them.
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	addonutils "open-cluster-management.io/addon-framework/pkg/utils"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	//+kubebuilder:scaffold:imports
	"open-cluster-management.io/governance-policy-spec-sync/controllers/secretsync"
	"open-cluster-management.io/governance-policy-spec-sync/controllers/sync"
	"open-cluster-management.io/governance-policy-spec-sync/tool"
	"open-cluster-management.io/governance-policy-spec-sync/version"
)

// Change below variables to serve metrics on different host or port.
var (
	metricsHost       = "0.0.0.0"
	metricsPort int32 = 8384
)

var (
	eventsScheme = k8sruntime.NewScheme()
	scheme       = k8sruntime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(eventsScheme))
	//+kubebuilder:scaffold:scheme
	utilruntime.Must(policiesv1.AddToScheme(scheme))
	utilruntime.Must(policiesv1.AddToScheme(eventsScheme))
}

func main() {
	zflags := zaputil.FlagConfig{
		LevelName:   "log-level",
		EncoderName: "log-encoder",
	}

	zflags.Bind(flag.CommandLine)
	klog.InitFlags(flag.CommandLine)
	tool.ProcessFlags()
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	ctrlZap, err := zflags.BuildForCtrl()
	if err != nil {
		panic(fmt.Sprintf("Failed to build zap logger for controller: %v", err))
	}

	ctrl.SetLogger(zapr.NewLogger(ctrlZap))
	setupLog := ctrl.Log.WithName("setup")

	klogZap, err := zaputil.BuildForKlog(zflags.GetConfig(), flag.CommandLine)
	if err != nil {
		setupLog.Error(err, "Failed to build zap logger for klog, those logs will not go through zap")
	} else {
		klog.SetLogger(zapr.NewLogger(klogZap).WithName("klog"))
	}

	setupLog.Info("Using", "OperatorVersion", version.Version, "GoVersion", runtime.Version(),
		"GOOS", runtime.GOOS, "GOARCH", runtime.GOARCH)

	namespace, err := tool.GetWatchNamespace()
	if err != nil {
		setupLog.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}

	// Get hubconfig to talk to hub apiserver
	if tool.Options.HubConfigFilePathName == "" {
		var found bool
		tool.Options.HubConfigFilePathName, found = os.LookupEnv("HUB_CONFIG")

		if found {
			setupLog.Info("Found ENV HUB_CONFIG, initializing using", "tool.Options.HubConfigFilePathName",
				tool.Options.HubConfigFilePathName)
		}
	}

	hubCfg, err := clientcmd.BuildConfigFromFlags("", tool.Options.HubConfigFilePathName)
	if err != nil {
		setupLog.Error(err, "Unable to build hub config from flags")
		os.Exit(1)
	}

	// Get managedconfig to talk to hub apiserver
	var managedCfg *rest.Config

	if tool.Options.ManagedConfigFilePathName == "" {
		var found bool
		tool.Options.ManagedConfigFilePathName, found = os.LookupEnv("MANAGED_CONFIG")

		if found {
			setupLog.Info("Found ENV MANAGED_CONFIG, initializing using", "tool.Options.ManagedConfigFilePathName",
				tool.Options.ManagedConfigFilePathName)

			managedCfg, err = clientcmd.BuildConfigFromFlags("", tool.Options.ManagedConfigFilePathName)

			if err != nil {
				setupLog.Error(err, "Unable to build managed config from flags")
				os.Exit(1)
			}
		} else {
			managedCfg, err = config.GetConfig()
			if err != nil {
				setupLog.Error(err, "Unable to get managed config")
				os.Exit(1)
			}
		}
	}

	managedClient, err := client.New(managedCfg, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "Failed to generate client to the managed cluster")
		os.Exit(1)
	}

	var kubeClient kubernetes.Interface = kubernetes.NewForConfigOrDie(managedCfg)

	var targetNamespace string
	if tool.Options.TargetNamespace == "" {
		targetNamespace = namespace
	} else {
		targetNamespace = tool.Options.TargetNamespace
		setupLog.Info(
			"Replicated policies from the Hub will be copied to the input target namespace",
			"namespace",
			targetNamespace,
		)
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events(targetNamespace)})
	managedRecorder := eventBroadcaster.NewRecorder(eventsScheme, v1.EventSource{Component: sync.ControllerName})

	// Set a field selector so that a watch on secrets will be limited to just the secret with the policy template
	// encryption key.
	newCacheFunc := cache.BuilderWithOptions(
		cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				&v1.Secret{}: {
					Field: fields.SelectorFromSet(fields.Set{"metadata.name": secretsync.SecretName}),
				},
			},
		},
	)

	// Set default manager options
	options := manager.Options{
		Scheme:                 scheme,
		Namespace:              namespace,
		MetricsBindAddress:     fmt.Sprintf("%s:%d", metricsHost, metricsPort),
		HealthProbeBindAddress: tool.Options.ProbeAddr,
		LeaderElection:         tool.Options.EnableLeaderElection,
		LeaderElectionID:       "policy-spec-sync.open-cluster-management.io",
		// Override LeaderElectionConfig to managed cluster config.
		// Otherwise it will improperly use the hub cluster config for leader election.
		LeaderElectionConfig: managedCfg,
		NewCache:             newCacheFunc,
	}

	if tool.Options.LegacyLeaderElection {
		// If legacyLeaderElection is enabled, then that means the lease API is not available.
		// In this case, use the legacy leader election method of a ConfigMap.
		options.LeaderElectionResourceLock = "configmaps"
	}

	// Add support for MultiNamespace set in WATCH_NAMESPACE (e.g ns1,ns2)
	// Note that this is not intended to be used for excluding namespaces, this is better done via a Predicate
	// Also note that you may face performance issues when using this with a high number of namespaces.
	// More Info: https://godoc.org/github.com/kubernetes-sigs/controller-runtime/pkg/cache#MultiNamespacedCacheBuilder
	if strings.Contains(namespace, ",") {
		options.Namespace = ""
		options.NewCache = cache.MultiNamespacedCacheBuilder(strings.Split(namespace, ","))
	}

	// Create a new manager to provide shared dependencies and start components
	mgr, err := ctrl.NewManager(hubCfg, options)
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	setupLog.Info("Registering Components.")

	// Setup all Controllers
	if err = (&sync.PolicyReconciler{
		HubClient:       mgr.GetClient(),
		ManagedClient:   managedClient,
		ManagedRecorder: managedRecorder,
		Scheme:          mgr.GetScheme(),
		TargetNamespace: targetNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Unable to create the controller", "controller", sync.ControllerName)
		os.Exit(1)
	}

	if err = (&secretsync.SecretReconciler{
		Client:          mgr.GetClient(),
		ManagedClient:   managedClient,
		Scheme:          mgr.GetScheme(),
		TargetNamespace: targetNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Unable to create the controller", "controller", secretsync.ControllerName)
		os.Exit(1)
	}

	// use config check
	configChecker, err := addonutils.NewConfigChecker("policy-spec-sync", tool.Options.HubConfigFilePathName)
	if err != nil {
		setupLog.Error(err, "unable to setup a configChecker")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder
	if err := mgr.AddHealthzCheck("healthz", configChecker.Check); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager.")

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Manager exited non-zero")
		os.Exit(1)
	}
}
