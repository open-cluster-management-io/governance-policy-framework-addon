// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package tool

import (
	"github.com/spf13/pflag"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("cmd")

// PolicySpecSyncOptions for command line flag parsing
type PolicySpecSyncOptions struct {
	ClusterName               string
	ClusterNamespace          string
	HubConfigFilePathName     string
	ManagedConfigFilePathName string
	EnableLease               bool
	EnableLeaderElection      bool
	LegacyLeaderElection      bool
	ProbeAddr                 string
}

// Options default value
var Options = PolicySpecSyncOptions{}

// ProcessFlags parses command line parameters into Options
func ProcessFlags() {
	flag := pflag.CommandLine

	flag.StringVar(
		&Options.ClusterName,
		"cluster-name",
		Options.ClusterName,
		"Name of this endpoint.",
	)

	flag.StringVar(
		&Options.ClusterNamespace,
		"cluster-namespace",
		Options.ClusterNamespace,
		"Cluster Namespace of this endpoint in hub.",
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
		":8082",
		"The address the probe endpoint binds to.",
	)
}
