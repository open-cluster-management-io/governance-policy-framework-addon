// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/uuid"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"

	fwutils "open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

// Helper function to create events
func generateEventOnPolicy(plcName string, cfgPlcName string, msg string, complianceState string) {
	managedPlc := utils.GetWithTimeout(
		clientManagedDynamic,
		gvrPolicy,
		plcName,
		clusterNamespace,
		true,
		defaultTimeoutSeconds)
	Expect(managedPlc).NotTo(BeNil())

	configPlc := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": gvrConfigurationPolicy.GroupVersion().String(),
			"kind":       "ConfigurationPolicy",
			"metadata": map[string]interface{}{
				"name":      cfgPlcName,
				"namespace": clusterNamespace,
				"uid":       uuid.NewUUID(),
			},
		},
	}

	err := managedEventSender.SendEvent(
		context.TODO(),
		&configPlc,
		metav1.OwnerReference{
			APIVersion: managedPlc.GetAPIVersion(),
			Kind:       managedPlc.GetKind(),
			Name:       managedPlc.GetName(),
			UID:        managedPlc.GetUID(),
		},
		fwutils.EventReason(clusterNamespace, cfgPlcName),
		msg,
		policiesv1.ComplianceState(complianceState),
	)
	Expect(err).To(BeNil())
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

	AfterEach(func() {
		By("clean up all events")
		_, err := kubectlManaged("delete", "events", "-n", clusterNamespace, "--all")
		Expect(err).Should(BeNil())
	})

	It("Should set to compliant when dep status is compliant", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyYaml, case12PolicyName)

		By("Generating a compliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"namespace-foo-setup-configpolicy",
			"No violation detected",
			"Compliant",
		)

		By("Checking if dependency status is compliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a compliant event on the policy")
		generateEventOnPolicy(
			case12PolicyName,
			"case12-config-policy",
			"No violation detected",
			"Compliant",
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
			"namespace-foo-setup-configpolicy",
			"there is violation",
			"NonCompliant",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))
	})
	It("Should set to Compliant when dep status is NonCompliant and ignorePending is true", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyIgnorePendingYaml, case12PolicyIgnorePendingName)

		By("Generating a noncompliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"namespace-foo-setup-configpolicy",
			"there is violation",
			"NonCompliant",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyIgnorePendingName),
			defaultTimeoutSeconds*2, 1).Should(Equal("Compliant"))
	})
	It("Should set to Compliant when dep status is resolved", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyYaml, case12PolicyName)

		By("Generating a non compliant event on the dep")
		generateEventOnPolicy(
			case12DepName,
			"namespace-foo-setup-configpolicy",
			"there is violation",
			"NonCompliant",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))

		By("Generating a compliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"namespace-foo-setup-configpolicy",
			"No violation detected",
			"Compliant",
		)

		By("Checking if dependency status is compliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a compliant event on the policy")
		generateEventOnPolicy(
			case12PolicyName,
			"case12-config-policy",
			"No violation detected",
			"Compliant",
		)

		By("Checking if policy status is compliant")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Creating a policy with an invalid dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyYamlInvalid, case12PolicyNameInvalid)

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyNameInvalid), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))
	})
	It("Should remove template if dependency changes", func() {
		By("Creating a dep on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12DepYaml, case12DepName)

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(case12PolicyYaml, case12PolicyName)

		By("Generating a compliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"namespace-foo-setup-configpolicy",
			"No violation detected",
			"Compliant",
		)

		By("Checking if dependency status is compliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a compliant event on the policy")
		generateEventOnPolicy(
			case12PolicyName,
			"case12-config-policy",
			"No violation detected",
			"Compliant",
		)

		By("Checking if policy status is compliant")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a non compliant event on the dep")
		generateEventOnPolicy(
			case12DepName,
			"namespace-foo-setup-configpolicy",
			"there is violation",
			"NonCompliant",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))
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
			"namespace-foo-setup-configpolicy-b",
			"there is violation",
			"NonCompliant",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepBName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Generating a compliant event on the dependency")
		generateEventOnPolicy(
			case12DepName,
			"namespace-foo-setup-configpolicy",
			"No violation detected",
			"Compliant",
		)

		By("Checking if dependency status is compliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a compliant event on the policy")
		generateEventOnPolicy(
			case12ExtraDepsPolicyName,
			"case12-config-policy-multi",
			"No violation detected",
			"Compliant",
		)

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12ExtraDepsPolicyName), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))
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
			"namespace-foo-setup-configpolicy",
			"there is violation",
			"NonCompliant",
		)

		By("Checking if dependency status is noncompliant")
		Eventually(checkCompliance(case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Generating a compliant event on the dependency b")
		generateEventOnPolicy(
			case12DepBName,
			"namespace-foo-setup-configpolicy-b",
			"No violation detected",
			"Compliant",
		)

		By("Checking if dependency status is compliant")
		Eventually(checkCompliance(case12DepBName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Generating a noncompliant event on the non-pending template")
		generateEventOnPolicy(
			case12Plc2TemplatesName,
			"case12-config-policy-2-templates-b",
			"there is violation",
			"NonCompliant",
		)

		// should be noncompliant - template A is pending and B is noncompliant
		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12Plc2TemplatesName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
	})
})
