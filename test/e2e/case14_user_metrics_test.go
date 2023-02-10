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

var _ = Describe("Test user error metrics", Ordered, func() {
	metricName := "policy_user_errors_total"
	policyName := "case10-template-decode-error"
	policyFile := "../resources/case10_template_sync_error_test/template-decode-error.yaml"

	cleanup := func() {
		By("Deleting a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		// Clean up and ignore any errors (in case it was deleted previously)
		_, _ = kubectlHub("delete", "-f", policyFile, "-n", clusterNamespaceOnHub)
		opt := metav1.ListOptions{}
		policyUtils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		policyUtils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	}

	AfterAll(cleanup)

	It("Should increment user error metric on user error", func() {
		hubApplyPolicy(policyName, policyFile)

		By("Checking for the " + metricName + " metric on the template-sync controller")
		values := []string{}
		Eventually(func() []string {
			values = utils.GetMetrics(metricName, policyName)

			return values
		}, defaultTimeoutSeconds, 1).Should(HaveLen(1))
		Expect(strconv.Atoi(values[0])).To(BeNumerically(">=", 1))
	})

	It("Should clean up user error metric on policy deletion", func() {
		By(
			"Deleting policy " + policyName + " on hub cluster " +
				"in ns:" + clusterNamespaceOnHub,
		)
		cleanup()
		By("Checking for the " + metricName + " metric on the template-sync controller")
		values := []string{}
		Eventually(func() []string {
			values = utils.GetMetrics(metricName, policyName)

			return values
		}, defaultTimeoutSeconds, 1).Should(HaveLen(0))
	})
})
