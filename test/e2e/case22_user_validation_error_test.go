// Copyright (c) 2023 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"open-cluster-management.io/config-policy-controller/test/utils"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policyUtils "open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test proper metrics handling on syntax error", Ordered, func() {
	case22ErrPolicyName := "case22-err"
	case22ErrYaml := "../resources/case22_user_validation_error/case22-test-err.yaml"
	case22CorrectPolicyName := "case22-correct"
	case22CorrectYaml := "../resources/case22_user_validation_error/case22-test-correct.yaml"

	cleanup := func() {
		By("Deleting test policies on hub cluster in ns:" + clusterNamespaceOnHub)
		// Clean up and ignore any errors (in case it was deleted previously)
		_, _ = kubectlHub("delete", "-f", case22ErrYaml, "-n", clusterNamespaceOnHub, "--ignore-not-found")
		_, _ = kubectlHub("delete", "-f", case22CorrectYaml, "-n", clusterNamespaceOnHub, "--ignore-not-found")
		opt := metav1.ListOptions{}
		policyUtils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		policyUtils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	}

	AfterAll(cleanup)

	It("Verifies NonCompliant status for non-decodable policy", func() {
		hubApplyPolicy(case22ErrPolicyName, case22ErrYaml)

		By("Waiting for " + case22ErrPolicyName + " to become NonCompliant")
		Eventually(func() interface{} {
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy,
				case22ErrPolicyName, clusterNamespaceOnHub,
				true, defaultTimeoutSeconds,
			)

			return utils.GetComplianceState(plc)
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
	})

	It("Verifies that validation errors are shown", func() {
		By("Checking message on " + case22ErrPolicyName)
		var plc *policiesv1.Policy
		Eventually(func(g Gomega) interface{} {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case22ErrPolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			g.Expect(err).ToNot(HaveOccurred())
			if len(plc.Status.Details) < 1 {
				return ""
			}

			if len(plc.Status.Details[0].History) < 1 {
				return ""
			}

			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(ContainSubstring("NonCompliant; template-error;"))
	})

	It("Verifies correct policy does not become NonCompliant", func() {
		hubApplyPolicy(case22CorrectPolicyName, case22CorrectYaml)

		By("Checking that " + case22CorrectPolicyName + " does not become NonCompliant")
		Consistently(func() interface{} {
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy,
				case22CorrectPolicyName, clusterNamespaceOnHub,
				true, defaultTimeoutSeconds,
			)

			return utils.GetComplianceState(plc)
		}, defaultTimeoutSeconds, 1).ShouldNot(Equal("NonCompliant"))
	})
})
