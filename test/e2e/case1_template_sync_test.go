// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/open-cluster-management/governance-policy-propagator/controllers/common"
	"github.com/open-cluster-management/governance-policy-propagator/test/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const case1PolicyName string = "default.case1-test-policy"
const case1PolicyYaml string = "../resources/case1_template_sync/case1-test-policy.yaml"
const cast1TrustedContainerPolicyName string = "case1-test-policy-trustedcontainerpolicy"

var _ = Describe("Test spec sync", func() {
	BeforeEach(func() {
		By("Creating a policy on managed cluster in ns:" + testNamespace)
		_, err := utils.KubectlWithOutput("apply", "-f", case1PolicyYaml, "-n", testNamespace)
		Expect(err).Should(BeNil())
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
	})
	AfterEach(func() {
		By("Deleting a policy on managed cluster in ns:" + testNamespace)
		utils.KubectlWithOutput("delete", "-f", case1PolicyYaml, "-n", testNamespace)
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("should create policy template on managed cluster", func() {
		By("Checking the trustedcontainerpolicy CR")
		yamlTrustedPlc := utils.ParseYaml("../resources/case1_template_sync/case1-trusted-container-policy.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrTrustedContainerPolicy, cast1TrustedContainerPolicyName, testNamespace, true, defaultTimeoutSeconds)
			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlTrustedPlc.Object["spec"]))
	})
	It("should override remediationAction in spec", func() {
		By("Patching policy remediationAction=enforce")
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
		plc.Object["spec"].(map[string]interface{})["remediationAction"] = "enforce"
		plc, err := clientManagedDynamic.Resource(gvrPolicy).Namespace("managed").Update(context.TODO(), plc, metav1.UpdateOptions{})
		Expect(err).To(BeNil())
		Expect(plc.Object["spec"].(map[string]interface{})["remediationAction"]).To(Equal("enforce"))
		By("Checking template policy remediationAction")
		yamlTrustedPlc := utils.ParseYaml("../resources/case1_template_sync/case1-trusted-container-policy-enforce.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrTrustedContainerPolicy, cast1TrustedContainerPolicyName, testNamespace, true, defaultTimeoutSeconds)
			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlTrustedPlc.Object["spec"]))
	})
	It("should still override remediationAction in spec when there is no remediationAction", func() {
		By("Updating policy with no remediationAction")
		_, err := utils.KubectlWithOutput("apply", "-f", "../resources/case1_template_sync/case1-test-policy-no-remediation.yaml", "-n", testNamespace)
		Expect(err).Should(BeNil())
		By("Checking template policy remediationAction")
		yamlTrustedPlc := utils.ParseYaml("../resources/case1_template_sync/case1-trusted-container-policy-enforce.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrTrustedContainerPolicy, cast1TrustedContainerPolicyName, testNamespace, true, defaultTimeoutSeconds)
			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlTrustedPlc.Object["spec"]))
	})
	It("should contains labels from parent policy", func() {
		By("Checking labels of template policy")
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
		trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrTrustedContainerPolicy, cast1TrustedContainerPolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(plc.Object["metadata"].(map[string]interface{})["labels"].(map[string]interface{})[common.ClusterNameLabel]).To(
			utils.SemanticEqual(trustedPlc.Object["metadata"].(map[string]interface{})["labels"].(map[string]interface{})[common.ClusterNameLabel]))
		Expect(plc.Object["metadata"].(map[string]interface{})["labels"].(map[string]interface{})[common.ClusterNameLabel]).To(
			utils.SemanticEqual(trustedPlc.Object["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["cluster-name"]))
		Expect(plc.Object["metadata"].(map[string]interface{})["labels"].(map[string]interface{})[common.ClusterNamespaceLabel]).To(
			utils.SemanticEqual(trustedPlc.Object["metadata"].(map[string]interface{})["labels"].(map[string]interface{})[common.ClusterNamespaceLabel]))
		Expect(plc.Object["metadata"].(map[string]interface{})["labels"].(map[string]interface{})[common.ClusterNamespaceLabel]).To(
			utils.SemanticEqual(trustedPlc.Object["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["cluster-namespace"]))
	})
	It("should delete template policy on managed cluster", func() {
		By("Deleting parent policy")
		_, err := utils.KubectlWithOutput("delete", "-f", case1PolicyYaml, "-n", testNamespace)
		Expect(err).Should(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		By("Checking the existence of template policy")
		utils.GetWithTimeout(clientManagedDynamic, gvrTrustedContainerPolicy, cast1TrustedContainerPolicyName, testNamespace, false, defaultTimeoutSeconds)
	})
})
