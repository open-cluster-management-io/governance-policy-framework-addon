// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package tool

import (
	"errors"
	"flag"
	"os"

	"github.com/spf13/pflag"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("cmd")

// PolicySpecSyncOptions for command line flag parsing
type SyncerOptions struct {
	ClusterNamespaceOnHub     string
	HubConfigFilePathName     string
	ManagedConfigFilePathName string
	OnMulticlusterhub         bool
	DisableGkSync             bool
	EnableLease               bool
	EnableLeaderElection      bool
	ProbeAddr                 string
	MetricsAddr               string
	SecureMetrics             bool
	// The namespace that the replicated policies should be synced to. This defaults to the same namespace as on the
	// Hub.
	ClusterNamespace      string
	DeploymentName        string
	EvaluationConcurrency uint8
	ClientQPS             float32
	ClientBurst           uint32
}

var disableSpecSync bool

// Options default value
var Options = SyncerOptions{}

// ProcessFlags parses command line parameters into Options
func ProcessFlags() {
	flag := pflag.CommandLine

	flag.StringVar(
		&Options.ClusterNamespace,
		"cluster-namespace",
		"",
		"The namespace that the replicated policies should be synced to. This is required.",
	)

	flag.StringVar(
		&Options.ClusterNamespaceOnHub,
		"cluster-namespace-on-hub",
		"",
		"The cluster namespace on the Hub. This defaults to the namespace provided with --cluster-namespace.",
	)

	flag.StringVar(
		&Options.HubConfigFilePathName,
		"hub-cluster-configfile",
		"",
		"Configuration file pathname to hub kubernetes cluster",
	)

	flag.StringVar(
		&Options.ManagedConfigFilePathName,
		"managed-cluster-configfile",
		"",
		"Configuration file pathname to managed kubernetes cluster",
	)

	flag.BoolVar(
		&Options.EnableLease,
		"enable-lease",
		false,
		"If enabled, the controller will start the lease controller to report its status",
	)

	flag.BoolVar(
		&disableSpecSync,
		"disable-spec-sync",
		false,
		"(Deprecated. Use '--on-multicluster-hub' instead.) If enabled, the spec-sync controller "+
			"will not be started. This is used when running on the Hub.",
	)

	flag.BoolVar(
		&Options.OnMulticlusterhub,
		"on-multicluster-hub",
		false,
		"If true, controllers will not sync things to/from the hub.",
	)

	flag.BoolVar(
		&Options.DisableGkSync,
		"disable-gatekeeper-sync",
		false,
		"If enabled, Gatekeeper object syncing will be entirely disabled.",
	)

	flag.BoolVar(
		&Options.EnableLeaderElection,
		"leader-elect",
		true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.",
	)

	flag.StringVar(
		&Options.ProbeAddr,
		"health-probe-bind-address",
		":8080",
		"The address the first probe endpoint binds to.",
	)

	flag.StringVar(
		&Options.MetricsAddr,
		"metrics-bind-address",
		"localhost:8383",
		"The address the metrics endpoint binds to.",
	)

	flag.BoolVar(
		&Options.SecureMetrics,
		"secure-metrics",
		false,
		"Enable secure metrics endpoint with certificates at /var/run/metrics-cert",
	)

	flag.Uint8Var(
		&Options.EvaluationConcurrency,
		"evaluation-concurrency",
		// Set a low default to not add too much load to the Kubernetes API server in resource constrained deployments.
		2,
		"The max number of concurrent reconciles",
	)

	flag.Float32Var(
		&Options.ClientQPS,
		"client-max-qps",
		30, // 15 * concurrency is recommended
		"The max queries per second that will be made against the kubernetes API server. "+
			"Will scale with concurrency, if not explicitly set.",
	)

	flag.Uint32Var(
		&Options.ClientBurst,
		"client-burst",
		45, // the controller-runtime defaults are 20:30 (qps:burst) - this matches that ratio
		"The maximum burst before client requests will be throttled. "+
			"Will scale with concurrency, if not explicitly set.",
	)
}

func ProcessAndParse(flagset *flag.FlagSet) error {
	ProcessFlags()

	pflag.CommandLine.AddGoFlagSet(flagset)

	pflag.Parse()

	if pflag.CommandLine.Changed("evaluation-concurrency") {
		if !pflag.CommandLine.Changed("client-max-qps") {
			Options.ClientQPS = float32(Options.EvaluationConcurrency) * 15
		}

		if !pflag.CommandLine.Changed("client-burst") {
			Options.ClientBurst = uint32(Options.EvaluationConcurrency)*22 + 1
		}
	}

	Options.DeploymentName = os.Getenv("DEPLOYMENT_NAME")
	if Options.DeploymentName == "" {
		log.Info("Environment variable DEPLOYMENT_NAME is empty, using default 'governance-policy-framework-addon'")

		Options.DeploymentName = "governance-policy-framework-addon"
	}

	// The `--disable-spec-sync` flag and ON_MULTICLUSTERHUB env var are deprecated,
	// The preferred configuration point is the `--on-multicluster-hub` flag.
	if disableSpecSync {
		log.Info("The '--disable-spec-sync' flag is deprecated. Use '--on-multicluster-hub' instead.")

		Options.OnMulticlusterhub = true
	}

	if os.Getenv("ON_MULTICLUSTERHUB") == "true" {
		log.Info("The 'ON_MULTICLUSTERHUB' environment variable is deprecated. " +
			"Use the '--on-multicluster-hub' flag instead.")

		Options.OnMulticlusterhub = true
	}

	if Options.ClusterNamespace == "" {
		return errors.New("the --cluster-namespace flag must be provided")
	}

	if Options.ClusterNamespaceOnHub == "" {
		Options.ClusterNamespaceOnHub = Options.ClusterNamespace
	}

	var found bool

	// Get hubconfig to talk to hub apiserver
	if Options.HubConfigFilePathName == "" {
		Options.HubConfigFilePathName, found = os.LookupEnv("HUB_CONFIG")
		if found {
			log.Info("Found ENV HUB_CONFIG, initializing using", "Options.HubConfigFilePathName",
				Options.HubConfigFilePathName)
		}
	}

	if Options.ManagedConfigFilePathName == "" {
		Options.ManagedConfigFilePathName, found = os.LookupEnv("MANAGED_CONFIG")
		if found {
			log.Info(
				"Found ENV MANAGED_CONFIG, initializing using",
				"Options.ManagedConfigFilePathName", Options.ManagedConfigFilePathName,
			)
		}
	}

	return nil
}
