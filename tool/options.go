// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package tool

import (
	"context"

	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PolicySpecSyncOptions for command line flag parsing
type PolicySpecSyncOptions struct {
	ClusterName               string
	ClusterNamespace          string
	EnableLeaderElection      bool
	HubConfigFilePathName     string
	ManagedConfigFilePathName string
	LegacyLeaderElection      bool
	ProbeAddr                 string
	// The namespace that the replicated policies should be synced to. This defaults to the same namespace as on the
	// Hub.
	TargetNamespace string
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
		":8081",
		"The address the probe endpoint binds to.",
	)

	flag.StringVar(
		&Options.TargetNamespace,
		"target-namespace",
		"",
		"The namespace that the replicated policies should be synced to. This defaults to the same namespace as on "+
			"the Hub",
	)
}

// DeleteClusterNs deletes the cluster namespace on managed cluster if not exists
func DeleteClusterNs(client *kubernetes.Interface, ns string) error {
	if ns != "" {
		return (*client).CoreV1().Namespaces().Delete(context.TODO(), ns, metav1.DeleteOptions{})
	}

	return nil
}
