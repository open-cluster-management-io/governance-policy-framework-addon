// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"errors"
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test template sync", func() {
	const (
		case9PolicyName       string = "case9-test-policy"
		case9PolicyYaml       string = "../resources/case9_template_sync/case9-test-policy.yaml"
		case9ConfigPolicyName string = "case9-config-policy"
	)

	BeforeEach(func() {
		hubApplyPolicy(case9PolicyName, case9PolicyYaml)
	})
	AfterEach(func() {
		By("Deleting a policy on the hub in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("delete", "-f", case9PolicyYaml, "-n", clusterNamespaceOnHub)
		var e *exec.ExitError
		if !errors.As(err, &e) {
			Expect(err).ShouldNot(HaveOccurred())
		}
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("should create policy template on managed cluster", func() {
		By("Checking the configpolicy CR")
		yamlTrustedPlc := utils.ParseYaml("../resources/case9_template_sync/case9-config-policy.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case9ConfigPolicyName, clusterNamespace, true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlTrustedPlc.Object["spec"]))
	})
	It("should override remediationAction in spec", func() {
		By("Patching policy remediationAction=enforce")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case9PolicyName, clusterNamespaceOnHub, true, defaultTimeoutSeconds,
		)
		plc, err := patchRemediationAction(clientHubDynamic, plc, "enforce")
		Expect(err).ToNot(HaveOccurred())
		Expect(plc.Object["spec"].(map[string]interface{})["remediationAction"]).To(Equal("enforce"))
		By("Checking template policy remediationAction")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case9ConfigPolicyName, clusterNamespace, true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
		}, defaultTimeoutSeconds, 1).Should(Equal("enforce"))
	})
	It("should still override remediationAction in spec when there is no remediationAction", func() {
		By("Updating policy with no remediationAction")
		hubApplyPolicy(case9PolicyName, "../resources/case9_template_sync/case9-test-policy-no-remediation.yaml")

		By("Checking template policy remediationAction")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case9ConfigPolicyName, clusterNamespace, true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
		}, defaultTimeoutSeconds, 1).Should(Equal("enforce"))
	})
	It("should contains labels from parent policy", func() {
		By("Checking labels of template policy")
		plc := utils.GetWithTimeout(
			clientManagedDynamic, gvrPolicy, case9PolicyName, clusterNamespace, true, defaultTimeoutSeconds,
		)
		trustedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrConfigurationPolicy,
			case9ConfigPolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds,
		)
		metadataLabels, ok := plc.Object["metadata"].(map[string]interface{})["labels"].(map[string]interface{})
		Expect(ok).To(BeTrue())
		trustedPlcObj, ok := trustedPlc.Object["metadata"].(map[string]interface{})
		Expect(ok).To(BeTrue())
		trustedPlcLabels, ok := trustedPlcObj["labels"].(map[string]interface{})
		Expect(ok).To(BeTrue())
		Expect(metadataLabels[common.ClusterNameLabel]).To(
			utils.SemanticEqual(trustedPlcLabels[common.ClusterNameLabel]))
		Expect(metadataLabels[common.ClusterNameLabel]).To(
			utils.SemanticEqual(trustedPlcLabels["cluster-name"]))
		Expect(metadataLabels[common.ClusterNamespaceLabel]).To(
			utils.SemanticEqual(trustedPlcLabels[common.ClusterNamespaceLabel]))
		Expect(metadataLabels[common.ClusterNamespaceLabel]).To(
			utils.SemanticEqual(trustedPlcLabels["cluster-namespace"]))
	})
	It("should delete template policy on managed cluster", func() {
		By("Deleting parent policy")
		_, err := kubectlHub("delete", "-f", case9PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).ShouldNot(HaveOccurred())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		By("Checking the existence of template policy")
		utils.GetWithTimeout(
			clientManagedDynamic,
			gvrConfigurationPolicy,
			case9ConfigPolicyName,
			clusterNamespace,
			false,
			defaultTimeoutSeconds,
		)
	})
})
