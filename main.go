// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/zapr"
	gktemplatesv1 "github.com/open-policy-agent/frameworks/constraint/pkg/apis/templates/v1"
	gktemplatesv1beta1 "github.com/open-policy-agent/frameworks/constraint/pkg/apis/templates/v1beta1"
	"github.com/spf13/pflag"
	"github.com/stolostron/go-log-utils/zaputil"
	depclient "github.com/stolostron/kubernetes-dependency-watches/client"
	"golang.org/x/mod/semver"
	admissionregistration "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	extensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"open-cluster-management.io/addon-framework/pkg/lease"
	addonutils "open-cluster-management.io/addon-framework/pkg/utils"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/gatekeepersync"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/secretsync"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/specsync"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/statussync"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/templatesync"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/uninstall"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
	"open-cluster-management.io/governance-policy-framework-addon/tool"
	"open-cluster-management.io/governance-policy-framework-addon/version"
)

var (
	eventsScheme = k8sruntime.NewScheme()
	log          = ctrl.Log.WithName("setup")
	scheme       = k8sruntime.NewScheme()
	eventFilter  fields.Selector
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
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(eventsScheme))
	//+kubebuilder:scaffold:scheme
	utilruntime.Must(policiesv1.AddToScheme(scheme))
	utilruntime.Must(policiesv1.AddToScheme(eventsScheme))
	utilruntime.Must(extensionsv1.AddToScheme(scheme))
	utilruntime.Must(extensionsv1beta1.AddToScheme(scheme))
	utilruntime.Must(gktemplatesv1.AddToScheme(scheme))
	utilruntime.Must(gktemplatesv1beta1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))

	// Filter out events not related to policy compliance
	eventFilter = fields.ParseSelectorOrDie(
		`involvedObject.kind=Policy,` +
			`reason!="PolicySpecSync",` +
			`reason!="PolicyTemplateSync",` +
			`reason!="PolicyStatusSync"`,
	)
}

