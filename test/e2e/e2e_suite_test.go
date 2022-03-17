// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	testNamespace          string
	clientManaged          kubernetes.Interface
	clientManagedDynamic   dynamic.Interface
	gvrPolicy              schema.GroupVersionResource
	gvrEvent               schema.GroupVersionResource
	gvrConfigurationPolicy schema.GroupVersionResource
	defaultTimeoutSeconds  int

	defaultImageRegistry string
)

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Policy template sync e2e Suite")
}

var log = ctrl.Log.WithName("test")

func init() {
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
}

var _ = BeforeSuite(func() {
	By("Setup client")
	gvrPolicy = schema.GroupVersionResource{
		Group:    "policy.open-cluster-management.io",
		Version:  "v1",
		Resource: "policies",
	}
	gvrConfigurationPolicy = schema.GroupVersionResource{
		Group:    "policy.open-cluster-management.io",
		Version:  "v1",
		Resource: "configurationpolicies",
	}
	gvrEvent = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "events"}
	clientManaged = NewKubeClient("", "", "")
	clientManagedDynamic = NewKubeClientDynamic("", "", "")
	defaultImageRegistry = "quay.io/stolostron"
	testNamespace = "managed"
	defaultTimeoutSeconds = 30
	By("Create Namesapce if needed")
	namespacesHub := clientManaged.CoreV1().Namespaces()
	if _, err := namespacesHub.Get(context.TODO(), testNamespace, metav1.GetOptions{}); err != nil &&
		errors.IsNotFound(err) {
		Expect(namespacesHub.Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}, metav1.CreateOptions{})).NotTo(BeNil())
	}
})

func NewKubeClient(url, kubeconfig, context string) kubernetes.Interface {
	log.V(1).Info("Creating kubeclient", "url", url, "config", kubeconfig)

	config, err := LoadConfig(url, kubeconfig, context)
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	return clientset
}

func NewKubeClientDynamic(url, kubeconfig, context string) dynamic.Interface {
	log.V(1).Info("Creating dynamic kubeclient", "url", url, "kubeconfig", kubeconfig)

	config, err := LoadConfig(url, kubeconfig, context)
	if err != nil {
		panic(err)
	}

	clientset, err := dynamic.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	return clientset
}

func LoadConfig(url, kubeconfig, context string) (*rest.Config, error) {
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	log.V(2).Info("Using", "kubeconfig", kubeconfig)
	// If we have an explicit indication of where the kubernetes config lives, read that.
	if kubeconfig != "" {
		if context == "" {
			return clientcmd.BuildConfigFromFlags(url, kubeconfig)
		}

		return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
			&clientcmd.ConfigOverrides{
				CurrentContext: context,
			}).ClientConfig()
	}
	// If not, try the in-cluster config.
	if c, err := rest.InClusterConfig(); err == nil {
		return c, nil
	}
	// If no in-cluster config, try the default location in the user's home directory.
	if usr, err := user.Current(); err == nil {
		defaultKubeConfig := filepath.Join(usr.HomeDir, ".kube", "config")

		log.V(1).Info("Running clientcmd.BuildConfigFromFlags", "url", url, "path", defaultKubeConfig)

		if c, err := clientcmd.BuildConfigFromFlags("", defaultKubeConfig); err == nil {
			return c, nil
		}
	}

	return nil, fmt.Errorf("could not create a valid kubeconfig")
}
