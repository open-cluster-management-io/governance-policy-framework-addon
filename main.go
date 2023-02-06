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

	"github.com/go-logr/zapr"
	"github.com/spf13/pflag"
	"github.com/stolostron/go-log-utils/zaputil"
	depclient "github.com/stolostron/kubernetes-dependency-watches/client"

	// to ensure that exec-entrypoint and run can make use of them.
	v1 "k8s.io/api/core/v1"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	extensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
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
	"open-cluster-management.io/addon-framework/pkg/lease"
	addonutils "open-cluster-management.io/addon-framework/pkg/utils"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/secretsync"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/specsync"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/statussync"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/templatesync"
	"open-cluster-management.io/governance-policy-framework-addon/tool"
	"open-cluster-management.io/governance-policy-framework-addon/version"
)

var (
	eventsScheme = k8sruntime.NewScheme()
	log          = ctrl.Log.WithName("setup")
	scheme       = k8sruntime.NewScheme()
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
}

func main() {
	zflags := zaputil.FlagConfig{
		LevelName:   "log-level",
		EncoderName: "log-encoder",
	}

	zflags.Bind(flag.CommandLine)
	klog.InitFlags(flag.CommandLine)

	// custom flags for the controler
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

	mgrOptionsBase := manager.Options{
		LeaderElection: tool.Options.EnableLeaderElection,
		// Disable the metrics endpoint
		MetricsBindAddress: tool.Options.MetricsAddr,
		Scheme:             scheme,
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

	if tool.Options.LegacyLeaderElection {
		// If legacyLeaderElection is enabled, then that means the lease API is not available.
		// In this case, use the legacy leader election method of a ConfigMap.
		mgrOptionsBase.LeaderElectionResourceLock = "configmaps"
	} else {
		// use the leases leader election by default for controller-runtime 0.11 instead of
		// the default of configmapsleases (leases is the new default in 0.12)
		mgrOptionsBase.LeaderElectionResourceLock = "leases"
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

	mgr := getManager(mgrCtx, mgrOptionsBase, mgrHealthAddr, hubCfg, managedCfg)

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
	mgrCtx context.Context, options manager.Options, healthAddr string, hubCfg *rest.Config, managedCfg *rest.Config,
) manager.Manager {
	hubClient, err := client.New(hubCfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "Failed to generate client to the hub cluster")
		os.Exit(1)
	}
	var kubeClient kubernetes.Interface = kubernetes.NewForConfigOrDie(hubCfg)

	eventBroadcaster := record.NewBroadcaster()

	eventBroadcaster.StartRecordingToSink(
		&corev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events(tool.Options.ClusterNamespaceOnHub)},
	)

	hubRecorder := eventBroadcaster.NewRecorder(eventsScheme, v1.EventSource{Component: statussync.ControllerName})

	crdLabelSelector := labels.SelectorFromSet(map[string]string{templatesync.PolicyTypeLabel: "template"})

	options.LeaderElectionID = "governance-policy-framework-addon.open-cluster-management.io"
	options.HealthProbeBindAddress = healthAddr
	options.Namespace = tool.Options.ClusterNamespace
	options.NewCache = cache.BuilderWithOptions(
		cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				&extensionsv1.CustomResourceDefinition{}: {
					Label: crdLabelSelector,
				},
				&extensionsv1beta1.CustomResourceDefinition{}: {
					Label: crdLabelSelector,
				},
			},
		},
	)

	mgr, err := ctrl.NewManager(managedCfg, options)
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&statussync.PolicyReconciler{
		ClusterNamespaceOnHub: tool.Options.ClusterNamespaceOnHub,
		HubClient:             hubClient,
		HubRecorder:           hubRecorder,
		ManagedClient:         mgr.GetClient(),
		ManagedRecorder:       mgr.GetEventRecorderFor(statussync.ControllerName),
		Scheme:                mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create controller", "controller", "Policy")
		os.Exit(1)
	}

	depReconciler, depEvents := depclient.NewControllerRuntimeSource()

	watcher, err := depclient.New(managedCfg, depReconciler, nil)
	if err != nil {
		log.Error(err, "Unable to create dependency watcher")
		os.Exit(1)
	}

	templateReconciler := &templatesync.PolicyReconciler{
		Client:           mgr.GetClient(),
		DynamicWatcher:   watcher,
		Scheme:           mgr.GetScheme(),
		Config:           mgr.GetConfig(),
		Recorder:         mgr.GetEventRecorderFor(templatesync.ControllerName),
		ClusterNamespace: tool.Options.ClusterNamespace,
	}

	go func() {
		err := watcher.Start(mgrCtx)
		if err != nil {
			panic(err)
		}
	}()

	// Wait until the dynamic watcher has started.
	<-watcher.Started()

	if err := templateReconciler.Setup(mgr, depEvents); err != nil {
		log.Error(err, "Unable to create the controller", "controller", templatesync.ControllerName)
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

// getHubManager return a controller Manager object that watches on the Hub and has the controllers registered.
func getHubManager(
	options manager.Options, healthAddr string, hubCfg *rest.Config, managedCfg *rest.Config,
) manager.Manager {
	managedClient, err := client.New(managedCfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "Failed to generate client to the managed cluster")
		os.Exit(1)
	}

	var kubeClient kubernetes.Interface = kubernetes.NewForConfigOrDie(managedCfg)

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(
		&corev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events(tool.Options.ClusterNamespace)},
	)

	managedRecorder := eventBroadcaster.NewRecorder(eventsScheme, v1.EventSource{Component: specsync.ControllerName})

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

	// Set the manager options
	options.HealthProbeBindAddress = healthAddr
	options.LeaderElectionID = "governance-policy-framework-addon2.open-cluster-management.io"
	options.LeaderElectionConfig = managedCfg
	options.Namespace = tool.Options.ClusterNamespaceOnHub
	options.NewCache = newCacheFunc

	// Disable the metrics endpoint for this manager. Note that since they both use the global
	// metrics registry, metrics for this manager are still exposed by the other manager.
	options.MetricsBindAddress = "0"

	// Create a new manager to provide shared dependencies and start components
	mgr, err := ctrl.NewManager(hubCfg, options)
	if err != nil {
		log.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// Setup all Controllers
	if err = (&specsync.PolicyReconciler{
		HubClient:       mgr.GetClient(),
		ManagedClient:   managedClient,
		ManagedRecorder: managedRecorder,
		Scheme:          mgr.GetScheme(),
		TargetNamespace: tool.Options.ClusterNamespace,
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "Unable to create the controller", "controller", specsync.ControllerName)
		os.Exit(1)
	}

	if err = (&secretsync.SecretReconciler{
		Client:          mgr.GetClient(),
		ManagedClient:   managedClient,
		Scheme:          mgr.GetScheme(),
		TargetNamespace: tool.Options.ClusterNamespace,
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "Unable to create the controller", "controller", secretsync.ControllerName)
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

				defer resp.Body.Close()

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

	server := &http.Server{Addr: tool.Options.ProbeAddr}

	// Once the input context is done, shutdown the server
	go func() {
		<-ctx.Done()

		log.Info("Stopping the health endpoint proxy")

		// Don't pass the already closed context or else the clean up won't happen
		// nolint: contextcheck
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
