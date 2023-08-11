// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	policyUtils "open-cluster-management.io/governance-policy-propagator/test/utils"

	"open-cluster-management.io/governance-policy-framework-addon/test/utils"
)

var _ = Describe("Test proper metrics handling on syntax error", Ordered, func() {
	userMetricName := "policy_user_errors_total"
	systemMetricName := "policy_system_errors_total"
	policyName := "case21-policy"
	errPolicyFile := "../resources/case21_syntax_error_metric/case21-test-policy-err.yaml"
	correctPolicyFile := "../resources/case21_syntax_error_metric/case21-test-policy.yaml"

	cleanup := func() {
		By("Deleting a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		// Clean up and ignore any errors (in case it was deleted previously)
		_, _ = kubectlHub("delete", "-f", errPolicyFile, "-n", clusterNamespaceOnHub, "--ignore-not-found")
		opt := metav1.ListOptions{}
		policyUtils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		policyUtils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	}

	AfterAll(cleanup)

	It("Should increment user error metric when attempting to apply policy with syntax error", func() {
		hubApplyPolicy(policyName, errPolicyFile)

		By("Checking for the " + userMetricName + " metric on the template-sync controller")
		values := []string{}
		Eventually(func() []string {
			values = utils.GetMetrics(userMetricName, policyName)

			return values
		}, defaultTimeoutSeconds, 1).Should(HaveLen(1))
		Expect(strconv.Atoi(values[0])).To(BeNumerically(">=", 1))
	})

	It("Should not increment system error metric", func() {
		hubApplyPolicy(policyName, errPolicyFile)

		By("Checking for the " + systemMetricName + " metric on the template-sync controller")
		values := []string{}
		Eventually(func() []string {
			values = utils.GetMetrics(systemMetricName, policyName)

			return values
		}, defaultTimeoutSeconds, 1).Should(HaveLen(0))
	})

	It("Should clean up user error metric on policy deletion", func() {
		By(
			"Deleting policy " + policyName + " on hub cluster " +
				"in ns:" + clusterNamespaceOnHub,
		)
		cleanup()
		By("Checking for the " + userMetricName + " metric on the template-sync controller")
		values := []string{}
		Eventually(func() []string {
			values = utils.GetMetrics(userMetricName, policyName)

			return values
		}, defaultTimeoutSeconds, 1).Should(HaveLen(0))
	})

	It("Should increment user error metric when patching a syntax error onto a correct policy", func() {
		hubApplyPolicy(policyName, correctPolicyFile)
		hubApplyPolicy(policyName, errPolicyFile)

		By("Checking for the " + userMetricName + " metric on the template-sync controller")
		values := []string{}
		Eventually(func() []string {
			values = utils.GetMetrics(userMetricName, policyName)

			return values
		}, defaultTimeoutSeconds, 1).Should(HaveLen(1))
		Expect(strconv.Atoi(values[0])).To(BeNumerically(">=", 1))
	})

	It("Should not increment system error metric", func() {
		hubApplyPolicy(policyName, errPolicyFile)

		By("Checking for the " + systemMetricName + " metric on the template-sync controller")
		values := []string{}
		Eventually(func() []string {
			values = utils.GetMetrics(systemMetricName, policyName)

			return values
		}, defaultTimeoutSeconds, 1).Should(HaveLen(0))
	})
})
