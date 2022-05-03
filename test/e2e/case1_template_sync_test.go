// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"errors"
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case1PolicyName       string = "default.case1-test-policy"
	case1PolicyYaml       string = "../resources/case1_template_sync/case1-test-policy.yaml"
	case1ConfigPolicyName string = "case1-config-policy"
)

var _ = Describe("Test template sync", func() {
	BeforeEach(func() {
		By("Creating a policy on managed cluster in ns:" + testNamespace)
		_, err := utils.KubectlWithOutput("apply", "-f", case1PolicyYaml, "-n", testNamespace)
		Expect(err).Should(BeNil())
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true,
			defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
	})
	AfterEach(func() {
		By("Deleting a policy on managed cluster in ns:" + testNamespace)
		_, err := utils.KubectlWithOutput("delete", "-f", case1PolicyYaml, "-n", testNamespace)
		var e *exec.ExitError
		if !errors.As(err, &e) {
			Expect(err).Should(BeNil())
		}
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("should create policy template on managed cluster", func() {
		By("Checking the configpolicy CR")
		yamlTrustedPlc := utils.ParseYaml("../resources/case1_template_sync/case1-config-policy.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case1ConfigPolicyName, testNamespace, true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlTrustedPlc.Object["spec"]))
	})
	It("should override remediationAction in spec", func() {
		By("Patching policy remediationAction=enforce")
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true,
			defaultTimeoutSeconds)
		plc.Object["spec"].(map[string]interface{})["remediationAction"] = "enforce"
		plc, err := clientManagedDynamic.Resource(gvrPolicy).Namespace("managed").Update(context.TODO(), plc,
			metav1.UpdateOptions{})
		Expect(err).To(BeNil())
		Expect(plc.Object["spec"].(map[string]interface{})["remediationAction"]).To(Equal("enforce"))
		By("Checking template policy remediationAction")
		yamlStr := "../resources/case1_template_sync/case1-config-policy-enforce.yaml"
		yamlTrustedPlc := utils.ParseYaml(yamlStr)
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case1ConfigPolicyName, testNamespace, true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlTrustedPlc.Object["spec"]))
	})
	It("should still override remediationAction in spec when there is no remediationAction", func() {
		By("Updating policy with no remediationAction")
		_, err := utils.KubectlWithOutput("apply", "-f",
			"../resources/case1_template_sync/case1-test-policy-no-remediation.yaml", "-n", testNamespace)
		Expect(err).Should(BeNil())
		By("Checking template policy remediationAction")
		yamlTrustedPlc := utils.ParseYaml(
			"../resources/case1_template_sync/case1-config-policy-enforce.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				case1ConfigPolicyName, testNamespace, true, defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlTrustedPlc.Object["spec"]))
	})
	It("should contains labels from parent policy", func() {
		By("Checking labels of template policy")
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true,
			defaultTimeoutSeconds)
		trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
			case1ConfigPolicyName, testNamespace, true, defaultTimeoutSeconds)
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
		_, err := utils.KubectlWithOutput("delete", "-f", case1PolicyYaml, "-n", testNamespace)
		Expect(err).Should(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		By("Checking the existence of template policy")
		utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy, case1ConfigPolicyName,
			testNamespace, false, defaultTimeoutSeconds)
	})
})
