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
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	apiWatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	apiCache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/watch"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"open-cluster-management.io/addon-framework/pkg/lease"
	addonutils "open-cluster-management.io/addon-framework/pkg/utils"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/complianceeventssync"
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
	eventsScheme        = k8sruntime.NewScheme()
	log                 = ctrl.Log.WithName("setup")
	scheme              = k8sruntime.NewScheme()
	eventFilter         fields.Selector
	healthAddresses     = map[string]bool{}
	healthAddressesLock = sync.RWMutex{}
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
			log.Info(
				"Found ENV MANAGED_CONFIG, initializing using",
				"tool.Options.ManagedConfigFilePathName", tool.Options.ManagedConfigFilePathName,
			)
		}
	}

	if tool.Options.ManagedConfigFilePathName == "" {
		managedCfg, err = config.GetConfig()
		if err != nil {
			log.Error(err, "Failed to build managed cluster config")
			os.Exit(1)
		}
	} else {
		managedCfg, err = clientcmd.BuildConfigFromFlags("", tool.Options.ManagedConfigFilePathName)
		if err != nil {
			log.Error(err, "Failed to build managed cluster config")
			os.Exit(1)
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

	healthAddressesLock.Lock()
	healthAddresses[mgrHealthAddr] = true

	mainCtx := ctrl.SetupSignalHandler()
	mgrCtx, mgrCtxCancel := context.WithCancel(mainCtx)

	mgr := getManager(mgrOptionsBase, mgrHealthAddr, hubCfg, managedCfg)

	var hubMgr manager.Manager

	if !tool.Options.DisableSpecSync {
		hubMgrHealthAddr, err := getFreeLocalAddr()
		if err != nil {
			log.Error(err, "Failed to get a free port for the health endpoint")
			os.Exit(1)
		}

		healthAddresses[hubMgrHealthAddr] = true

		hubMgr = getHubManager(mgrOptionsBase, hubMgrHealthAddr, hubCfg, managedCfg)
	}

	healthAddressesLock.Unlock()

	var wg sync.WaitGroup

	// Add Gatekeeper controller if enabled
	if !tool.Options.DisableGkSync {
		discoveryClient := discovery.NewDiscoveryClientForConfigOrDie(managedCfg)

		serverVersion, err := discoveryClient.ServerVersion()
		if err != nil {
			log.Error(err, "unable to detect the managed cluster's Kubernetes version")
			os.Exit(1)
		}

		// Gatekeeper is not supported on such old versions of Kubernetes, so just always disable it in this case. In
		// particular, CRD v1 is not available before Kubernetes v1.16.0.
		if semver.Compare(serverVersion.GitVersion, "v1.16.0") >= 0 {
			dynamicClient := dynamic.NewForConfigOrDie(managedCfg)

			go manageGatekeeperSyncManager(mgrCtx, &wg, managedCfg, dynamicClient, mgrOptionsBase)
		} else {
			log.Info("The Gatekeeper integration is disabled due to the Kubernetes version being less than 1.16.0")
		}
	} else {
		log.Info("The Gatekeeper integration is set to disabled")
	}

	log.Info("Adding controllers to managers")

	var queue workqueue.RateLimitingInterface

	var dynamicClient *dynamic.DynamicClient

	var policyListResourceVersion string

	if tool.Options.ComplianceAPIURL != "" {
		queue = workqueue.NewRateLimitingQueueWithConfig(
			workqueue.DefaultControllerRateLimiter(),
			workqueue.RateLimitingQueueConfig{Name: "compliance-api-events"},
		)

		dynamicClient = dynamic.NewForConfigOrDie(managedCfg)

		gvr := complianceeventssync.GVRPolicy

		listResult, err := dynamicClient.Resource(gvr).Namespace(tool.Options.ClusterNamespace).List(
			mgrCtx, metav1.ListOptions{},
		)
		if err != nil {
			log.Error(err, "Failed to list the policies for recording disabled events")
			os.Exit(1)
		}

		policyListResourceVersion = listResult.GetResourceVersion()
	}

	addControllers(mgrCtx, hubCfg, hubMgr, mgr, queue)

	log.Info("Starting the controller managers")

	wg.Add(1)

	go func() {
		err := startHealthProxy(mgrCtx, &wg)
		if err != nil {
			log.Error(err, "failed to start the health endpoint proxy")

			// On errors, the parent context (mainCtx) may not have closed, so cancel the child context.
			mgrCtxCancel()
		}
	}()

	if tool.Options.ComplianceAPIURL != "" {
		wg.Add(1)

		go func() {
			err := statussync.StartComplianceEventsSyncer(
				mgrCtx, tool.Options.ClusterNamespace, hubCfg, dynamicClient, tool.Options.ComplianceAPIURL, queue,
			)
			if err != nil {
				log.Error(err, "Failed to start the compliance events API syncer")

				mgrCtxCancel()
			}

			wg.Done()
		}()

		wg.Add(1)

		go func() {
			complianceeventssync.DisabledEventsRecorder(
				mgrCtx, dynamicClient, tool.Options.ClusterNamespace, queue, policyListResourceVersion,
			)
			wg.Done()
		}()
	}

	var errorExit bool

	wg.Add(1)

	go func() {
		if err := mgr.Start(mgrCtx); err != nil {
			log.Error(err, "problem running manager")

			// On errors, the parent context (mainCtx) may not have closed, so cancel the child context.
			mgrCtxCancel()

			errorExit = true
		}

		if queue != nil {
			queue.ShutDownWithDrain()
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
	options manager.Options, healthAddr string, hubCfg *rest.Config, managedCfg *rest.Config,
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
			&v1.Event{}: {
				Namespaces: map[string]cache.Config{
					tool.Options.ClusterNamespace: {
						FieldSelector: eventFilter,
						Transform: func(obj interface{}) (interface{}, error) {
							event := obj.(*v1.Event)
							// Only cache fields that are utilized by the controllers.
							guttedEvent := &v1.Event{
								InvolvedObject: event.InvolvedObject,
								TypeMeta:       event.TypeMeta,
								ObjectMeta: metav1.ObjectMeta{
									Name:      event.ObjectMeta.Name,
									Namespace: event.ObjectMeta.Namespace,
									UID:       event.ObjectMeta.UID,
								},
								LastTimestamp: event.LastTimestamp,
								Message:       event.Message,
								Reason:        event.Reason,
								EventTime:     event.EventTime,
							}

							eventAnnotations := map[string]string{}
							parentID := event.Annotations[utils.ParentDBIDAnnotation]

							if parentID != "" {
								eventAnnotations[utils.ParentDBIDAnnotation] = parentID
							}

							policyID := event.Annotations[utils.PolicyDBIDAnnotation]

							if policyID != "" {
								eventAnnotations[utils.PolicyDBIDAnnotation] = policyID
							}

							if len(eventAnnotations) > 0 {
								guttedEvent.Annotations = eventAnnotations
							}

							return guttedEvent, nil
						},
					},
				},
			},
			&v1.Secret{}: {
				Namespaces: map[string]cache.Config{
					tool.Options.ClusterNamespace: {
						FieldSelector: fields.SelectorFromSet(fields.Set{"metadata.name": secretsync.SecretName}),
					},
				},
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

	configFiles := []string{tool.Options.HubConfigFilePathName}

	if hubCfg.TLSClientConfig.CertFile != "" {
		configFiles = append(configFiles, hubCfg.TLSClientConfig.CertFile)
	}

	// use config check
	configChecker, err := addonutils.NewConfigChecker("governance-policy-framework-addon", configFiles...)
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
	// Set the manager options
	options.HealthProbeBindAddress = healthAddr
	options.LeaderElectionID = "governance-policy-framework-addon2.open-cluster-management.io"
	options.LeaderElectionConfig = managedCfg
	// Set a field selector so that a watch on secrets will be limited to just the secret with the policy template
	// encryption key.
	options.Cache = cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&v1.Secret{}: {
				Namespaces: map[string]cache.Config{
					tool.Options.ClusterNamespaceOnHub: {
						FieldSelector: fields.SelectorFromSet(fields.Set{"metadata.name": secretsync.SecretName}),
					},
				},
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

	configFiles := []string{tool.Options.HubConfigFilePathName}

	if hubCfg.TLSClientConfig.CertFile != "" {
		configFiles = append(configFiles, hubCfg.TLSClientConfig.CertFile)
	}

	// use config check
	configChecker, err := addonutils.NewConfigChecker("governance-policy-framework-addon2", configFiles...)
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

// startHealthProxy responds to /healthz and /readyz HTTP requests and combines the status together of the
// healthAddresses map representing the managers. The HTTP server gracefully shutsdown when the input context is closed.
// The wg.Done() is only called after the HTTP server fails to start or after graceful shutdown of the HTTP server.
func startHealthProxy(ctx context.Context, wg *sync.WaitGroup) error {
	log := ctrl.Log.WithName("healthproxy")

	httpClient := http.Client{
		Timeout: 5 * time.Second,
	}

	for _, endpoint := range []string{"/healthz", "/readyz"} {
		endpoint := endpoint

		http.HandleFunc(endpoint, func(w http.ResponseWriter, r *http.Request) {
			healthAddressesLock.RLock()
			addresses := make([]string, 0, len(healthAddresses))

			// Populate a separate slice to avoid holding the lock too long.
			for address := range healthAddresses {
				addresses = append(addresses, address)
			}

			healthAddressesLock.RUnlock()

			for _, address := range addresses {
				req, err := http.NewRequestWithContext(
					ctx, http.MethodGet, fmt.Sprintf("http://%s%s", address, endpoint), nil,
				)
				if err != nil {
					http.Error(w, fmt.Sprintf("manager: %s", err.Error()), http.StatusInternalServerError)

					return
				}

				resp, err := httpClient.Do(req)
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
func addControllers(
	ctx context.Context,
	hubCfg *rest.Config,
	hubMgr manager.Manager,
	managedMgr manager.Manager,
	queue workqueue.RateLimitingInterface,
) {
	// Set up all controllers for manager on managed cluster
	var hubClient client.Client
	var specSyncRequests chan event.GenericEvent
	var specSyncRequestsSource *source.Channel

	if hubMgr == nil {
		hubCache, err := cache.New(hubCfg,
			cache.Options{
				ByObject: map[client.Object]cache.ByObject{
					&v1.Secret{}: {
						Namespaces: map[string]cache.Config{
							tool.Options.ClusterNamespaceOnHub: {
								FieldSelector: fields.SelectorFromSet(
									fields.Set{"metadata.name": secretsync.SecretName},
								),
							},
						},
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
		bufferSize := 100

		specSyncRequests = make(chan event.GenericEvent, bufferSize)
		specSyncRequestsSource = &source.Channel{
			Source:         specSyncRequests,
			DestBufferSize: bufferSize,
		}

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
		EventsQueue:           queue,
		SpecSyncRequests:      specSyncRequests,
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
		EventsQueue:          queue,
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
	}).SetupWithManager(hubMgr, specSyncRequestsSource); err != nil {
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

// manageGatekeeperSyncManager ensures the gatekeeper-constraint-status-sync controller is running based on Gatekeeper's
// installation status. The controller will be off when Gatekeeper is not installed. This is blocking until ctx
// is closed and continuously retries to start the manager if the manager shuts down unexpectedly.
func manageGatekeeperSyncManager(
	ctx context.Context,
	wg *sync.WaitGroup,
	managedCfg *rest.Config,
	dynamicClient dynamic.Interface,
	mgrOptions manager.Options,
) {
	fieldSelector := fmt.Sprintf("metadata.name=constrainttemplates.%s", utils.GvkConstraintTemplate.Group)
	timeout := int64(30)
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}

	var mgrCtx context.Context
	var mgrCtxCancel context.CancelFunc
	var watcher *watch.RetryWatcher
	var mgrRunning bool
	var gatekeeperInstalled bool

	for {
		if watcher == nil {
			listResult, err := dynamicClient.Resource(crdGVR).List(
				ctx, metav1.ListOptions{FieldSelector: fieldSelector, TimeoutSeconds: &timeout},
			)
			if err != nil {
				log.Error(err, "Failed to list the CRDs to check for the Gatekeeper installation. Will retry.")

				time.Sleep(time.Second)

				continue
			}

			resourceVersion := listResult.GetResourceVersion()

			watchFunc := func(options metav1.ListOptions) (apiWatch.Interface, error) {
				options.FieldSelector = fieldSelector

				return dynamicClient.Resource(crdGVR).Watch(ctx, options)
			}

			watcher, err = watch.NewRetryWatcher(resourceVersion, &apiCache.ListWatch{WatchFunc: watchFunc})
			if err != nil {
				log.Error(err, "Failed to watch the CRDs to check for the Gatekeeper installation. Will retry.")

				time.Sleep(time.Second)

				continue
			}

			gatekeeperInstalled = len(listResult.Items) > 0
		}

		if gatekeeperInstalled && !mgrRunning {
			mgrRunning = true

			wg.Add(1)

			mgrCtx, mgrCtxCancel = context.WithCancel(ctx)

			// Keep retrying to start the Gatekeeper sync manager until mgrCtx closes.
			go func(ctx context.Context) {
				for {
					select {
					case <-ctx.Done():
						wg.Done()

						return
					default:
						log.Info(
							"Gatekeeper is installed. Starting the gatekeeper-constraint-status-sync controller.",
						)

						err := runGatekeeperSyncManager(ctx, managedCfg, mgrOptions)
						// The error is logged in runGatekeeperSyncManager since it has more context.
						if err != nil {
							time.Sleep(time.Second)
						}
					}
				}
			}(mgrCtx)
		}

		if !gatekeeperInstalled && mgrRunning {
			log.Info("Gatekeeper was uninstalled. Stopping the gatekeeper-constraint-status-sync controller.")

			mgrRunning = false

			mgrCtxCancel()
		}

		select {
		case <-ctx.Done():
			// Stop the retry watcher if the parent context is canceled. It likely already is stopped, but this is not
			// documented behavior.
			watcher.Stop()

			// Satisfy the lostcancel linter but if this code is reached, then we know mgrCtx is cancelled since the
			// parent context (ctx) is cancelled.
			if mgrCtxCancel != nil {
				mgrCtxCancel()
			}

			return
		case <-watcher.Done():
			// Restart the watcher on the next loop since the context wasn't closed which indicates it was not stopped
			// on purpose.
			watcher = nil
		case result := <-watcher.ResultChan():
			// If the CRD is added, then Gatekeeper is installed.
			if result.Type == apiWatch.Added {
				gatekeeperInstalled = true
			} else if result.Type == apiWatch.Deleted {
				gatekeeperInstalled = false
			}
		}
	}
}

func runGatekeeperSyncManager(ctx context.Context, managedCfg *rest.Config, mgrOptions manager.Options) error {
	healthAddress, err := getFreeLocalAddr()
	if err != nil {
		log.Error(err, "Unable to get a health address for the Gatekeeper constraint status sync manager")

		return err
	}

	// Disable the metrics endpoint for this manager. Note that since they both use the global
	// metrics registry, metrics for this manager are still exposed by the other manager.
	mgrOptions.Metrics.BindAddress = "0"
	mgrOptions.LeaderElectionID = "governance-policy-framework-addon3.open-cluster-management.io"
	mgrOptions.Cache = cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&admissionregistration.ValidatingWebhookConfiguration{}: {
				Field: fields.SelectorFromSet(fields.Set{"metadata.name": gatekeepersync.GatekeeperWebhookName}),
			},
		},
		DefaultNamespaces: map[string]cache.Config{
			tool.Options.ClusterNamespace: {},
		},
	}
	mgrOptions.HealthProbeBindAddress = healthAddress

	// When ctx is still open, then start the manager.
	mgr, err := ctrl.NewManager(managedCfg, mgrOptions)
	if err != nil {
		log.Error(err, "Unable to start the Gatekeeper constraint status sync manager")

		return err
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "Unable to set up health check on the Gatekeeper constraint status sync manager")

		return err
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "Unable to set up ready check on the Gatekeeper constraint status sync manager")

		return err
	}

	clientset := kubernetes.NewForConfigOrDie(mgr.GetConfig())
	instanceName, _ := os.Hostname() // on an error, instanceName will be empty, which is ok

	constraintsReconciler, constraintEvents := depclient.NewControllerRuntimeSource()

	constraintsWatcher, err := depclient.New(
		managedCfg,
		constraintsReconciler,
		&depclient.Options{
			DisableInitialReconcile: true,
			EnableCache:             true,
		},
	)
	if err != nil {
		log.Error(err, "Unable to create the constraints watcher")

		return err
	}

	// Create a separate context for the dynamic watcher to be able to cancel that separately in case
	// starting the manager needs to be retried.
	dynamicWatcherCtx, dynamicWatchCancel := context.WithCancel(ctx)
	defer dynamicWatchCancel()

	go func() {
		err := constraintsWatcher.Start(dynamicWatcherCtx)
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
		ConstraintsWatcher:   constraintsWatcher,
		Scheme:               mgr.GetScheme(),
		ConcurrentReconciles: int(tool.Options.EvaluationConcurrency),
	}).SetupWithManager(mgr, constraintEvents); err != nil {
		log.Error(err, "Unable to create controller", "controller", gatekeepersync.ControllerName)

		// Stop the dynamic watcher since the manager will get recreated.
		dynamicWatchCancel()

		return err
	}

	// Add the health bind address to be considered by the health proxy.
	healthAddressesLock.Lock()
	healthAddresses[healthAddress] = true
	healthAddressesLock.Unlock()

	// This blocks until the manager stops.
	err = mgr.Start(ctx)

	// Remove the health bind address after the manager stops.
	healthAddressesLock.Lock()
	delete(healthAddresses, healthAddress)
	healthAddressesLock.Unlock()

	if err != nil {
		log.Error(err, "Unable to start the Gatekeeper constraint status sync manager")

		// Stop the dynamic watcher since the manager will get recreated.
		dynamicWatchCancel()

		return err
	}

	return nil
}
