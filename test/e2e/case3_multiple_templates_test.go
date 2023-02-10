// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

func getCompliant(policy *unstructured.Unstructured) string {
	status, statusOk := policy.Object["status"].(map[string]interface{})
	if !statusOk {
		return ""
	}

	compliant, compliantOk := status["compliant"].(string)
	if !compliantOk {
		return ""
	}

	return compliant
}

var _ = Describe("Test status sync with multiple templates", func() {
	const (
		case3PolicyName string = "case3-test-policy"
		case3PolicyYaml string = "../resources/case3_multiple_templates/case3-test-policy.yaml"
	)

	BeforeEach(func() {
		hubApplyPolicy(case3PolicyName, case3PolicyYaml)
	})
	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			_, err := utils.KubectlWithOutput("-n", clusterNamespace, "get", "events")

			Expect(err).To(BeNil())
		}

		By("Deleting a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("delete", "-f", case3PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).To(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		By("clean up all events")
		_, err = kubectlManaged("delete", "events", "-n", clusterNamespace, "--all")
		Expect(err).To(BeNil())
	})
	It("Should not set overall compliancy to compliant", func() {
		By("Generating an event doesn't belong to any template")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy",
			"Compliant; there is no violation")
		By("Checking if policy status consistently nil")
		Consistently(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, 20, 1).Should(Equal(""))
	})
	It("Should not set overall compliancy to compliant", func() {
		By("Generating an event belong to template: case3-test-policy-configurationpolicy1")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy1",
			"Compliant; there is no violation")
		By("Checking if template: case3-test-policy-configurationpolicy1 status is compliant")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return ""
			}
			Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case3-test-policy-configurationpolicy1"))

			return plc.Status.Details[0].ComplianceState
		}, defaultTimeoutSeconds, 1).Should(Equal(policiesv1.Compliant))
		By("Checking if policy overall status is still nil as only one of two policy templates has status")
		Consistently(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, 20, 1).Should(Equal(""))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
	})
	It("Should not set overall compliancy to compliant", func() {
		By("Generating an event belong to template: case3-test-policy-configurationpolicy2")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy2",
			"Compliant; there is no violation")
		By("Checking if template: case3-test-policy-configurationpolicy2 status is compliant")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 2 {
				return ""
			}
			Expect(plc.Status.Details[1].TemplateMeta.GetName()).To(Equal("case3-test-policy-configurationpolicy2"))

			return plc.Status.Details[1].ComplianceState
		}, defaultTimeoutSeconds, 1).Should(Equal(policiesv1.Compliant))
		By("Checking if policy overall status is still nil as only one of two policy templates has status")
		Consistently(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, 20, 1).Should(Equal(""))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
	})
	It("Should set overall compliancy to compliant", func() {
		By("Generating events belong to both template")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy1",
			"Compliant; there is no violation")
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy2",
			"Compliant; there is no violation")
		By("Checking if policy overall status is compliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
	})
	It("Should set overall compliancy to NonCompliant", func() {
		By("Generating events belong to both template")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy1",
			"Compliant; there is no violation")
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy2",
			"Compliant; there is no violation")
		By("Checking if policy overall status is compliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("Generating violation event for templatecase3-test-policy-configurationpolicy1")
		managedRecorder.Event(
			managedPlc,
			"Warning",
			"policy: managed/case3-test-policy-configurationpolicy1",
			"NonCompliant; there is violation")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
	})
	It("Should set overall compliancy to NonCompliant", func() {
		By("Generating events belong to both template")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy1",
			"Compliant; there is no violation")
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy2",
			"Compliant; there is no violation")
		By("Checking if policy overall status is compliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("Generating violation event for templatecase3-test-policy-configurationpolicy2")
		managedRecorder.Event(
			managedPlc,
			"Warning",
			"policy: managed/case3-test-policy-configurationpolicy2",
			"NonCompliant; there is violation")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
	})
	It("Should remove status when template is removed", func() {
		By("Generating events belong to both template")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy1",
			"Compliant; there is no violation")
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy2",
			"Compliant; there is no violation")
		By("Checking if policy overall status is compliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("Patching policy template to remove template: case3-test-policy-configurationpolicy1")
		_, err := kubectlHub(
			"apply",
			"-f",
			"../resources/case3_multiple_templates/case3-test-policy-without-template1.yaml",
			"-n",
			clusterNamespaceOnHub,
		)
		Expect(err).To(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())
		By("Creating a policy on the hub in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub(
			"apply",
			"-f",
			"../resources/case3_multiple_templates/case3-test-policy-without-template1.yaml",
			"-n",
			clusterNamespaceOnHub,
		)
		Expect(err).To(BeNil())
		managedPlc = utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		By("Checking if policy status of template1 has been removed")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())

			return len(plc.Status.Details)
		}, defaultTimeoutSeconds, 1).Should(Equal(1))
		Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case3-test-policy-configurationpolicy2"))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
	})
	It("Should remove status when template is removed", func() {
		By("Generating events belong to both template")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy1",
			"Compliant; there is no violation")
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case3-test-policy-configurationpolicy2",
			"Compliant; there is no violation")
		By("Checking if policy overall status is compliant")
		Eventually(checkCompliance(case3PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("Patching policy template to remove template: case3-test-policy-configurationpolicy2")
		_, err := kubectlHub(
			"apply",
			"-f",
			"../resources/case3_multiple_templates/case3-test-policy-without-template2.yaml",
			"-n",
			clusterNamespaceOnHub,
		)
		Expect(err).To(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())
		By("Creating a policy on the hub in ns:" + clusterNamespaceOnHub)
		_, err = kubectlHub(
			"apply",
			"-f",
			"../resources/case3_multiple_templates/case3-test-policy-without-template2.yaml",
			"-n",
			clusterNamespaceOnHub,
		)
		Expect(err).To(BeNil())
		managedPlc = utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case3PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		By("Checking if policy status of template2 has been removed")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())

			return len(plc.Status.Details)
		}, defaultTimeoutSeconds, 1).Should(Equal(1))
		Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case3-test-policy-configurationpolicy1"))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case3PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
	})
})
