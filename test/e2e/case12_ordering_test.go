// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"time"

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
	Expect(err).ToNot(HaveOccurred())
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
			Expect(err).ToNot(HaveOccurred())
		})
	}

	AfterEach(func() {
		By("clean up all events")
		_, err := kubectlManaged("delete", "events", "-n", clusterNamespace, "--all")
		Expect(err).ShouldNot(HaveOccurred())
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
		By("Checking if policy status is noncompliant")
		Eventually(checkCompliance(case12Plc2TemplatesName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
	})
	It("Should correct a late compliance event while the policy is pending", func() {
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

		By("Generating an (incorrect, late) compliance event on the policy")
		generateEventOnPolicy(
			case12PolicyName,
			"case12-config-policy",
			"No violation detected",
			"Compliant",
		)

		// Sleep 5 seconds to avoid the Compliant status that only sometimes appears;
		// otherwise, the next Eventually might finish *before* that status comes and goes.
		time.Sleep(5 * time.Second)

		By("Checking if policy status is pending")
		Eventually(checkCompliance(case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Pending"))

		By("Checking if policy status is consistently pending")
		Consistently(checkCompliance(case12PolicyName), "15s", 1).Should(Equal("Pending"))
	})
	// This test case is meant for a specific panic that was being caused by some Policies.
	It("Should function properly despite a nonexistent dependency kind", func() {
		const (
			testPolicyName string = "case12-test-nonexist-dep"
			testPolicyYaml string = "../resources/case12_ordering/case12-plc-nonexist-dep.yaml"
			templateName   string = "case12-config-policy"
		)

		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		hubPolicyApplyAndDeferCleanup(testPolicyYaml, testPolicyName)

		By("Generating a noncompliant event on the first template")
		generateEventOnPolicy(testPolicyName, templateName, "not yet successful", "NonCompliant")

		By("Checking if policy status is noncompliant")
		Eventually(checkCompliance(testPolicyName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		// Sleep to avoid the status-sync from reconciling again before the template-sync has a
		// chance to reconcile and (possibly) panic.
		time.Sleep(5 * time.Second)

		By("Generating a compliant event on the first template")
		generateEventOnPolicy(testPolicyName, templateName, "all systems go", "Compliant")

		By("Checking if policy status is Compliant")
		Eventually(checkCompliance(testPolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
	})
})
