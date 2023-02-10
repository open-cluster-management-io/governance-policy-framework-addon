// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test spec sync", func() {
	const (
		case7PolicyName string = "case7-test-policy"
		case7PolicyYaml string = "../resources/case7_spec_sync/case7-test-policy.yaml"
	)

	BeforeEach(func() {
		hubApplyPolicy(case7PolicyName, case7PolicyYaml)
	})
	AfterEach(func() {
		By("Deleting a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, _ = kubectlHub("delete", "-f", case7PolicyYaml, "-n", clusterNamespaceOnHub)
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("should create policy on managed cluster", func() {
		plc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case7PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
	})
	It("should update policy on the hub", func() {
		By("Patching " + case7PolicyYaml + " on hub with spec.remediationAction = enforce")
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case7PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())
		Expect(hubPlc.Object["spec"].(map[string]interface{})["remediationAction"]).To(Equal("inform"))
		hubPlc, err := patchRemediationAction(clientHubDynamic, hubPlc, "enforce")
		Expect(err).To(BeNil())
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case7PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return managedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(hubPlc.Object["spec"]))
	})
	It("should update policy to a different policy template", func() {
		hubApplyPolicy(case7PolicyName, "../resources/case7_spec_sync/case7-test-policy2.yaml")

		yamlPlc := utils.ParseYaml("../resources/case7_spec_sync/case7-test-policy2.yaml")
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case7PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return managedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
	})
	It("should delete policy on managed cluster", func() {
		By("Deleting policy on hub")
		_, err := kubectlHub("delete", "policy", "-n", clusterNamespaceOnHub, "--all")
		Expect(err).Should(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
})
