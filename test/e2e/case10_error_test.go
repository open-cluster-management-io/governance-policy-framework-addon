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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test error handling", func() {
	const (
		caseNumber          = "case10"
		yamlBasePath        = "../resources/" + caseNumber + "_template_sync_error_test/"
		dupNamePolicyYaml   = yamlBasePath + "dup-name-policy.yaml"
		dupNamePolicyName   = "dup-policy"
		dupConfigPolicyName = "policy-config-dup"
		nonCompliantPrefix  = "NonCompliant; "
	)

	AfterEach(func() {
		_, err := kubectlHub("delete", "policies", "--all", "-A")
		Expect(err).ToNot(HaveOccurred())
		_, err = kubectlManaged("delete", "configurationpolicies", "--all", "-A")
		Expect(err).ToNot(HaveOccurred())
		_, err = kubectlManaged("delete", "events", "--all", "-A")
		Expect(err).ToNot(HaveOccurred())
	})
	It("should not reconcile the policy when there are same names in policy-template", func() {
		By("Creating policy")
		hubApplyPolicy(dupNamePolicyName, dupNamePolicyYaml)

		By("Should generate warning events")
		Eventually(
			checkForEvent(dupNamePolicyName,
				"There are duplicate names in configurationpolicies, please check the policy"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())

		By("Should not create any configuration policy")
		Consistently(func() interface{} {
			return utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy, dupConfigPolicyName,
				testNamespace, false, defaultTimeoutSeconds)
		}, 10, 1).Should(BeNil())
	})
	It("should not override remediationAction if doesn't exist on parent policy", func() {
		hubApplyPolicy("case10-remediation-action-not-exists",
			yamlBasePath+"remediation-action-not-exists.yaml")

		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				"case10-remediation-action-not-exists-configpolicy", clusterNamespace, true,
				defaultTimeoutSeconds)

			return trustedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
		}, defaultTimeoutSeconds, 1).Should(Equal("inform"))

		hubApplyPolicy("case10-remediation-action-not-exists",
			yamlBasePath+"remediation-action-not-exists2.yaml")

		By("Checking the case10-remediation-action-not-exists-configpolicy CR")
		yamlTrustedPlc := utils.ParseYaml(
			yamlBasePath + "remediation-action-not-exists-configpolicy.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				"case10-remediation-action-not-exists-configpolicy", clusterNamespace, true,
				defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlTrustedPlc.Object["spec"]))
	})
	It("should generate decode err event", func() {
		hubApplyPolicy("case10-template-decode-error",
			yamlBasePath+"template-decode-error.yaml")

		By("Checking for event with decode err on managed cluster in ns:" + clusterNamespace)
		Eventually(
			checkForEvent("case10-template-decode-error", "template-error; Failed to decode policy template"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should generate missing name err event", func() {
		hubApplyPolicy("case10-template-name-error",
			yamlBasePath+"template-name-error.yaml")

		By("Checking for event with missing name err on managed cluster in ns:" + clusterNamespace)
		Eventually(
			checkForEvent("case10-template-name-error", "template-error; Failed to parse or get name from policy"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should generate mapping err event", func() {
		hubApplyPolicy("case10-template-mapping-error",
			yamlBasePath+"template-mapping-error.yaml")

		By("Checking for event with decode err on managed cluster in ns:" + clusterNamespace)
		Eventually(
			checkForEvent("case10-template-mapping-error", "template-error; Mapping not found"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should generate creation err event", func() {
		policyName := "case10-invalid-name-error"
		statusMsg := "template-error; Failed to create policy template:"

		hubApplyPolicy(policyName,
			yamlBasePath+"invalid-name-error.yaml")

		By("Checking for event with creation err on managed cluster in ns:" + clusterNamespace)
		Eventually(
			checkForEvent(policyName, statusMsg),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
		By("Checking if policy status is noncompliant")
		Eventually(func(g Gomega) {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				policyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)
			var plc *policiesv1.Policy
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(hubPlc.Object, &plc)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(plc.Status.Details).To(HaveLen(1))
			g.Expect(plc.Status.Details[0].History).To(HaveLen(1))
			g.Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case10_invalid-name"))
			g.Expect(plc.Status.Details[0].History[0].Message).To(ContainSubstring(statusMsg))
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})
	It("should generate unsupported object err event", func() {
		hubApplyPolicy("case10-unsupported-object",
			yamlBasePath+"unsupported-object-error.yaml")

		By("Checking for event with unsupported CRD err on managed cluster in ns:" + clusterNamespace)
		Eventually(
			checkForEvent("case10-unsupported-object", "template-error; policy-template kind is not supported"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should generate duplicate policy template err event", func() {
		hubApplyPolicy("case10-test-policy",
			yamlBasePath+"working-policy.yaml")

		// wait for original policy to be processed before creating duplicate policy
		utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
			"case10-config-policy", clusterNamespace, true, defaultTimeoutSeconds)

		hubApplyPolicy("case10-test-policy-duplicate",
			yamlBasePath+"working-policy-duplicate.yaml")

		By("Checking for event with duplicate err on managed cluster in ns:" + clusterNamespace)
		Eventually(
			checkForEvent("case10-test-policy-duplicate", "Template name must be unique"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should create other objects, even when one is invalid", func() {
		hubApplyPolicy("case10-middle-tmpl",
			yamlBasePath+"middle-template-error.yaml")

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
			yamlBasePath+"working-policy.yaml")

		utils.ListWithTimeout(clientManagedDynamic, gvrConfigurationPolicy, metav1.ListOptions{},
			1, true, defaultTimeoutSeconds)

		By("Manually updating the status on the created configuration policy")
		compliancePatch := []byte(`[{"op":"add","path":"/status","value":{"compliant":"testing"}}]`)
		// can't just use kubectl - status is a sub-resource
		cfgInt := clientManagedDynamic.Resource(gvrConfigurationPolicy).Namespace(clusterNamespace)
		_, err := cfgInt.Patch(context.TODO(), "case10-config-policy", types.JSONPatchType,
			compliancePatch, metav1.PatchOptions{}, "status")
		Expect(err).ShouldNot(HaveOccurred())

		By("Patching the policy to make the template invalid")
		errorPatch := []byte(`[{` +
			`"op":"replace",` +
			`"path":"/spec/policy-templates/0/objectDefinition/kind",` +
			`"value":"PretendPolicy"}]`)
		polInt := clientHubDynamic.Resource(gvrPolicy).Namespace(clusterNamespaceOnHub)
		_, err = polInt.Patch(
			context.TODO(), "case10-test-policy", types.JSONPatchType, errorPatch, metav1.PatchOptions{},
		)
		Expect(err).ShouldNot(HaveOccurred())

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
		Expect(err).ShouldNot(HaveOccurred())

		By("Checking that the complianceState is still on the configuration policy")
		cfgPolicy, err := cfgInt.Get(context.TODO(), "case10-config-policy", metav1.GetOptions{}, "status")
		Expect(err).ToNot(HaveOccurred())
		compState, found, err := unstructured.NestedString(cfgPolicy.Object, "status", "compliant")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(compState).To(Equal("testing"))

		By("Re-applying the working policy")
		hubApplyPolicy("case10-test-policy",
			yamlBasePath+"working-policy.yaml")

		By("Checking that the complianceState is removed on the configuration policy")
		Eventually(func() bool {
			cfgPolicy, err := cfgInt.Get(context.TODO(), "case10-config-policy", metav1.GetOptions{}, "status")
			if err != nil {
				return false
			}

			_, found, _ := unstructured.NestedString(cfgPolicy.Object, "status", "compliant")

			return found
		}, defaultTimeoutSeconds*2, 1).Should(BeFalse())
	})
	It("should throw a noncompliance event for hub template errors", func() {
		By("Deploying a test policy CRD")
		_, err := kubectlManaged("apply", "-f", yamlBasePath+"mock-crd.yaml")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() error {
			_, err := kubectlManaged("delete", "-f", yamlBasePath+"mock-crd.yaml")

			return err
		})

		hubApplyPolicy("case10-bad-hubtemplate",
			yamlBasePath+"error-hubtemplate.yaml")

		By("Checking for the error event")
		Eventually(
			checkForEvent("case10-bad-hubtemplate", "must be aboveground"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())

		By("Checking for the compliance message formatting")
		Eventually(
			checkForEvent("case10-bad-hubtemplate", nonCompliantPrefix+nonCompliantPrefix),
			defaultTimeoutSeconds,
			1,
		).Should(BeFalse())
	})
	It("should throw a noncompliance event if the template object is invalid", func() {
		hubApplyPolicy("case10-invalid-severity",
			yamlBasePath+"invalid-severity-template.yaml")

		By("Checking for the error event")
		Eventually(
			checkForEvent("case10-invalid-severity", "Failed to create policy"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())

		By("Checking for the compliance message formatting")
		Eventually(
			checkForEvent("case10-invalid-severity", nonCompliantPrefix+nonCompliantPrefix),
			defaultTimeoutSeconds,
			1,
		).Should(BeFalse())
	})
	It("should not throw a noncompliance event if the policy-templates array is empty", func() {
		hubApplyPolicy("case10-empty-templates",
			yamlBasePath+"empty-templates.yaml")

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
			"--filename="+yamlBasePath+"working-policy-configpol.yaml",
			"--namespace="+clusterNamespace,
		)
		Expect(err).ShouldNot(HaveOccurred())

		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrConfigurationPolicy,
			"case10-config-policy",
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		ExpectWithOffset(1, managedPlc).NotTo(BeNil())

		hubApplyPolicy("case10-test-policy",
			yamlBasePath+"working-policy.yaml")

		By("Checking for the error event")
		Eventually(
			checkForEvent("case10-test-policy", "already exists outside of a Policy"),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should throw a noncompliance event if the both object-templates and object-templates-raw are set", func() {
		hubApplyPolicy("case10-obj-template-conflict",
			yamlBasePath+"multiple-obj-template-arrays.yaml")

		By("Checking for the error event")
		Eventually(
			checkForEvent(
				"case10-obj-template-conflict",
				"spec may only contain one of object-templates and object-templates-raw",
			),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
	})
	It("should only generate one event for a missing kind", func() {
		policyName := "case10-missing-kind"
		hubApplyPolicy(policyName,
			yamlBasePath+"missing-kind.yaml")

		By("Checking for the error event and ensuring it only occurs once")
		Eventually(
			checkForEvent(
				policyName, "Object 'Kind' is missing in",
			),
			defaultTimeoutSeconds,
			1,
		).Should(BeTrue())
		Consistently(
			getMatchingEvents(
				policyName, "Object 'Kind' is missing in",
			),
			defaultTimeoutSeconds,
			1,
		).Should(HaveLen(2))
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

// Checks for an event on the managed cluster
func getMatchingEvents(policyName, msgSubStr string) func() []string {
	return func() []string {
		eventInterface := clientManagedDynamic.Resource(gvrEvent).Namespace(clusterNamespace)

		eventList, err := eventInterface.List(context.TODO(), metav1.ListOptions{
			FieldSelector: "involvedObject.name=" + policyName,
		})
		if err != nil {
			return []string{}
		}

		matchingEvents := []string{}

		for _, event := range eventList.Items {
			msg, found, err := unstructured.NestedString(event.Object, "message")
			if !found || err != nil {
				continue
			}

			if strings.Contains(msg, msgSubStr) {
				matchingEvents = append(matchingEvents, msg)
			}
		}

		return matchingEvents
	}
}
