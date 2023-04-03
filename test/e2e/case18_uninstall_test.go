// Copyright (c) 2023 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	propagatorutils "open-cluster-management.io/governance-policy-propagator/test/utils"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/uninstall"
)

var _ = Describe("Test uninstallation procedure", Ordered, Label("uninstall"), func() {
	suiteConfig, _ := GinkgoConfiguration()

	if suiteConfig.LabelFilter != "uninstall" {
		// If the label filter isn't *exactly* "uninstall", then don't run these uninstall tests.
		// It's not _the_best_ way to do this, but it is _a_ way to do this.
		return
	}

	const (
		caseNumber         string = "case18"
		configMapName      string = caseNumber + "-test"
		configMapNamespace string = caseNumber + "-gk-test"
		yamlBasePath       string = "../resources/" + caseNumber + "_uninstall/"
		policyName         string = caseNumber + "-gk-policy"
		policyYaml         string = yamlBasePath + policyName + ".yaml"
		policyYamlUpdated  string = yamlBasePath + policyName + "-updated.yaml"
		gkConstraintName   string = caseNumber + "-gk-constraint"
		constraintResource string = caseNumber + "constrainttemplate"
	)
	gvrConstraint := schema.GroupVersionResource{
		Group:    gvConstraintGroup,
		Version:  "v1beta1",
		Resource: constraintResource,
	}

	BeforeAll(func() {
		By("Creating the namespace " + configMapNamespace)
		ns := &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Namespace",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: configMapNamespace,
			},
		}

		_, err := clientManaged.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
		Expect(err).To(BeNil())
	})

	AfterAll(func() {
		By("Deleting policy " + policyName + " on the hub in ns:" + clusterNamespaceOnHub)
		err := clientHubDynamic.Resource(gvrPolicy).Namespace(clusterNamespaceOnHub).Delete(
			context.TODO(), policyName, metav1.DeleteOptions{},
		)
		if !k8serrors.IsNotFound(err) {
			Expect(err).To(BeNil())
		}

		By("Cleaning up the events for the policy " + policyName)
		_, err = kubectlManaged(
			"delete",
			"events",
			"-n",
			clusterNamespace,
			"--field-selector=involvedObject.name="+policyName,
			"--ignore-not-found",
		)
		Expect(err).To(BeNil())

		opt := metav1.ListOptions{}
		propagatorutils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)

		By("Deleting the namespace " + configMapNamespace)
		err = clientManaged.CoreV1().Namespaces().Delete(context.TODO(), configMapNamespace, metav1.DeleteOptions{})
		if !k8serrors.IsNotFound(err) {
			Expect(err).To(BeNil())
		}
	})

	It("should create the policy and create the constraint", func() {
		By("Creating policy " + policyName + " on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", policyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).Should(BeNil())
		plc := propagatorutils.GetWithTimeout(clientManagedDynamic, gvrPolicy, policyName, clusterNamespace, true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())

		By("Checking for the synced Constraint " + gkConstraintName)
		_ = propagatorutils.GetWithTimeout(clientManagedDynamic, gvrConstraint, gkConstraintName,
			"", true, defaultTimeoutSeconds)
	})

	It("should make the uninstallation more interesting", func() {
		By("Adding an invalid template to the policy")
		_, err := kubectlHub("apply", "-f", policyYamlUpdated, "-n", clusterNamespaceOnHub)
		Expect(err).Should(BeNil())

		By("Adding a finalizer to the constraint")
		_, err = kubectlManaged("patch", constraintResource, gkConstraintName, "--type=json",
			`-p=[{"op":"add","path":"/metadata/finalizers","value":[test.io/foo]}]`)
		Expect(err).Should(BeNil())
	})

	It("should trigger the uninstallation and successfully delete all the policies", func() {
		// Note: the test runs in an env where the default KUBECONFIG is for the managed cluster
		err := uninstall.Trigger([]string{"--policy-namespace=" + clusterNamespace, "--timeout-seconds=60"})
		Expect(err).Should(BeNil())

		By("Verifying there are no policies left on the managed cluster")
		propagatorutils.ListWithTimeout(clientManagedDynamic, gvrPolicy, metav1.ListOptions{}, 0, true,
			defaultTimeoutSeconds)
	})
})
