// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

// Helper function to create events
func generateEventOnPolicy(plcName string, cfgPlcNamespacedName string, eventType string, msg string) {
	managedPlc := utils.GetWithTimeout(
		clientManagedDynamic,
		gvrPolicy,
		plcName,
		clusterNamespace,
		true,
		defaultTimeoutSeconds)
	Expect(managedPlc).NotTo(BeNil())
	managedRecorder.Event(
		managedPlc,
		eventType,
		"policy: "+cfgPlcNamespacedName,
		msg)
}

var _ = Describe("Test dependency logic in template sync", Ordered, func() {
	const (
		case12PolicyName              string = "case12-test-policy"
		case12PolicyYaml              string = "../resources/case12_ordering/case12-plc.yaml"
		case12PolicyIgnorePendingName string = "case12-test-policy-ignorepending"
		case12PolicyIgnorePendingYaml string = "../resources/case12_ordering/case12-plc-ignorepending.yaml"
		case12PolicyNameInvalid       string = "case12-test-policy-invalid"
		case12PolicyYamlInvalid       string = "../resources/case12_ordering/case12-plc-invalid-dep.yaml"
		case12ExtraDepsPolicyName     string = "case12-test-policy-multi"
		case12ExtraDepsPolicyYaml     string = "../resources/case12_ordering/case12-plc-multiple-deps.yaml"
		case12Plc2TemplatesName       string = "case12-test-policy-2-templates"
		case12Plc2TemplatesYaml       string = "../resources/case12_ordering/case12-plc-2-template.yaml"
		case12DepName                 string = "namespace-foo-setup-policy"
		case12DepYaml                 string = "../resources/case12_ordering/case12-dependency.yaml"
		case12DepBName                string = "namespace-foo-setup-policy-b"
		case12DepBYaml                string = "../resources/case12_ordering/case12-dependency-b.yaml"
	)

	hubPolicyApplyAndDeferCleanup := func(yamlFile, policyName string) {
		hubApplyPolicy(policyName, yamlFile)

		DeferCleanup(func() {
			By("Deleting policy " + policyName + " on hub cluster in ns: " + clusterNamespaceOnHub)
			_, err := kubectlHub("delete", "-f", yamlFile, "-n", clusterNamespaceOnHub)
			Expect(err).To(BeNil())
		})
	}

	It("Should set to compliant when dep status is compliant", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyYaml, case12PolicyName)

		By("Generating a compliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"managed/namespace-foo-setup-configpolicy",
			"Normal",
			"Compliant; No violation detected",
		)

		By("Checking if dependency status is compliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a compliant event on the policy")
		generateEventOnPolicy(
			case12PolicyName,
			"managed/case12-config-policy",
			"Normal",
			"Compliant; No violation detected",
		)

		By("Checking if policy status is compliant")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
	})
	It("Should set to Pending when dep status is NonCompliant", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyYaml, case12PolicyName)

		By("Generating a noncompliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"managed/namespace-foo-setup-configpolicy",
			"Warning",
			"NonCompliant; there is violation",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Pending"))
	})
	It("Should set to Compliant when dep status is NonCompliant and ignorePending is true", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyIgnorePendingYaml, case12PolicyIgnorePendingName)

		By("Generating a noncompliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"managed/namespace-foo-setup-configpolicy",
			"Warning",
			"NonCompliant; there is violation",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyIgnorePendingName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
	})
	It("Should set to Compliant when dep status is resolved", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyYaml, case12PolicyName)

		By("Generating a non compliant event on the dep")
		generateEventOnPolicy(
			case12DepName,
			"managed/namespace-foo-setup-configpolicy",
			"Warning",
			"NonCompliant; there is violation",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Pending"))

		By("Generating a compliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"managed/namespace-foo-setup-configpolicy",
			"Normal",
			"Compliant; No violation detected",
		)

		By("Checking if dependency status is compliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a compliant event on the policy")
		generateEventOnPolicy(
			case12PolicyName,
			"managed/case12-config-policy",
			"Normal",
			"Compliant; No violation detected",
		)

		By("Checking if policy status is compliant")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Creating a policy with an invalid dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyYamlInvalid, case12PolicyNameInvalid)

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyNameInvalid), defaultTimeoutSeconds, 1).Should(Equal("Pending"))
	})
	It("Should remove template if dependency changes", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyYaml, case12PolicyName)

		By("Generating a compliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"managed/namespace-foo-setup-configpolicy",
			"Normal",
			"Compliant; No violation detected",
		)

		By("Checking if dependency status is compliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a compliant event on the policy")
		generateEventOnPolicy(
			case12PolicyName,
			"managed/case12-config-policy",
			"Normal",
			"Compliant; No violation detected",
		)

		By("Checking if policy status is compliant")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a non compliant event on the dep")
		generateEventOnPolicy(
			case12DepName,
			"managed/namespace-foo-setup-configpolicy",
			"Warning",
			"NonCompliant; there is violation",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Pending"))
	})
	It("Should process extra dependencies properly", func() {
		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12ExtraDepsPolicyYaml, case12ExtraDepsPolicyName)

		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating an extra dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepBYaml, case12DepBName)

		By("Generating a non compliant event on the dep b")
		generateEventOnPolicy(
			case12DepBName,
			"managed/namespace-foo-setup-configpolicy-b",
			"Warning",
			"NonCompliant; there is violation",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepBName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Generating a compliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"managed/namespace-foo-setup-configpolicy",
			"Normal",
			"Compliant; No violation detected",
		)

		By("Checking if dependency status is compliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a compliant event on the policy")
		generateEventOnPolicy(
			case12ExtraDepsPolicyName,
			"managed/case12-config-policy-multi",
			"Normal",
			"Compliant; No violation detected",
		)

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12ExtraDepsPolicyName), defaultTimeoutSeconds, 1).Should(Equal("Pending"))
	})
	It("Should handle policies with multiple templates (with different dependencies) properly", func() {
		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12Plc2TemplatesYaml, case12Plc2TemplatesName)

		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating an extra dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepBYaml, case12DepBName)

		By("Generating a non compliant event on the dep a")
		generateEventOnPolicy(
			case12DepName,
			"managed/namespace-foo-setup-configpolicy",
			"Warning",
			"NonCompliant; there is violation",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Generating a compliant event on the dependency b")
		generateEventOnPolicy(
			case12DepBName,
			"managed/namespace-foo-setup-configpolicy-b",
			"Normal",
			"Compliant; No violation detected",
		)

		By("Checking if dependency status is compliant")
		Eventually(checkCompliance(case12DepBName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a noncompliant event on the non-pending template")
		generateEventOnPolicy(
			case12Plc2TemplatesName,
			"managed/case12-config-policy-2-templates-b",
			"Warning",
			"NonCompliant; there is violation",
		)

		// should be noncompliant - template A is pending and B is noncompliant
		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12Plc2TemplatesName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
	})
})
