// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

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
	AfterEach(func() {
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("Should set to compliant when dep status is compliant", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12DepName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", case12PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

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

		By("Deleting dependency on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())

		By("Deleting the policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
	})
	It("Should set to Pending when dep status is NonCompliant", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12DepName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", case12PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

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

		By("Deleting dependency on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())

		By("Deleting the policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
	})
	It("Should set to Compliant when dep status is NonCompliant and ignorePending is true", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12DepName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", case12PolicyIgnorePendingYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12PolicyIgnorePendingName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

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

		By("Deleting dependency on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())

		By("Deleting the policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12PolicyIgnorePendingYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
	})
	It("Should set to Compliant when dep status is resolved", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12DepName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", case12PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())
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
		_, err = kubectlHub("apply", "-f", case12PolicyYamlInvalid, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12PolicyNameInvalid,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyNameInvalid), defaultTimeoutSeconds, 1).Should(Equal("Pending"))

		By("Deleting dependency on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())

		By("Deleting the policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())

		By("Deleting invalid policy in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12PolicyYamlInvalid, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
	})
	It("Should remove template if dependency changes", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12DepName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", case12PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

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

		By("Deleting dependency on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())

		By("Deleting the policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
	})
	It("Should process extra dependencies properly", func() {
		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", case12ExtraDepsPolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12ExtraDepsPolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12DepName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

		By("Creating an extra dep on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", case12DepBYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12DepBName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

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

		By("Deleting the policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12ExtraDepsPolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())

		By("Deleting dependency on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())

		By("Deleting dependency b on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12DepBYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
	})
	It("Should handle policies with multiple templates (with different dependencies) properly", func() {
		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("apply", "-f", case12Plc2TemplatesYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12Plc2TemplatesName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12DepName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

		By("Creating an extra dep on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("apply", "-f", case12DepBYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		hubPlc = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case12DepBName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())

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

		By("Deleting the policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12Plc2TemplatesYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())

		By("Deleting dependency on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12DepYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())

		By("Deleting dependency b on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub("delete", "-f", case12DepBYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
	})
})
