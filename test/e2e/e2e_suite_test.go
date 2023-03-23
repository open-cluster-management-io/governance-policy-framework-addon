// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"flag"
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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	propagatorutils "open-cluster-management.io/governance-policy-propagator/test/utils"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
	testutils "open-cluster-management.io/governance-policy-framework-addon/test/utils"
)

var (
	testNamespace          string
	clientHub              kubernetes.Interface
	clientHubDynamic       dynamic.Interface
	clientManaged          kubernetes.Interface
	clientManagedDynamic   dynamic.Interface
	gvrPolicy              schema.GroupVersionResource
	gvrSecret              schema.GroupVersionResource
	gvrEvent               schema.GroupVersionResource
	gvrConfigurationPolicy schema.GroupVersionResource
	gvrConstraintTemplate  schema.GroupVersionResource
	kubeconfigHub          string
	kubeconfigManaged      string
	defaultTimeoutSeconds  int
	clusterNamespaceOnHub  string
	clusterNamespace       string

	defaultImageRegistry string

	managedRecorder    record.EventRecorder
	managedEventSender utils.ComplianceEventSender
)

const (
	gvConstraintGroup = "constraints.gatekeeper.sh"
)

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Governance Policy Framework Addon e2e Suite")
}

var log = ctrl.Log.WithName("test")

func init() {
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	flag.StringVar(
		&kubeconfigHub,
		"kubeconfig_hub", "../../kubeconfig_hub",
		"Location of the kubeconfig to use; defaults to KUBECONFIG if not set")
	flag.StringVar(
		&kubeconfigManaged,
		"kubeconfig_managed", "../../kubeconfig_managed",
		"Location of the kubeconfig to use; defaults to KUBECONFIG if not set")
}

var _ = BeforeSuite(func() {
	By("Setup Hub and Managed client")
	gvrPolicy = schema.GroupVersionResource{
		Group:    "policy.open-cluster-management.io",
		Version:  "v1",
		Resource: "policies",
	}
	gvrSecret = schema.GroupVersionResource{
		Version:  "v1",
		Resource: "secrets",
	}
	gvrEvent = schema.GroupVersionResource{
		Version:  "v1",
		Resource: "events",
	}
	gvrConfigurationPolicy = schema.GroupVersionResource{
		Group:    "policy.open-cluster-management.io",
		Version:  "v1",
		Resource: "configurationpolicies",
	}
	gvrConstraintTemplate = schema.GroupVersionResource{
		Group:    "templates.gatekeeper.sh",
		Version:  "v1",
		Resource: "constrainttemplates",
	}
	clientHub = NewKubeClient("", kubeconfigHub, "")
	clientHubDynamic = NewKubeClientDynamic("", kubeconfigHub, "")
	clientManaged = NewKubeClient("", kubeconfigManaged, "")
	clientManagedDynamic = NewKubeClientDynamic("", kubeconfigManaged, "")
	defaultImageRegistry = "quay.io/open-cluster-management"
	testNamespace = "managed"
	defaultTimeoutSeconds = 30
	By("Create Namespace if needed")

	if os.Getenv("E2E_CLUSTER_NAMESPACE_ON_HUB") == "" {
		clusterNamespaceOnHub = testNamespace
	} else {
		clusterNamespaceOnHub = os.Getenv("E2E_CLUSTER_NAMESPACE_ON_HUB")
	}

	namespacesHub := clientHub.CoreV1().Namespaces()
	if _, err := namespacesHub.Get(
		context.TODO(),
		clusterNamespaceOnHub,
		metav1.GetOptions{}); err != nil && errors.IsNotFound(err) {
		Expect(namespacesHub.Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterNamespaceOnHub,
			},
		}, metav1.CreateOptions{})).NotTo(BeNil())
	}
	namespacesManaged := clientHub.CoreV1().Namespaces()
	if _, err := namespacesManaged.Get(
		context.TODO(),
		testNamespace,
		metav1.GetOptions{}); err != nil && errors.IsNotFound(err) {
		Expect(namespacesManaged.Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}, metav1.CreateOptions{})).NotTo(BeNil())
	}
	By("Create EventRecorder")
	var err error
	managedRecorder, err = testutils.CreateRecorder(clientManaged, "status-sync-controller-test")
	Expect(err).To(BeNil())

	if os.Getenv("E2E_CLUSTER_NAMESPACE") != "" {
		clusterNamespace = os.Getenv("E2E_CLUSTER_NAMESPACE")

		_, err := clientManaged.CoreV1().Namespaces().Create(
			context.TODO(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNamespace}},
			metav1.CreateOptions{},
		)
		if !errors.IsAlreadyExists(err) {
			Expect(err).Should(BeNil())
		}
	} else {
		clusterNamespace = testNamespace
	}

	managedConfig, err := LoadConfig("", kubeconfigManaged, "")
	Expect(err).To(BeNil())

	managedEventSender = utils.ComplianceEventSender{
		ClusterNamespace: clusterNamespace,
		InstanceName:     "status-sync-controller-test",
		ClientSet:        kubernetes.NewForConfigOrDie(managedConfig),
		ControllerName:   "status-sync-controller-test",
	}
})

