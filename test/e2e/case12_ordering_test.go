// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
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

var _ = Describe("Test dependency logic in template sync", Ordered, func() {
	const (
		resourcePath     string = "../resources/case12_ordering/"
		case12PolicyName string = "case12-policy"
		case12PolicyYaml string = resourcePath + "case12-plc.yaml"
		case12DepName    string = "namespace-foo-setup-policy"
		case12DepYaml    string = resourcePath + "case12-dependency.yaml"
		case12DepBName   string = case12DepName + "-b"
		case12DepBYaml   string = resourcePath + "case12-dependency-b.yaml"

		// Template names (configuration policy names within the policies)
		case12DepTemplateName    string = "namespace-foo-setup-configpolicy"
		case12PolicyTemplateName string = "case12-config-policy"
	)

	// sendComplianceEvent creates a compliance event on the given policy's template.
	sendComplianceEvent := func(ctx SpecContext, plcName, templateName, complianceState string) {
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
					"name":      templateName,
					"namespace": clusterNamespace,
					"uid":       uuid.NewUUID(),
				},
			},
		}

		msg := map[string]string{
			"Compliant":    "No violation detected",
			"NonCompliant": "there is violation",
		}

		err := managedEventSender.SendEvent(
			ctx,
			&configPlc,
			metav1.OwnerReference{
				APIVersion: managedPlc.GetAPIVersion(),
				Kind:       managedPlc.GetKind(),
				Name:       managedPlc.GetName(),
				UID:        managedPlc.GetUID(),
			},
			fwutils.EventReason(clusterNamespace, templateName),
			msg[complianceState],
			policiesv1.ComplianceState(complianceState),
		)
		Expect(err).ToNot(HaveOccurred())
	}

	applyHubPolicyWithCleanup := func(yamlFile, policyName string) {
		hubApplyPolicy(policyName, yamlFile)
		DeferCleanup(func() {
			By("Deleting policy " + policyName + " on hub cluster in ns: " + clusterNamespaceOnHub)
			_, err := kubectlHub("delete", "-f", yamlFile, "-n", clusterNamespaceOnHub)
			Expect(err).ToNot(HaveOccurred())
		})
	}

	applyDepAndPolicy := func(depYaml, depName, policyYaml, policyName string) {
		By("Creating dependency " + depName + " on hub in ns: " + clusterNamespaceOnHub)
		applyHubPolicyWithCleanup(depYaml, depName)
		By("Creating policy " + policyName + " on hub in ns: " + clusterNamespaceOnHub)
		applyHubPolicyWithCleanup(policyYaml, policyName)
	}

	AfterEach(func() {
		By("Clean up all events")
		_, err := kubectlManaged("delete", "events", "-n", clusterNamespace, "--all")
		Expect(err).ShouldNot(HaveOccurred())
	})

	Context("When dependency is compliant", func() {
		It("Should set to compliant when dep status is compliant", func(ctx SpecContext) {
			applyDepAndPolicy(case12DepYaml, case12DepName, case12PolicyYaml, case12PolicyName)

			By("Generating compliant event on the dependency")
			sendComplianceEvent(ctx, case12DepName, case12DepTemplateName, "Compliant")
			By("Checking that dependency status is Compliant")
			Eventually(checkCompliance(ctx, case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

			By("Generating compliant event on the policy")
			sendComplianceEvent(ctx, case12PolicyName, case12PolicyTemplateName, "Compliant")
			By("Checking that policy status is Compliant")
			Eventually(checkCompliance(ctx, case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		})
	})

	Context("When dependency is noncompliant", func() {
		It("Should set to Pending when dep status is NonCompliant", func(ctx SpecContext) {
			applyDepAndPolicy(case12DepYaml, case12DepName, case12PolicyYaml, case12PolicyName)

			By("Generating noncompliant event on the dependency")
			sendComplianceEvent(ctx, case12DepName, case12DepTemplateName, "NonCompliant")
			By("Checking that dependency status is NonCompliant")
			Eventually(checkCompliance(ctx, case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
			By("Checking that policy status is Pending")
			Eventually(checkCompliance(ctx, case12PolicyName), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))
		})
	})

	Context("With ignorePending", func() {
		policyName := case12PolicyName + "-ignorepending"
		It("Should set to Compliant when dep status is NonCompliant and ignorePending is true", func(ctx SpecContext) {
			applyDepAndPolicy(case12DepYaml, case12DepName, resourcePath+"case12-plc-ignorepending.yaml", policyName)

			By("Generating noncompliant event on the dependency")
			sendComplianceEvent(ctx, case12DepName, case12DepTemplateName, "NonCompliant")
			By("Checking that dependency status is NonCompliant")
			Eventually(checkCompliance(ctx, case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
			By("Checking that policy status is Compliant")
			Eventually(checkCompliance(ctx, policyName), defaultTimeoutSeconds*2, 1).Should(Equal("Compliant"))
		})
	})

	Context("When dependency status is resolved", func() {
		It("Should set to Compliant when dep status is resolved", func(ctx SpecContext) {
			applyDepAndPolicy(case12DepYaml, case12DepName, case12PolicyYaml, case12PolicyName)

			By("Generating noncompliant event on the dependency")
			sendComplianceEvent(ctx, case12DepName, case12DepTemplateName, "NonCompliant")
			By("Checking that dependency status is NonCompliant")
			Eventually(checkCompliance(ctx, case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
			By("Checking that policy status is Pending")
			Eventually(checkCompliance(ctx, case12PolicyName), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))

			By("Generating compliant event on the dependency")
			sendComplianceEvent(ctx, case12DepName, case12DepTemplateName, "Compliant")
			By("Checking that dependency status is Compliant")
			Eventually(checkCompliance(ctx, case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
			By("Generating compliant event on the policy")
			sendComplianceEvent(ctx, case12PolicyName, case12PolicyTemplateName, "Compliant")
			By("Checking that policy status is Compliant")
			Eventually(checkCompliance(ctx, case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

			policyInvalidName := case12PolicyName + "-invalid"
			By("Creating policy with invalid dependency on hub in ns: " + clusterNamespaceOnHub)
			applyHubPolicyWithCleanup(resourcePath+"case12-plc-invalid-dep.yaml", policyInvalidName)
			By("Checking that policy status is Pending")
			Eventually(checkCompliance(ctx, policyInvalidName), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))
		})
	})

	Context("When dependency becomes noncompliant after being compliant", func() {
		It("Should remove template if dependency changes", func(ctx SpecContext) {
			applyDepAndPolicy(case12DepYaml, case12DepName, case12PolicyYaml, case12PolicyName)

			By("Generating compliant event on the dependency")
			sendComplianceEvent(ctx, case12DepName, case12DepTemplateName, "Compliant")
			By("Checking that dependency status is Compliant")
			Eventually(checkCompliance(ctx, case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
			By("Generating compliant event on the policy")
			sendComplianceEvent(ctx, case12PolicyName, case12PolicyTemplateName, "Compliant")
			By("Checking that policy status is Compliant")
			Eventually(checkCompliance(ctx, case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

			By("Generating noncompliant event on the dependency")
			sendComplianceEvent(ctx, case12DepName, case12DepTemplateName, "NonCompliant")
			By("Checking that dependency status is NonCompliant")
			Eventually(checkCompliance(ctx, case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
			By("Checking that policy status is Pending")
			Eventually(checkCompliance(ctx, case12PolicyName), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))
		})
	})

	Context("Multiple dependencies", func() {
		It("Should process extra dependencies properly", func(ctx SpecContext) {
			policyName := case12PolicyName + "-multi"
			By("Creating policy on hub in ns: " + clusterNamespaceOnHub)
			applyHubPolicyWithCleanup(resourcePath+"case12-plc-multiple-deps.yaml", policyName)
			By("Creating dependency on hub in ns: " + clusterNamespaceOnHub)
			applyHubPolicyWithCleanup(case12DepYaml, case12DepName)
			By("Creating extra dependency on hub in ns: " + clusterNamespaceOnHub)
			applyHubPolicyWithCleanup(case12DepBYaml, case12DepBName)

			By("Generating noncompliant event on dependency B")
			sendComplianceEvent(ctx, case12DepBName, case12DepTemplateName+"-b", "NonCompliant")
			By("Checking that dependency B status is NonCompliant")
			Eventually(checkCompliance(ctx, case12DepBName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
			By("Generating compliant event on the dependency")
			sendComplianceEvent(ctx, case12DepName, case12DepTemplateName, "Compliant")
			By("Checking that dependency status is Compliant")
			Eventually(checkCompliance(ctx, case12DepName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
			By("Generating compliant event on the policy")
			sendComplianceEvent(ctx, policyName, case12PolicyTemplateName+"-multi", "Compliant")
			By("Checking that policy status is Pending")
			Eventually(checkCompliance(ctx, policyName), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))
		})
	})

	Context("Multiple templates", func() {
		It("Should handle policies with multiple templates (with different dependencies) properly",
			func(ctx SpecContext) {
				policyName := case12PolicyName + "-2-templates"
				By("Creating policy on hub in ns: " + clusterNamespaceOnHub)
				applyHubPolicyWithCleanup(resourcePath+"case12-plc-2-template.yaml", policyName)
				By("Creating dependency on hub in ns: " + clusterNamespaceOnHub)
				applyHubPolicyWithCleanup(case12DepYaml, case12DepName)
				By("Creating extra dependency on hub in ns: " + clusterNamespaceOnHub)
				applyHubPolicyWithCleanup(case12DepBYaml, case12DepBName)

				By("Generating noncompliant event on dependency A")
				sendComplianceEvent(ctx, case12DepName, case12DepTemplateName, "NonCompliant")
				By("Checking that dependency A status is NonCompliant")
				Eventually(checkCompliance(ctx, case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
				By("Generating compliant event on dependency B")
				sendComplianceEvent(ctx, case12DepBName, case12DepTemplateName+"-b", "Compliant")
				By("Checking that dependency B status is Compliant")
				Eventually(checkCompliance(ctx, case12DepBName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
				By("Generating noncompliant event on the non-pending template")
				sendComplianceEvent(ctx, policyName, case12PolicyTemplateName+"-2-templates-b", "NonCompliant")
				By("Checking that policy status is NonCompliant (template A pending, template B noncompliant)")
				Eventually(checkCompliance(ctx, policyName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
			})
	})

	Context("Edge cases", func() {
		It("Should correct a late compliance event while the policy is pending", func(ctx SpecContext) {
			applyDepAndPolicy(case12DepYaml, case12DepName, case12PolicyYaml, case12PolicyName)

			By("Generating noncompliant event on the dependency")
			sendComplianceEvent(ctx, case12DepName, case12DepTemplateName, "NonCompliant")
			By("Checking that dependency status is NonCompliant")
			Eventually(checkCompliance(ctx, case12DepName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
			By("Checking that policy status is Pending")
			Eventually(checkCompliance(ctx, case12PolicyName), defaultTimeoutSeconds*2, 1).Should(Equal("Pending"))

			By("Generating an (incorrect, late) compliant event on the policy")
			sendComplianceEvent(ctx, case12PolicyName, case12PolicyTemplateName, "Compliant")

			// Allow status-sync to settle before asserting; otherwise the next Eventually
			// might finish before a transient Compliant status appears and is overridden.
			time.Sleep(5 * time.Second)

			By("Checking that policy status is Pending")
			Eventually(checkCompliance(ctx, case12PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Pending"))
			By("Checking that policy status remains Pending")
			Consistently(checkCompliance(ctx, case12PolicyName), "15s", 1).Should(Equal("Pending"))
		})

		// This test case is meant for a specific panic that was being caused by some Policies.
		It("Should function properly despite a nonexistent dependency kind", func(ctx SpecContext) {
			const (
				testPolicyName string = "case12-policy-nonexist-dep"
				testPolicyYaml string = "../resources/case12_ordering/case12-plc-nonexist-dep.yaml"
				templateName   string = "case12-config-policy"
			)

			By("Creating policy on hub in ns: " + clusterNamespaceOnHub)
			applyHubPolicyWithCleanup(testPolicyYaml, testPolicyName)

			By("Generating noncompliant event on the first template")
			sendComplianceEvent(ctx, testPolicyName, templateName, "NonCompliant")
			By("Checking that policy status is NonCompliant")
			Eventually(checkCompliance(ctx, testPolicyName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

			// Allow status-sync to settle so template-sync can reconcile (and not panic) before we continue.
			time.Sleep(5 * time.Second)

			By("Generating compliant event on the first template")
			sendComplianceEvent(ctx, testPolicyName, templateName, "Compliant")
			By("Checking that policy status is Compliant")
			Eventually(checkCompliance(ctx, testPolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		})
	})
})