func main() {
	// specially handle the uninstall command, otherwise try to start the controllers.
	if len(os.Args) >= 2 && os.Args[1] == "trigger-uninstall" {
		if err := uninstall.Trigger(os.Args[2:]); err != nil {
			log.Error(err, "Failed to trigger uninstallation preparation")
			os.Exit(1)
		}

		return
	}

	zflags := zaputil.FlagConfig{
		LevelName:   "log-level",
		EncoderName: "log-encoder",
	}

	zflags.Bind(flag.CommandLine)
	klog.InitFlags(flag.CommandLine)

	// custom flags for the controller
	tool.ProcessFlags()

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	pflag.Parse()

	ctrlZap, err := zflags.BuildForCtrl()
	if err != nil {
		panic(fmt.Sprintf("Failed to build zap logger for controller: %v", err))
	}

	ctrl.SetLogger(zapr.NewLogger(ctrlZap))

	klogZap, err := zaputil.BuildForKlog(zflags.GetConfig(), flag.CommandLine)
	if err != nil {
		log.Error(err, "Failed to build zap logger for klog, those logs will not go through zap")
	} else {
		klog.SetLogger(zapr.NewLogger(klogZap).WithName("klog"))
	}

	printVersion()

	if tool.Options.ClusterNamespace == "" {
		log.Info("The --cluster-namespace flag must be provided")
		os.Exit(1)
	}

	if tool.Options.ClusterNamespaceOnHub == "" {
		tool.Options.ClusterNamespaceOnHub = tool.Options.ClusterNamespace
	}

	// Get hubconfig to talk to hub apiserver
	if tool.Options.HubConfigFilePathName == "" {
		var found bool

		tool.Options.HubConfigFilePathName, found = os.LookupEnv("HUB_CONFIG")
		if found {
			log.Info("Found ENV HUB_CONFIG, initializing using", "tool.Options.HubConfigFilePathName",
				tool.Options.HubConfigFilePathName)
		}
	}

	hubCfg, err := clientcmd.BuildConfigFromFlags("", tool.Options.HubConfigFilePathName)
	if err != nil {
		log.Error(err, "Failed to build hub cluster config")
		os.Exit(1)
	}

	// Get managedconfig to talk to managed apiserver
	var managedCfg *rest.Config

	if tool.Options.ManagedConfigFilePathName == "" {
		var found bool

		tool.Options.ManagedConfigFilePathName, found = os.LookupEnv("MANAGED_CONFIG")
		if found {
			log.Info("Found ENV MANAGED_CONFIG, initializing using", "tool.Options.ManagedConfigFilePathName",
				tool.Options.ManagedConfigFilePathName)

			managedCfg, err = clientcmd.BuildConfigFromFlags("", tool.Options.ManagedConfigFilePathName)
			if err != nil {
				log.Error(err, "Failed to build managed cluster config")
				os.Exit(1)
			}
		} else {
			managedCfg, err = config.GetConfig()
			if err != nil {
				log.Error(err, "Failed to build managed cluster config")
				os.Exit(1)
			}
		}
	}

	// Override default QPS (20) and Burst (30) to client configurations
	managedCfg.QPS = tool.Options.ClientQPS
	managedCfg.Burst = int(tool.Options.ClientBurst)

	mgrOptionsBase := manager.Options{
		LeaderElection: tool.Options.EnableLeaderElection,
		// Disable the metrics endpoint
		Metrics: server.Options{
			BindAddress: tool.Options.MetricsAddr,
		},
		Scheme: scheme,
		// Override the EventBroadcaster so that the spam filter will not ignore events for the policy but with
		// different messages if a large amount of events for that policy are sent in a short time.
		EventBroadcaster: record.NewBroadcasterWithCorrelatorOptions(
			record.CorrelatorOptions{
				// This essentially disables event aggregation of the same events but with different messages.
				MaxIntervalInSeconds: 1,
				// This is the default spam key function except it adds the reason and message as well.
				// https://github.com/kubernetes/client-go/blob/v0.23.3/tools/record/events_cache.go#L70-L82
				SpamKeyFunc: func(event *v1.Event) string {
					return strings.Join(
						[]string{
							event.Source.Component,
							event.Source.Host,
							event.InvolvedObject.Kind,
							event.InvolvedObject.Namespace,
							event.InvolvedObject.Name,
							string(event.InvolvedObject.UID),
							event.InvolvedObject.APIVersion,
							event.Reason,
							event.Message,
						},
						"",
					)
				},
			},
		),
	}

	// This lease is not related to leader election. This is to report the status of the controller
	// to the addon framework. This can be seen in the "status" section of the ManagedClusterAddOn
	// resource objects.
	if tool.Options.EnableLease {
		ctx := context.TODO()

		operatorNs, err := tool.GetOperatorNamespace()
		if err != nil {
			if errors.Is(err, tool.ErrNoNamespace) || errors.Is(err, tool.ErrRunLocal) {
				log.Info("Skipping lease; not running in a cluster.")
			} else {
				log.Error(err, "Failed to get operator namespace")
				os.Exit(1)
			}
		} else {
			log.Info("Starting lease controller to report status")
			generatedClient := kubernetes.NewForConfigOrDie(managedCfg)
			leaseUpdater := lease.NewLeaseUpdater(
				generatedClient, "governance-policy-framework", operatorNs,
			).WithHubLeaseConfig(hubCfg, tool.Options.ClusterNamespaceOnHub)
			go leaseUpdater.Start(ctx)
		}
	} else {
		log.Info("Status reporting is not enabled")
	}

	mgrHealthAddr, err := getFreeLocalAddr()
	if err != nil {
		log.Error(err, "Failed to get a free port for the health endpoint")
		os.Exit(1)
	}

	healthAddresses := []string{mgrHealthAddr}

	mainCtx := ctrl.SetupSignalHandler()
	mgrCtx, mgrCtxCancel := context.WithCancel(mainCtx)

	mgr := getManager(mgrCtx, mgrOptionsBase, mgrHealthAddr, managedCfg)

	var hubMgr manager.Manager

	if !tool.Options.DisableSpecSync {
		hubMgrHealthAddr, err := getFreeLocalAddr()
		if err != nil {
			log.Error(err, "Failed to get a free port for the health endpoint")
			os.Exit(1)
		}

		healthAddresses = append(healthAddresses, hubMgrHealthAddr)

		hubMgr = getHubManager(mgrOptionsBase, hubMgrHealthAddr, hubCfg, managedCfg)
	}

	log.Info("Adding controllers to managers")
	addControllers(mgrCtx, hubCfg, hubMgr, mgr)

	log.Info("Starting the controller managers")

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		err := startHealthProxy(mgrCtx, &wg, healthAddresses...)
		if err != nil {
			log.Error(err, "failed to start the health endpoint proxy")

			// On errors, the parent context (mainCtx) may not have closed, so cancel the child context.
			mgrCtxCancel()
		}
	}()

	var errorExit bool

	wg.Add(1)

	go func() {
		if err := mgr.Start(mgrCtx); err != nil {
			log.Error(err, "problem running manager")

			// On errors, the parent context (mainCtx) may not have closed, so cancel the child context.
			mgrCtxCancel()

			errorExit = true
		}

		wg.Done()
	}()

	operatorNs, err := tool.GetOperatorNamespace()

	if errors.Is(err, tool.ErrRunLocal) {
		log.Info("Using default operatorNs for the uninstall-watcher during this local run")

		operatorNs = "open-cluster-management-agent-addon"
		err = nil
	}

	if err != nil {
		log.Error(err, "Failed to get operator namespace")
		os.Exit(1)
	}

	wg.Add(1)

	go func() {
		if err := uninstall.StartWatcher(mgrCtx, mgr, operatorNs); err != nil {
			log.Error(err, "problem running uninstall-watcher")

			// On errors, the parent context (mainCtx) may not have closed, so cancel the child context.
			mgrCtxCancel()

			errorExit = true
		}

		wg.Done()
	}()

	if !tool.Options.DisableSpecSync {
		wg.Add(1)

		go func() {
			if err := hubMgr.Start(mgrCtx); err != nil {
				log.Error(err, "problem running hub manager")

				// On errors, the parent context (mainCtx) may not have closed, so cancel the child context.
				mgrCtxCancel()

				errorExit = true
			}

			wg.Done()
		}()
	}

	wg.Wait()

	if errorExit {
		os.Exit(1)
	}
}

