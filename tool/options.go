// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package tool

import (
	"context"

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
		":8081",
		"The address the probe endpoint binds to.",
	)
}

// CreateClusterNs creates the cluster namespace on managed cluster if not exists
func CreateClusterNs(client *kubernetes.Interface, nsName string) error {
	const clusterLabel = "policy.open-cluster-management.io/isClusterNamespace"
	ns, err := (*client).CoreV1().Namespaces().Get(context.TODO(), nsName, metav1.GetOptions{})
	log.Info("Checking if cluster namespace exist.", "Namespace", nsName)
	if err != nil {
		if errors.IsNotFound(err) {
			// not found, create it
			log.Info("Cluster namespace not found, creating it...", "Namespace", nsName)
			_, err := (*client).CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   nsName,
					Labels: map[string]string{clusterLabel: "true"},
				},
			}, metav1.CreateOptions{})
			return err
		}
		return err
	}
	// namespace exists, patching it
	log.Info("Cluster namespace exists, checking if label exists...", "Namespace", nsName)
	labels := ns.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	if _, ok := labels[clusterLabel]; !ok {
		log.Info("Label doesn't exist, patching it...", "Namespace", nsName)
		labels[clusterLabel] = "true"
		ns.SetLabels(labels)
		_, err = (*client).CoreV1().Namespaces().Update(context.TODO(), ns, metav1.UpdateOptions{})
		if err != nil {
			log.Error(err, "Failed to patch cluster namespace with label.", "Namespace", nsName)
			return err
		}
	}
	log.Info("Cluster namespace exists with label", "Namespace", nsName)
	return nil
}
