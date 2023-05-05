// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test mutation recovery", func() {
	const (
		case1PolicyName string = "case1-test-policy"
		case1PolicyYaml string = "../resources/case1_mutation_recovery/case1-test-policy.yaml"
	)

	BeforeEach(func() {
		hubApplyPolicy(case1PolicyName, case1PolicyYaml)
	})

	AfterEach(func() {
		By("Deleting a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("delete", "-f", case1PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).ShouldNot(HaveOccurred())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("Should recover policy on managed if spec.remediationAction being modified", func() {
		By("Patching " + case1PolicyYaml + " on managed with spec.remediationAction = enforce")
		Eventually(
			func() interface{} {
				managedPlc := utils.GetWithTimeout(
					clientManagedDynamic, gvrPolicy, case1PolicyName, clusterNamespace, true, defaultTimeoutSeconds,
				)
				_, err := patchRemediationAction(clientManagedDynamic, managedPlc, "enforce")

				return err
			},
			defaultTimeoutSeconds,
			1,
		).Should(BeNil())
		By("Comparing spec between hub and managed policy")
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case1PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case1PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return managedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(hubPlc.Object["spec"]))
	})
	It("Should recover policy on managed if spec.policyTemplates being modified", func() {
		By("Patching " + case1PolicyYaml + " on managed with spec.policyTemplate = {}")
		Eventually(
			func() interface{} {
				managedPlc := utils.GetWithTimeout(
					clientManagedDynamic, gvrPolicy, case1PolicyName, clusterNamespace, true, defaultTimeoutSeconds,
				)
				managedPlc.Object["spec"].(map[string]interface{})["policy-templates"] = []*policiesv1.PolicyTemplate{}
				_, err := clientManagedDynamic.Resource(gvrPolicy).Namespace(clusterNamespace).Update(
					context.TODO(), managedPlc, metav1.UpdateOptions{},
				)

				return err
			},
			defaultTimeoutSeconds,
			1,
		).Should(BeNil())
		By("Comparing spec between hub and managed policy")
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case1PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case1PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return managedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(hubPlc.Object["spec"]))
	})
	It("Should recover policy on managed if being deleted", func() {
		By("Deleting " + case1PolicyYaml + " on managed with spec.policyTemplate = {}")
		_, err := kubectlManaged(
			"delete",
			"-f",
			case1PolicyYaml,
			"-n",
			clusterNamespace,
			"--ignore-not-found",
		)
		Expect(err).ShouldNot(HaveOccurred())
		By("Comparing spec between hub and managed policy")
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case1PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case1PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return managedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(hubPlc.Object["spec"]))
	})
	It("Should recover status if policy status being modified", func() {
		By("Generating an compliant event on the policy")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case1PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case1-test-policy-configurationpolicy",
			"Compliant; No violation detected")
		By("Checking if policy status is compliant")
		Eventually(checkCompliance(case1PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Update status to NonCompliant")
		Eventually(
			func() interface{} {
				managedPlc = utils.GetWithTimeout(
					clientManagedDynamic, gvrPolicy, case1PolicyName, clusterNamespace, true, defaultTimeoutSeconds,
				)
				managedPlc.Object["status"].(map[string]interface{})["compliant"] = "NonCompliant"
				nsPolicy := clientManagedDynamic.Resource(gvrPolicy).Namespace(clusterNamespace)
				var err error
				managedPlc, err = nsPolicy.UpdateStatus(context.TODO(), managedPlc, metav1.UpdateOptions{})

				return err
			},
			defaultTimeoutSeconds,
			1,
		).Should(BeNil())
		Expect(getCompliant(managedPlc)).To(Equal("NonCompliant"))
		By("Checking if policy status was recovered to compliant")
		Eventually(checkCompliance(case1PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("clean up all events")
		_, err := kubectlManaged("delete", "events", "-n", clusterNamespace, "--all")
		Expect(err).ShouldNot(HaveOccurred())
	})
})
