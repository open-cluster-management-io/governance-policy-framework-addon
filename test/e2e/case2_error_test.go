// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test error handling", func() {
	It("should not override remediationAction if doesn't exist on parent policy", func() {
		By("Creating ../resources/case2_error_test/remediation-action-not-exists.yaml on managed cluster in ns:" +
			testNamespace)
		_, err := utils.KubectlWithOutput("apply", "-f",
			"../resources/case2_error_test/remediation-action-not-exists.yaml", "-n", testNamespace)
		Expect(err).Should(BeNil())
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				"case2-remedation-action-not-exists-configpolicy", testNamespace, true,
				defaultTimeoutSeconds)

			return trustedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
		}, defaultTimeoutSeconds, 1).Should(Equal("inform"))
		By("Patching ../resources/case2_error_test/remediation-action-not-exists2.yaml on managed cluster in ns:" +
			testNamespace)
		_, err = utils.KubectlWithOutput("apply", "-f",
			"../resources/case2_error_test/remediation-action-not-exists2.yaml", "-n", testNamespace)
		Expect(err).Should(BeNil())
		By("Checking the case2-remedation-action-not-exists-configpolicy CR")
		yamlTrustedPlc := utils.ParseYaml(
			"../resources/case2_error_test/remedation-action-not-exists-configpolicy.yaml")
		Eventually(func() interface{} {
			trustedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
				"case2-remedation-action-not-exists-configpolicy", testNamespace, true,
				defaultTimeoutSeconds)

			return trustedPlc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlTrustedPlc.Object["spec"]))
		By("Deleting ../resources/case2_error_test/remediation-action-not-exists.yaml to clean up")
		_, err = utils.KubectlWithOutput("delete", "-f",
			"../resources/case2_error_test/remediation-action-not-exists.yaml", "-n", testNamespace)
		Expect(err).Should(BeNil())
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, metav1.ListOptions{}, 0, true, defaultTimeoutSeconds)
	})
	It("should generate decode err event", func() {
		By("Creating ../resources/case2_error_test/template-decode-error.yaml on managed cluster in ns:" +
			testNamespace)
		_, err := utils.KubectlWithOutput("apply", "-f", "../resources/case2_error_test/template-decode-error.yaml",
			"-n", testNamespace)
		Expect(err).Should(BeNil())
		By("Creating event with decode err on managed cluster in ns:" + testNamespace)
		eventList := utils.ListWithTimeout(clientManagedDynamic, gvrEvent, metav1.ListOptions{
			FieldSelector: "involvedObject.name=default.case2-template-decode-error",
		}, 1, true, defaultTimeoutSeconds)
		By("Deleting the event to clean up")
		event := eventList.Items[0]
		_, err = utils.KubectlWithOutput("delete", "event", event.GetName(), "-n", testNamespace)
		Expect(err).Should(BeNil())
		By("Deleting ../resources/case2_error_test/template-decode-error.yaml to clean up")
		_, err = utils.KubectlWithOutput("delete", "-f",
			"../resources/case2_error_test/template-decode-error.yaml", "-n", testNamespace)
		Expect(err).Should(BeNil())
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, metav1.ListOptions{}, 0, true, defaultTimeoutSeconds)
	})
	It("should generate mapping err event", func() {
		By("Creating ../resources/case2_error_test/template-mapping-error.yaml on managed cluster in ns:" +
			testNamespace)
		_, err := utils.KubectlWithOutput("apply", "-f", "../resources/case2_error_test/template-mapping-error.yaml",
			"-n", testNamespace)
		Expect(err).Should(BeNil())
		By("Creating event with decode err on managed cluster in ns:" + testNamespace)
		eventList := utils.ListWithTimeout(clientManagedDynamic, gvrEvent, metav1.ListOptions{
			FieldSelector: "involvedObject.name=default.case2-template-mapping-error",
		}, 2, true, defaultTimeoutSeconds)
		By("Deleting the event to clean up")
		for _, event := range eventList.Items {
			_, err = utils.KubectlWithOutput("delete", "event", event.GetName(), "-n", testNamespace)
			Expect(err).Should(BeNil())
		}
		By("Deleting ../resources/case2_error_test/template-mapping-error.yaml to clean up")
		_, err = utils.KubectlWithOutput("delete", "-f", "../resources/case2_error_test/template-mapping-error.yaml",
			"-n", testNamespace)
		Expect(err).Should(BeNil())
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, metav1.ListOptions{}, 0, true, defaultTimeoutSeconds)
	})
	It("should generate duplicate policy template err event", func() {
		By("Creating ../resources/case2_error_test/working-policy-duplicate.yaml on managed cluster in ns:" +
			testNamespace)
		_, err := utils.KubectlWithOutput("apply", "-f", "../resources/case2_error_test/working-policy.yaml",
			"-n", testNamespace)
		Expect(err).Should(BeNil())
		// wait for original policy to be processed before creating duplicate policy
		time.Sleep(30 * time.Second)
		_, err = utils.KubectlWithOutput("apply", "-f", "../resources/case2_error_test/working-policy-duplicate.yaml",
			"-n", testNamespace)
		Expect(err).Should(BeNil())
		By("Creating event with duplicate err on managed cluster in ns:" + testNamespace)
		eventList := utils.ListWithTimeout(clientManagedDynamic, gvrEvent, metav1.ListOptions{
			FieldSelector: "involvedObject.name=default.case2-test-policy-duplicate",
		}, 2, true, defaultTimeoutSeconds)
		violationStringFound := false
		for _, event := range eventList.Items {
			if event.Object["message"] == "NonCompliant; Template name must be unique. Policy template with kind: "+
				"ConfigurationPolicy name: case2-config-policy already exists in policy "+
				"default.case2-test-policy" {
				violationStringFound = true

				break
			}
		}
		Expect(violationStringFound).To(BeTrue())
		By("Deleting the event to clean up")
		for _, event := range eventList.Items {
			_, err = utils.KubectlWithOutput("delete", "event", event.GetName(), "-n", testNamespace)
			Expect(err).Should(BeNil())
		}
		By("Deleting policies to clean up")
		_, err = utils.KubectlWithOutput("delete", "-f", "../resources/case2_error_test/working-policy.yaml",
			"-n", testNamespace)
		Expect(err).Should(BeNil())
		_, err = utils.KubectlWithOutput("delete", "-f", "../resources/case2_error_test/working-policy-duplicate.yaml",
			"-n", testNamespace)
		Expect(err).Should(BeNil())
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, metav1.ListOptions{}, 0, true, defaultTimeoutSeconds)
	})
})
