// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	v1 "k8s.io/api/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	addonutils "open-cluster-management.io/addon-framework/pkg/utils"

	"github.com/open-cluster-management/addon-framework/pkg/lease"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	"github.com/spf13/pflag"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/open-cluster-management/governance-policy-status-sync/controllers/sync"
	"github.com/open-cluster-management/governance-policy-status-sync/tool"
	"github.com/open-cluster-management/governance-policy-status-sync/version"
	//+kubebuilder:scaffold:imports
)

var (
	eventsScheme = k8sruntime.NewScheme()
	log          = logf.Log.WithName("setup")
	scheme       = k8sruntime.NewScheme()
)

func printVersion() {
	log.Info(fmt.Sprintf("Operator Version: %s", version.Version))
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(eventsScheme))

	utilruntime.Must(policiesv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	// custom flags for the controler
	tool.ProcessFlags()

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	pflag.Parse()

	logf.SetLogger(zap.New())

	printVersion()

	// Get hubconfig to talk to hub apiserver
	if tool.Options.HubConfigFilePathName == "" {
		found := false
		tool.Options.HubConfigFilePathName, found = os.LookupEnv("HUB_CONFIG")
		if found {
			log.Info("Found ENV HUB_CONFIG, initializing using", "tool.Options.HubConfigFilePathName",
				tool.Options.HubConfigFilePathName)
		}
	}

	hubCfg, err := clientcmd.BuildConfigFromFlags("", tool.Options.HubConfigFilePathName)

	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Get managedconfig to talk to managed apiserver
	var managedCfg *rest.Config
	if tool.Options.ManagedConfigFilePathName == "" {
		found := false
		tool.Options.ManagedConfigFilePathName, found = os.LookupEnv("MANAGED_CONFIG")
		if found {
			log.Info("Found ENV MANAGED_CONFIG, initializing using", "tool.Options.ManagedConfigFilePathName",
				tool.Options.ManagedConfigFilePathName)
			managedCfg, err = clientcmd.BuildConfigFromFlags("", tool.Options.ManagedConfigFilePathName)
			if err != nil {
				log.Error(err, "")
				os.Exit(1)
			}
		} else {
			managedCfg, err = config.GetConfig()
			if err != nil {
				log.Error(err, "")
				os.Exit(1)
			}
		}
	}

	hubClient, err := client.New(hubCfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "Failed to generate client to the hub cluster")
		os.Exit(1)
	}
	var kubeClient kubernetes.Interface = kubernetes.NewForConfigOrDie(hubCfg)

	eventBroadcaster := record.NewBroadcaster()
	namespace, err := tool.GetWatchNamespace()
	if err != nil {
		log.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}
	eventBroadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events(namespace)})
	hubRecorder := eventBroadcaster.NewRecorder(eventsScheme, v1.EventSource{Component: sync.ControllerName})

	options := manager.Options{
		LeaderElection:         tool.Options.EnableLeaderElection,
		LeaderElectionID:       "policy-status-sync.open-cluster-management.io",
		HealthProbeBindAddress: tool.Options.ProbeAddr,
		// Disable the metrics endpoint
		MetricsBindAddress: "0",
		Namespace:          namespace,
		Scheme:             scheme,
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

	mgr, err := ctrl.NewManager(managedCfg, options)
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&sync.PolicyReconciler{
		HubClient:       hubClient,
		HubRecorder:     hubRecorder,
		ManagedClient:   mgr.GetClient(),
		ManagedRecorder: mgr.GetEventRecorderFor(sync.ControllerName),
		Scheme:          mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create controller", "controller", "Policy")
		os.Exit(1)
	}

	// use config check
	cc, err := addonutils.NewConfigChecker("policy-status-sync", tool.Options.HubConfigFilePathName)
	if err != nil {
		log.Error(err, "unable to setup a configChecker")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder
	if err := mgr.AddHealthzCheck("healthz", cc.Check); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// create namespace with labels
	var generatedClient kubernetes.Interface = kubernetes.NewForConfigOrDie(managedCfg)
	if err := tool.CreateClusterNs(&generatedClient, namespace); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// This lease is not related to leader election. This is to report the status of the controller
	// to the addon framework. This can be seen in the "status" section of the ManagedClusterAddOn
	// resource objects.
	if tool.Options.EnableLease {
		ctx := context.TODO()
		operatorNs, err := tool.GetOperatorNamespace()
		if err != nil {
			if err == tool.ErrNoNamespace || err == tool.ErrRunLocal {
				log.Info("Skipping lease; not running in a cluster.")
			} else {
				log.Error(err, "Failed to get operator namespace")
				os.Exit(1)
			}
		} else {
			log.Info("Starting lease controller to report status")
			leaseUpdater := lease.NewLeaseUpdater(
				generatedClient,
				"policy-controller",
				operatorNs,
				lease.CheckAddonPodFunc(generatedClient.CoreV1(), operatorNs, "app=policy-framework"),
				// this additional CheckAddonPodFunc is temporary until the
				// addon framework independently verifies the config-policy-controller via its lease
				// see https://github.com/open-cluster-management/backlog/issues/11508
				lease.CheckAddonPodFunc(generatedClient.CoreV1(), operatorNs, "app=policy-config-policy"),
			)
			go leaseUpdater.Start(ctx)
		}
	} else {
		log.Info("Status reporting is not enabled")
	}

	log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}