func NewKubeClient(url, kubeconfig, context string) kubernetes.Interface {
	log.V(5).Info(fmt.Sprintf("Create kubeclient for url %s using kubeconfig path %s\n", url, kubeconfig))

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
	log.V(5).Info(fmt.Sprintf("Create kubeclient dynamic for url %s using kubeconfig path %s\n", url, kubeconfig))

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

	log.V(5).Info(fmt.Sprintf("Kubeconfig path %s\n", kubeconfig))
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
		log.V(5).Info(fmt.Sprintf(
			"clientcmd.BuildConfigFromFlags for url %s using %s\n",
			url,
			filepath.Join(usr.HomeDir, ".kube", "config")))

		if c, err := clientcmd.BuildConfigFromFlags("", filepath.Join(usr.HomeDir, ".kube", "config")); err == nil {
			return c, nil
		}
	}

	return nil, fmt.Errorf("could not create a valid kubeconfig")
}

func kubectlHub(args ...string) (string, error) {
	args = append(args, "--kubeconfig=../../kubeconfig_hub")

	return propagatorutils.KubectlWithOutput(args...)
}

func kubectlManaged(args ...string) (string, error) {
	args = append(args, "--kubeconfig=../../kubeconfig_managed")

	return propagatorutils.KubectlWithOutput(args...)
}

// nolint: unparam
func patchRemediationAction(
	client dynamic.Interface, plc *unstructured.Unstructured, remediationAction string,
) (
	*unstructured.Unstructured, error,
) {
	patch := []byte(`[{"op": "replace", "path": "/spec/remediationAction", "value": "` + remediationAction + `"}]`)

	return client.Resource(gvrPolicy).Namespace(plc.GetNamespace()).Patch(
		context.TODO(), plc.GetName(), types.JSONPatchType, patch, metav1.PatchOptions{},
	)
}

func checkCompliance(name string) func() string {
	return func() string {
		getter := clientManagedDynamic.Resource(gvrPolicy).Namespace(clusterNamespace)

		policy, err := getter.Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return "policy not found"
		}

		status, statusOk := policy.Object["status"].(map[string]interface{})
		if !statusOk {
			return "policy has no status"
		}

		compliant, compliantOk := status["compliant"].(string)
		if !compliantOk {
			return "policy status has no complianceState"
		}

		return compliant
	}
}

func hubApplyPolicy(name, path string) {
	By("Applying policy " + path + " to the hub in ns: " + clusterNamespaceOnHub)

	_, err := kubectlHub("apply", "-f", path, "-n", clusterNamespaceOnHub)
	ExpectWithOffset(1, err).Should(BeNil())

	hubPlc := propagatorutils.GetWithTimeout(
		clientHubDynamic,
		gvrPolicy,
		name,
		clusterNamespaceOnHub,
		true,
		defaultTimeoutSeconds)
	ExpectWithOffset(1, hubPlc).NotTo(BeNil())
}
