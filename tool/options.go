// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package tool

import (
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
	DisableSpecSync           bool
	DisableGkSync             bool
	EnableLease               bool
	EnableLeaderElection      bool
	ProbeAddr                 string
	MetricsAddr               string
	// The namespace that the replicated policies should be synced to. This defaults to the same namespace as on the
	// Hub.
	ClusterNamespace      string
	DeploymentName        string
	EvaluationConcurrency uint8
	ClientQPS             float32
	ClientBurst           uint
	ComplianceAPIURL      string
}

// Options default value
var Options = SyncerOptions{}

// ProcessFlags parses command line parameters into Options
func ProcessFlags() {
	flag := pflag.CommandLine

	flag.StringVar(
		&Options.ClusterNamespace,
		"cluster-namespace",
		Options.ClusterNamespace,
		"The namespace that the replicated policies should be synced to. This is required.",
	)

	flag.StringVar(
		&Options.ClusterNamespaceOnHub,
		"cluster-namespace-on-hub",
		Options.ClusterNamespaceOnHub,
		"The cluster namespace on the Hub. This defaults to the namespace provided with --cluster-namespace.",
	)

	flag.StringVar(
		&Options.HubConfigFilePathName,
		"hub-cluster-configfile",
		Options.HubConfigFilePathName,
		"Configuration file pathname to hub kubernetes cluster",
	)

	flag.StringVar(
		&Options.ManagedConfigFilePathName,
		"managed-cluster-configfile",
		Options.ManagedConfigFilePathName,
		"Configuration file pathname to managed kubernetes cluster",
	)

	flag.BoolVar(
		&Options.EnableLease,
		"enable-lease",
		false,
		"If enabled, the controller will start the lease controller to report its status",
	)

	flag.BoolVar(
		&Options.DisableSpecSync,
		"disable-spec-sync",
		false,
		"If enabled, the spec-sync controller will not be started. This is used when running on the Hub.",
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

	flag.UintVar(
		&Options.ClientBurst,
		"client-burst",
		45, // the controller-runtime defaults are 20:30 (qps:burst) - this matches that ratio
		"The maximum burst before client requests will be throttled. "+
			"Will scale with concurrency, if not explicitly set.",
	)

	flag.StringVar(
		&Options.ComplianceAPIURL,
		"compliance-api-url",
		"",
		"The base URL to the Compliance Events API. If not set, compliance events will not be recorded on the API.",
	)

	if flag.Changed("evaluation-concurrency") {
		if !flag.Changed("client-max-qps") {
			Options.ClientQPS = float32(Options.EvaluationConcurrency) * 15
		}

		if !flag.Changed("client-burst") {
			Options.ClientBurst = uint(Options.EvaluationConcurrency)*22 + 1
		}
	}

	Options.DeploymentName = os.Getenv("DEPLOYMENT_NAME")
	if Options.DeploymentName == "" {
		log.Info("Environment variable DEPLOYMENT_NAME is empty, using default 'governance-policy-framework-addon'")

		Options.DeploymentName = "governance-policy-framework-addon"
	}
}