// getManager return a controller Manager object that watches on the managed cluster and has the controllers registered.
func getManager(
	mgrCtx context.Context, options manager.Options, healthAddr string, managedCfg *rest.Config,
) manager.Manager {
	crdLabelSelector := labels.SelectorFromSet(map[string]string{utils.PolicyTypeLabel: "template"})

	options.LeaderElectionID = "governance-policy-framework-addon.open-cluster-management.io"
	options.HealthProbeBindAddress = healthAddr
	options.Cache = cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&extensionsv1.CustomResourceDefinition{}: {
				Label: crdLabelSelector,
			},
			&extensionsv1beta1.CustomResourceDefinition{}: {
				Label: crdLabelSelector,
			},
			&admissionregistration.ValidatingWebhookConfiguration{}: {
				Field: fields.SelectorFromSet(fields.Set{"metadata.name": gatekeepersync.GatekeeperWebhookName}),
			},
			&v1.Event{}: {
				Field: eventFilter,
				Transform: func(obj interface{}) (interface{}, error) {
					event := obj.(*v1.Event)
					// Only cache fields that are utilized by the controllers.
					guttedEvent := &v1.Event{
						InvolvedObject: event.InvolvedObject,
						TypeMeta:       event.TypeMeta,
						ObjectMeta: metav1.ObjectMeta{
							Name:      event.ObjectMeta.Name,
							Namespace: event.ObjectMeta.Namespace,
						},
						LastTimestamp: event.LastTimestamp,
						Message:       event.Message,
						Reason:        event.Reason,
						EventTime:     event.EventTime,
					}

					return guttedEvent, nil
				},
			},
			&v1.Secret{}: {
				Field: fields.SelectorFromSet(fields.Set{"metadata.name": secretsync.SecretName}),
			},
		},
		DefaultNamespaces: map[string]cache.Config{
			tool.Options.ClusterNamespace: {},
		},
	}

	mgr, err := ctrl.NewManager(managedCfg, options)
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// use config check
	configChecker, err := addonutils.NewConfigChecker(
		"governance-policy-framework-addon", tool.Options.HubConfigFilePathName,
	)
	if err != nil {
		log.Error(err, "unable to setup a configChecker")
		os.Exit(1)
	}

	healthCheck := configChecker.Check

	// Add Gatekeeper controller if enabled
	if !tool.Options.DisableGkSync {
		healthCheck = addGkControllerToManager(mgrCtx, mgr, managedCfg, configChecker.Check)
	} else {
		log.Info("The Gatekeeper integration is set to disabled")
	}

	//+kubebuilder:scaffold:builder
	if err := mgr.AddHealthzCheck("healthz", healthCheck); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	return mgr
}

