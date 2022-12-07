// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package tool

import (
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
	EnableLease               bool
	EnableLeaderElection      bool
	LegacyLeaderElection      bool
	ProbeAddr                 string
	MetricsAddr               string
	// The namespace that the replicated policies should be synced to. This defaults to the same namespace as on the
	// Hub.
	ClusterNamespace string
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
		&Options.EnableLeaderElection,
		"leader-elect",
		true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.",
	)

	flag.BoolVar(
		&Options.LegacyLeaderElection,
		"legacy-leader-elect",
		false,
		"Use a legacy leader election method for controller manager instead of the lease API.",
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
}
