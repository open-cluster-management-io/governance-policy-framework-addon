// Copyright (c) 2020 Red Hat, Inc.
package tool

import (
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("cmd")

// PolicySpecSyncOptions for command line flag parsing
type PolicySpecSyncOptions struct {
	ClusterName           string
	ClusterNamespace      string
	HubConfigFilePathName string
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
}

// CreateClusterNs creates the cluster namespace on managed cluster if not exists
func CreateClusterNs(client *kubernetes.Interface, ns string) error {
	_, err := (*client).CoreV1().Namespaces().Get(ns, metav1.GetOptions{})
	log.Info("Checking if cluster namespace exist.", "Namespace", ns)
	if err != nil {
		if errors.IsNotFound(err) {
			// not found, create it
			log.Info("Cluster namespace not found, creating it...", "Namespace", ns)
			_, err := (*client).CoreV1().Namespaces().Create(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			})
			return err
		}
		return err
	}
	log.Info("Cluster namespace exists.", "Namespace", ns)
	return nil
}