// getHubManager return a controller Manager object that watches on the Hub and has the controllers registered.
func getHubManager(
	options manager.Options, healthAddr string, hubCfg *rest.Config, managedCfg *rest.Config,
) manager.Manager {
	// Set the manager options
	options.HealthProbeBindAddress = healthAddr
	options.LeaderElectionID = "governance-policy-framework-addon2.open-cluster-management.io"
	options.LeaderElectionConfig = managedCfg
	// Set a field selector so that a watch on secrets will be limited to just the secret with the policy template
	// encryption key.
	options.Cache = cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&v1.Secret{}: {
				Field: fields.SelectorFromSet(fields.Set{"metadata.name": secretsync.SecretName}),
			},
		},
		DefaultNamespaces: map[string]cache.Config{
			tool.Options.ClusterNamespaceOnHub: {},
		},
	}

	// Disable the metrics endpoint for this manager. Note that since they both use the global
	// metrics registry, metrics for this manager are still exposed by the other manager.
	options.Metrics.BindAddress = "0"

	// Create a new manager to provide shared dependencies and start components
	mgr, err := ctrl.NewManager(hubCfg, options)
	if err != nil {
		log.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// use config check
	configChecker, err := addonutils.NewConfigChecker(
		"governance-policy-framework-addon2", tool.Options.HubConfigFilePathName,
	)
	if err != nil {
		log.Error(err, "unable to setup a configChecker")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder
	if err := mgr.AddHealthzCheck("healthz", configChecker.Check); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	return mgr
}

// addGkControllerToManager will configure the input manager with the gatekeeper-constraint-status-sync controller if
// Gatekeeper is installed and the Kubernetes cluster is v1.16.0+. The returned health checker will wrap the input
// health checkers but also return unhealthy if the Gatekeeper installation status chanages.
func addGkControllerToManager(
	mgrCtx context.Context, mgr manager.Manager, managedCfg *rest.Config, healthCheck healthz.Checker,
) healthz.Checker {
	discoveryClient := discovery.NewDiscoveryClientForConfigOrDie(managedCfg)

	serverVersion, err := discoveryClient.ServerVersion()
	if err != nil {
		log.Error(err, "unable to detect the managed cluster's Kubernetes version")
		os.Exit(1)
	}

	// Gatekeeper is not supported on such old versions of Kubernetes, so just always disable it in this case. In
	// particular, CRD v1 is not available before Kubernetes v1.16.0.
	if semver.Compare(serverVersion.GitVersion, "v1.16.0") < 0 {
		return healthCheck
	}

	dynamicClient := dynamic.NewForConfigOrDie(managedCfg)

	gkHealthCheck, gatekeeperInstalled, err := gatekeepersync.GatekeeperInstallationChecker(
		mgrCtx, dynamicClient, healthCheck,
	)
	if err != nil {
		log.Error(err, "unable to determine if Gatekeeper is installed")
		os.Exit(1)
	}

	// Only run the controller if Gatekeeper is installed
	if !gatekeeperInstalled {
		log.Info(
			"Gatekeeper is not installed so the gatekeepersync controller will be disabled. If running in a " +
				"cluster, the health endpoint will become unhealthy if Gatekeeper is installed to trigger a " +
				"restart.",
		)

		return gkHealthCheck
	}

	log.Info(
		"Starting the controller since a Gatekeeper installation was detected",
		"controller", gatekeepersync.ControllerName,
	)

	clientset := kubernetes.NewForConfigOrDie(mgr.GetConfig())
	instanceName, _ := os.Hostname() // on an error, instanceName will be empty, which is ok

	constraintsReconciler, constraintEvents := depclient.NewControllerRuntimeSource()

	constraintsWatcher, err := depclient.New(managedCfg, constraintsReconciler, nil)
	if err != nil {
		log.Error(err, "Unable to create constraints watcher")
		os.Exit(1)
	}

	go func() {
		err := constraintsWatcher.Start(mgrCtx)
		if err != nil {
			panic(err)
		}
	}()

	// Wait until the constraints watcher has started.
	<-constraintsWatcher.Started()

	if err = (&gatekeepersync.GatekeeperConstraintReconciler{
		Client: mgr.GetClient(),
		ComplianceEventSender: utils.ComplianceEventSender{
			ClusterNamespace: tool.Options.ClusterNamespace,
			ClientSet:        clientset,
			ControllerName:   gatekeepersync.ControllerName,
			InstanceName:     instanceName,
		},
		DynamicClient:        dynamicClient,
		ConstraintsWatcher:   constraintsWatcher,
		Scheme:               mgr.GetScheme(),
		ConcurrentReconciles: int(tool.Options.EvaluationConcurrency),
	}).SetupWithManager(mgr, constraintEvents); err != nil {
		log.Error(err, "unable to create controller", "controller", gatekeepersync.ControllerName)
		os.Exit(1)
	}

	return gkHealthCheck
}

// startHealthProxy responds to /healthz and /readyz HTTP requests and combines the status together of the input
// addresses representing the managers. The HTTP server gracefully shutsdown when the input context is closed.
// The wg.Done() is only called after the HTTP server fails to start or after graceful shutdown of the HTTP server.
func startHealthProxy(ctx context.Context, wg *sync.WaitGroup, addresses ...string) error {
	log := ctrl.Log.WithName("healthproxy")

	for _, endpoint := range []string{"/healthz", "/readyz"} {
		endpoint := endpoint

		http.HandleFunc(endpoint, func(w http.ResponseWriter, r *http.Request) {
			for _, address := range addresses {
				req, err := http.NewRequestWithContext(
					ctx, http.MethodGet, fmt.Sprintf("http://%s%s", address, endpoint), nil,
				)
				if err != nil {
					http.Error(w, fmt.Sprintf("manager: %s", err.Error()), http.StatusInternalServerError)

					return
				}

				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					http.Error(w, fmt.Sprintf("manager: %s", err.Error()), http.StatusInternalServerError)

					return
				}

				defer func() {
					if err := resp.Body.Close(); err != nil {
						log.Info(fmt.Sprintf("Error closing %s response reader: %s\n", endpoint, err))
					}
				}()

				if resp.StatusCode != http.StatusOK {
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						http.Error(w, "not ok", resp.StatusCode)

						return
					}

					http.Error(w, string(body), resp.StatusCode)

					return
				}
			}

			_, err := io.WriteString(w, "ok")
			if err != nil {
				http.Error(w, fmt.Sprintf("manager: %s", err.Error()), http.StatusInternalServerError)
			}
		})
	}

	server := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Addr:              tool.Options.ProbeAddr,
	}

	// Once the input context is done, shutdown the server
	go func() {
		<-ctx.Done()

		log.Info("Stopping the health endpoint proxy")

		// Don't pass the already closed context or else the clean up won't happen
		//nolint:contextcheck
		err := server.Shutdown(context.TODO())
		if err != nil {
			log.Error(err, "Failed to shutdown the health endpoints")
		}

		wg.Done()
	}()

	err := server.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		wg.Done()

		return err
	}

	return nil
}

