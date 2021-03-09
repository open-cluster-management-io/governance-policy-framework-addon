// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package tool

import (
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("cmd")

// PolicySpecSyncOptions for command line flag parsing
type PolicySpecSyncOptions struct {
	ClusterName               string
	ClusterNamespace          string
	HubConfigFilePathName     string
	ManagedConfigFilePathName string
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
}

// DeleteClusterNs deletes the cluster namespace on managed cluster if not exists
func DeleteClusterNs(client *kubernetes.Interface, ns string) error {
	if ns != "" {
		return (*client).CoreV1().Namespaces().Delete(ns, &metav1.DeleteOptions{})
	}
	return nil
}
