// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	"github.com/open-cluster-management/governance-policy-propagator/test/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const case1PolicyName string = "default.case1-test-policy"
const case1PolicyYaml string = "../resources/case1_mutation_recovery/case1-test-policy.yaml"

var _ = Describe("Test mutation recovery", func() {
	BeforeEach(func() {
		By("Creating a policy on hub cluster in ns:" + testNamespace)
		_, err := utils.KubectlWithOutput("apply", "-f", case1PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_hub")
		Expect(err).Should(BeNil())
		hubPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())
		By("Creating a policy on managed cluster in ns:" + testNamespace)
		_, err = utils.KubectlWithOutput("apply", "-f", case1PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
		managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
	})
	AfterEach(func() {
		By("Deleting a policy on hub cluster in ns:" + testNamespace)
		_, err := utils.KubectlWithOutput("delete", "-f", case1PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_hub")
		Expect(err).Should(BeNil())
		_, err = utils.KubectlWithOutput("delete", "-f", case1PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("Should recover policy on managed if spec.remediationAction being modified", func() {
		By("Patching " + case1PolicyYaml + " on managed with spec.remediationAction = enforce")
		Eventually(
			func() interface{} {
				managedPlc := utils.GetWithTimeout(
					clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)
				Expect(managedPlc.Object["spec"].(map[string]interface{})["remediationAction"]).To(Equal("inform"))
				managedPlc.Object["spec"].(map[string]interface{})["remediationAction"] = "enforce"
				_, err := clientManagedDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(
					context.TODO(), managedPlc, metav1.UpdateOptions{},
				)
				return err
			},
			defaultTimeoutSeconds,
			1,
		).Should(BeNil())
		By("Comparing spec between hub and managed policy")
		hubPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return managedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(hubPlc.Object["spec"]))
	})
	It("Should recover policy on managed if spec.policyTemplates being modified", func() {
		By("Patching " + case1PolicyYaml + " on managed with spec.policyTemplate = {}")
		Eventually(
			func() interface{} {
				managedPlc := utils.GetWithTimeout(
					clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)
				managedPlc.Object["spec"].(map[string]interface{})["policy-templates"] = []*policiesv1.PolicyTemplate{}
				_, err := clientManagedDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(
					context.TODO(), managedPlc, metav1.UpdateOptions{},
				)
				return err
			},
			defaultTimeoutSeconds,
			1,
		).Should(BeNil())
		By("Comparing spec between hub and managed policy")
		hubPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return managedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(hubPlc.Object["spec"]))
	})
	It("Should recover policy on managed if being deleted", func() {
		By("Deleting " + case1PolicyYaml + " on managed with spec.policyTemplate = {}")
		_, err := utils.KubectlWithOutput("delete", "-f", case1PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
		By("Comparing spec between hub and managed policy")
		hubPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return managedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(hubPlc.Object["spec"]))
	})
	It("Should recover status if policy status being modified", func() {
		By("Generating an compliant event on the policy")
		managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(managedPlc, "Normal", "policy: managed/case1-test-policy-trustedcontainerpolicy", fmt.Sprintf("Compliant; No violation detected"))
		By("Checking if policy status is compliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Update status to NonCompliant")
		Eventually(
			func() interface{} {
				managedPlc = utils.GetWithTimeout(
					clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)
				managedPlc.Object["status"].(map[string]interface{})["compliant"] = "NonCompliant"
				nsPolicy := clientManagedDynamic.Resource(gvrPolicy).Namespace(testNamespace)
				var err error
				managedPlc, err = nsPolicy.UpdateStatus(context.TODO(), managedPlc, metav1.UpdateOptions{})
				return err
			},
			defaultTimeoutSeconds,
			1,
		).Should(BeNil())
		Expect(getCompliant(managedPlc)).To(Equal("NonCompliant"))
		By("Checking if policy status was recovered to compliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("clean up all events")
		_, err := utils.KubectlWithOutput("delete", "events", "-n", testNamespace, "--all",
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
	})
})