// getFreeLocalAddr returns an address on the localhost interface with a random free port assigned.
func getFreeLocalAddr() (string, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return "", err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "", err
	}

	defer l.Close()

	return fmt.Sprintf("127.0.0.1:%d", l.Addr().(*net.TCPAddr).Port), nil
}

// addControllers sets up all controllers with their respective managers
func addControllers(ctx context.Context, hubCfg *rest.Config, hubMgr manager.Manager, managedMgr manager.Manager) {
	// Set up all controllers for manager on managed cluster
	var hubClient client.Client

	if hubMgr == nil {
		hubCache, err := cache.New(hubCfg,
			cache.Options{
				ByObject: map[client.Object]cache.ByObject{
					&v1.Secret{}: {
						Field: fields.SelectorFromSet(fields.Set{"metadata.name": secretsync.SecretName}),
					},
				},
				DefaultNamespaces: map[string]cache.Config{
					tool.Options.ClusterNamespaceOnHub: {},
				},
				Scheme: scheme,
			},
		)
		if err != nil {
			log.Error(err, "Failed to generate a cache to the hub cluster")
			os.Exit(1)
		}

		go func() {
			err := hubCache.Start(ctx)
			if err != nil {
				log.Error(err, "Failed to start the cache to the hub cluster")
				os.Exit(1)
			}
		}()

		hubClient, err = client.New(
			hubCfg, client.Options{Scheme: scheme, Cache: &client.CacheOptions{Reader: hubCache}},
		)

		if err != nil {
			log.Error(err, "Failed to generate a client to the hub cluster")
			os.Exit(1)
		}
	} else {
		hubClient = hubMgr.GetClient()
	}

	var kubeClientHub kubernetes.Interface = kubernetes.NewForConfigOrDie(hubCfg)

	eventBroadcasterHub := record.NewBroadcaster()
	eventBroadcasterHub.StartRecordingToSink(
		&corev1.EventSinkImpl{Interface: kubeClientHub.CoreV1().Events(tool.Options.ClusterNamespaceOnHub)},
	)

	hubRecorder := eventBroadcasterHub.NewRecorder(eventsScheme, v1.EventSource{Component: statussync.ControllerName})

	if err := (&statussync.PolicyReconciler{
		ClusterNamespaceOnHub: tool.Options.ClusterNamespaceOnHub,
		HubClient:             hubClient,
		HubRecorder:           hubRecorder,
		ManagedClient:         managedMgr.GetClient(),
		ManagedRecorder:       managedMgr.GetEventRecorderFor(statussync.ControllerName),
		Scheme:                managedMgr.GetScheme(),
		ConcurrentReconciles:  int(tool.Options.EvaluationConcurrency),
	}).SetupWithManager(managedMgr); err != nil {
		log.Error(err, "unable to create controller", "controller", "Policy")
		os.Exit(1)
	}

	depReconciler, depEvents := depclient.NewControllerRuntimeSource()

	watcher, err := depclient.New(managedMgr.GetConfig(), depReconciler, nil)
	if err != nil {
		log.Error(err, "Unable to create dependency watcher")
		os.Exit(1)
	}

	instanceName, _ := os.Hostname() // on an error, instanceName will be empty, which is ok

	templateReconciler := &templatesync.PolicyReconciler{
		Client:               managedMgr.GetClient(),
		DynamicWatcher:       watcher,
		Scheme:               managedMgr.GetScheme(),
		Config:               managedMgr.GetConfig(),
		Recorder:             managedMgr.GetEventRecorderFor(templatesync.ControllerName),
		ClusterNamespace:     tool.Options.ClusterNamespace,
		Clientset:            kubernetes.NewForConfigOrDie(managedMgr.GetConfig()),
		InstanceName:         instanceName,
		DisableGkSync:        tool.Options.DisableGkSync,
		ConcurrentReconciles: int(tool.Options.EvaluationConcurrency),
	}

	go func() {
		err := watcher.Start(ctx)
		if err != nil {
			panic(err)
		}
	}()

	// Wait until the dynamic watcher has started.
	<-watcher.Started()

	if err := templateReconciler.Setup(managedMgr, depEvents); err != nil {
		log.Error(err, "Unable to create the controller", "controller", templatesync.ControllerName)
		os.Exit(1)
	}

	// Set up all controllers for manager on hub cluster
	if tool.Options.DisableSpecSync {
		return
	}

	var kubeClient kubernetes.Interface = kubernetes.NewForConfigOrDie(managedMgr.GetConfig())

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(
		&corev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events(tool.Options.ClusterNamespace)},
	)

	managedRecorder := eventBroadcaster.NewRecorder(eventsScheme, v1.EventSource{Component: specsync.ControllerName})

	if err = (&specsync.PolicyReconciler{
		HubClient:            hubClient,
		ManagedClient:        managedMgr.GetClient(),
		ManagedRecorder:      managedRecorder,
		Scheme:               hubMgr.GetScheme(),
		TargetNamespace:      tool.Options.ClusterNamespace,
		ConcurrentReconciles: int(tool.Options.EvaluationConcurrency),
	}).SetupWithManager(hubMgr); err != nil {
		log.Error(err, "Unable to create the controller", "controller", specsync.ControllerName)
		os.Exit(1)
	}

	if err = (&secretsync.SecretReconciler{
		Client:               hubClient,
		ManagedClient:        managedMgr.GetClient(),
		Scheme:               hubMgr.GetScheme(),
		TargetNamespace:      tool.Options.ClusterNamespace,
		ConcurrentReconciles: int(tool.Options.EvaluationConcurrency),
	}).SetupWithManager(hubMgr); err != nil {
		log.Error(err, "Unable to create the controller", "controller", secretsync.ControllerName)
		os.Exit(1)
	}
}
