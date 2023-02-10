// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test error handling", func() {
	AfterEach(func() {
		_, err := kubectlHub("delete", "policies", "--all", "-A")
		Expect(err).To(BeNil())
		_, err = kubectlManaged("delete", "configurationpolicies", "--all", "-A")
		Expect(err).To(BeNil())
		_, err = kubectlManaged("delete", "events", "--all", "-A")
		Expect(err).To(BeNil())
	})
	It("should not override remediationAction if doesn't exist on parent policy", func() {
		hubApplyPolicy("case10-remediation-action-not-exists",
			"../resources/case10_template_sync_error_test/remediation-action-not-exists.yaml")

		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				"case10-remediation-action-not-exists-configpolicy", clusterNamespace, true,
				defaultTimeoutSeconds)

			return trustedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
		}, defaultTimeoutSeconds, 1).Should(Equal("inform"))

		hubApplyPolicy("case10-remediation-action-not-exists",
			"../resources/case10_template_sync_error_test/remediation-action-not-exists2.yaml")

		By("Checking the case10-remediation-action-not-exists-configpolicy CR")
		yamlTrustedPlc := utils.ParseYaml(
			"../resources/case10_template_sync_error_test/remediation-action-not-exists-configpolicy.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				"case10-remediation-action-not-exists-configpolicy", clusterNamespace, true,
				defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlTrustedPlc.Object["spec"]))
	})
	It("should generate decode err event", func() {
		hubApplyPolicy("case10-template-decode-error",
			"../resources/case10_template_sync_error_test/template-decode-error.yaml")

		By("Checking for event with decode err on managed cluster in ns:" + clusterNamespace)
		Eventually(
			checkForEvent("case10-template-decode-error", "template-error; Failed to decode policy template"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should generate missing name err event", func() {
		hubApplyPolicy("case10-template-name-error",
			"../resources/case10_template_sync_error_test/template-name-error.yaml")

		By("Checking for event with missing name err on managed cluster in ns:" + clusterNamespace)
		Eventually(
			checkForEvent("case10-template-name-error", "template-error; Failed to get name from policy"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should generate mapping err event", func() {
		hubApplyPolicy("case10-template-mapping-error",
			"../resources/case10_template_sync_error_test/template-mapping-error.yaml")

		By("Checking for event with decode err on managed cluster in ns:" + clusterNamespace)
		Eventually(
			checkForEvent("case10-template-mapping-error", "template-error; Mapping not found"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should generate duplicate policy template err event", func() {
		hubApplyPolicy("case10-test-policy",
			"../resources/case10_template_sync_error_test/working-policy.yaml")

		// wait for original policy to be processed before creating duplicate policy
		utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
			"case10-config-policy", clusterNamespace, true, defaultTimeoutSeconds)

		hubApplyPolicy("case10-test-policy-duplicate",
			"../resources/case10_template_sync_error_test/working-policy-duplicate.yaml")

		By("Checking for event with duplicate err on managed cluster in ns:" + clusterNamespace)
		Eventually(
			checkForEvent("case10-test-policy-duplicate", "Template name must be unique"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should create other objects, even when one is invalid", func() {
		hubApplyPolicy("case10-middle-tmpl",
			"../resources/case10_template_sync_error_test/middle-template-error.yaml")

		By("Checking for the other template objects")
		utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
			"case10-middle-one", clusterNamespace, true, defaultTimeoutSeconds)
		utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
			"case10-middle-three", clusterNamespace, true, defaultTimeoutSeconds)

		By("Checking for the error event")
		Eventually(
			checkForEvent("case10-middle-tmpl", "template-error;"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should remove the complianceState on a template only after an error is resolved", func() {
		hubApplyPolicy("case10-test-policy",
			"../resources/case10_template_sync_error_test/working-policy.yaml")

		utils.ListWithTimeout(clientManagedDynamic, gvrConfigurationPolicy, metav1.ListOptions{},
			1, true, defaultTimeoutSeconds)

		By("Manually updating the status on the created configuration policy")
		compliancePatch := []byte(`[{"op":"add","path":"/status","value":{"compliant":"testing"}}]`)
		// can't just use kubectl - status is a sub-resource
		cfgInt := clientManagedDynamic.Resource(gvrConfigurationPolicy).Namespace(clusterNamespace)
		_, err := cfgInt.Patch(context.TODO(), "case10-config-policy", types.JSONPatchType,
			compliancePatch, metav1.PatchOptions{}, "status")
		Expect(err).Should(BeNil())

		By("Patching the policy to make the template invalid")
		errorPatch := []byte(`[{` +
			`"op":"replace",` +
			`"path":"/spec/policy-templates/0/objectDefinition/kind",` +
			`"value":"PretendPolicy"}]`)
		polInt := clientHubDynamic.Resource(gvrPolicy).Namespace(clusterNamespaceOnHub)
		_, err = polInt.Patch(
			context.TODO(), "case10-test-policy", types.JSONPatchType, errorPatch, metav1.PatchOptions{},
		)
		Expect(err).Should(BeNil())

		By("Checking for the error event")
		Eventually(
			checkForEvent("case10-test-policy", "template-error;"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())

		By("Updating the policy status with the template-error")
		statusPatch := []byte(`[{` +
			`"op":"add",` +
			`"path":"/status",` +
			`"value":{"details":[{"history":[{"message":"template-error;"}]}]}}]`)
		_, err = polInt.Patch(context.TODO(), "case10-test-policy", types.JSONPatchType,
			statusPatch, metav1.PatchOptions{}, "status")
		Expect(err).Should(BeNil())

		By("Checking that the complianceState is still on the configuration policy")
		cfgPolicy, err := cfgInt.Get(context.TODO(), "case10-config-policy", metav1.GetOptions{}, "status")
		Expect(err).To(BeNil())
		compState, found, err := unstructured.NestedString(cfgPolicy.Object, "status", "compliant")
		Expect(err).To(BeNil())
		Expect(found).To(BeTrue())
		Expect(compState).To(Equal("testing"))

		By("Re-applying the working policy")
		hubApplyPolicy("case10-test-policy",
			"../resources/case10_template_sync_error_test/working-policy.yaml")

		By("Checking that the complianceState is removed on the configuration policy")
		Eventually(func() bool {
			cfgPolicy, err := cfgInt.Get(context.TODO(), "case10-config-policy", metav1.GetOptions{}, "status")
			if err != nil {
				return false
			}

			_, found, _ := unstructured.NestedString(cfgPolicy.Object, "status", "compliant")

			return found
		}, defaultTimeoutSeconds, 1).Should(BeFalse())
	})
	It("should throw a noncompliance event if a non-configurationpolicy uses a hub template", func() {
		hubApplyPolicy("case10-bad-hubtemplate",
			"../resources/case10_template_sync_error_test/non-config-hubtemplate.yaml")

		By("Checking for the error event")
		Eventually(
			checkForEvent("case10-bad-hubtemplate", "Templates are not supported for kind"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should throw a noncompliance event if the template object is invalid", func() {
		hubApplyPolicy("case10-invalid-severity",
			"../resources/case10_template_sync_error_test/invalid-severity-template.yaml")

		By("Checking for the error event")
		Eventually(
			checkForEvent("case10-invalid-severity", "Failed to create policy"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should not throw a noncompliance event if the policy-templates array is empty", func() {
		hubApplyPolicy("case10-empty-templates",
			"../resources/case10_template_sync_error_test/empty-templates.yaml")

		By("Checking for the error event")
		Eventually(
			checkForEvent("case10-empty-templates", "Failed to create policy template"),
			defaultTimeoutSeconds,
			1,
		).Should(BeFalse())
	})
	It("should throw a noncompliance event if the template already exists outside of a policy", func() {
		By("Creating the ConfigurationPolicy on the managed cluster directly")
		_, err := kubectlManaged(
			"apply",
			"--filename=../resources/case10_template_sync_error_test/working-policy-configpol.yaml",
			"--namespace="+clusterNamespace,
		)
		Expect(err).Should(BeNil())

		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrConfigurationPolicy,
			"case10-config-policy",
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		ExpectWithOffset(1, managedPlc).NotTo(BeNil())

		hubApplyPolicy("case10-test-policy",
			"../resources/case10_template_sync_error_test/working-policy.yaml")

		By("Checking for the error event")
		Eventually(
			checkForEvent("case10-test-policy", "already exists outside of a Policy"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
})

// Checks for an event on the managed cluster
func checkForEvent(policyName, msgSubStr string) func() bool {
	return func() bool {
		eventInterface := clientManagedDynamic.Resource(gvrEvent).Namespace(clusterNamespace)

		eventList, err := eventInterface.List(context.TODO(), metav1.ListOptions{
			FieldSelector: "involvedObject.name=" + policyName,
		})
		if err != nil {
			return false
		}

		for _, event := range eventList.Items {
			msg, found, err := unstructured.NestedString(event.Object, "message")
			if !found || err != nil {
				continue
			}

			if strings.Contains(msg, msgSubStr) {
				return true
			}
		}

		return false
	}
}
