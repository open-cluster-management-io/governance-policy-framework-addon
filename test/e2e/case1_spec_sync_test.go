// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case1PolicyName string = "default.case1-test-policy"
	case1PolicyYaml string = "../resources/case1_spec_sync/case1-test-policy.yaml"
)

var _ = Describe("Test spec sync", func() {
	BeforeEach(func() {
		By("Creating a policy on hub cluster in ns:" + testNamespace)
		_, err := utils.KubectlWithOutput("apply", "-f", case1PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_hub")
		Expect(err).Should(BeNil())
		plc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case1PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
	})
	AfterEach(func() {
		By("Deleting a policy on hub cluster in ns:" + testNamespace)
		_, _ = utils.KubectlWithOutput("delete", "-f", case1PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_hub")
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("should create policy on managed cluster", func() {
		plc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case1PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
	})
	It("should update policy on managed cluster", func() {
		By("Patching " + case1PolicyYaml + " on hub with spec.remediationAction = enforce")
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case1PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())
		Expect(hubPlc.Object["spec"].(map[string]interface{})["remediationAction"]).To(Equal("inform"))
		hubPlc.Object["spec"].(map[string]interface{})["remediationAction"] = "enforce"
		hubPlc, err := clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(
			context.TODO(),
			hubPlc,
			metav1.UpdateOptions{})
		Expect(err).To(BeNil())
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case1PolicyName,
				testNamespace,
				true,
				defaultTimeoutSeconds)

			return managedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(hubPlc.Object["spec"]))
	})
	It("should update policy to a different policy template", func() {
		By("Updating policy on hub with ../resources/case1_propagation/case1-test-policy2.yaml")
		_, err := utils.KubectlWithOutput("apply",
			"-f", "../resources/case1_spec_sync/case1-test-policy2.yaml",
			"-n", testNamespace, "--kubeconfig=../../kubeconfig_hub")
		Expect(err).Should(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case1PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())
		yamlPlc := utils.ParseYaml("../resources/case1_spec_sync/case1-test-policy2.yaml")
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case1PolicyName,
				testNamespace,
				true,
				defaultTimeoutSeconds)

			return managedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
	})
	It("should delete policy on managed cluster", func() {
		By("Deleting policy on hub")
		_, err := utils.KubectlWithOutput("delete", "policy", "-n", testNamespace,
			"--all", "--kubeconfig=../../kubeconfig_hub")
		Expect(err).Should(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
})
