// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	"github.com/open-cluster-management/governance-policy-propagator/test/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const case6PolicyName string = "default.case6-test-policy"
const case6PolicyYaml string = "../resources/case6_event_msg/case6-test-policy.yaml"

var _ = Describe("Test event message handling", func() {
	BeforeEach(func() {
		By("Creating a policy on hub cluster in ns:" + testNamespace)
		_, err := utils.KubectlWithOutput("apply", "-f", case6PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_hub")
		Expect(err).Should(BeNil())
		hubPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())
		By("Creating a policy on managed cluster in ns:" + testNamespace)
		_, err = utils.KubectlWithOutput("apply", "-f", case6PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
		managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
	})
	AfterEach(func() {
		By("Deleting a policy on hub cluster in ns:" + testNamespace)
		_, err := utils.KubectlWithOutput("delete", "-f", case6PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_hub")
		Expect(err).Should(BeNil())
		_, err = utils.KubectlWithOutput("delete", "-f", case6PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		By("clean up all events")
		_, err = utils.KubectlWithOutput("delete", "events", "-n", testNamespace, "--all",
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
	})
	It("Should remove `(combined from similar events):` prefix but still noncompliant", func() {
		By("Generating an event in ns:" + testNamespace + " that contains `(combined from similar events):` prefix")
		managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(managedPlc, "Warning", "policy: managed/case6-test-policy-trustedcontainerpolicy", fmt.Sprintf("(combined from similar events): NonCompliant; Violation detected"))
		By("Checking if violation message contains the prefix")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return 0
			}
			return len(plc.Status.Details[0].History)
		}, defaultTimeoutSeconds, 1).Should(Equal(1))
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return ""
			}
			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant; Violation detected"))
		By("Checking if policy status is noncompliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
	})
	It("Should remove `(combined from similar events):` prefix but still compliant", func() {
		By("Generating an event in ns:" + testNamespace + " that contains `(combined from similar events):` prefix")
		managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(managedPlc, "Normal", "policy: managed/case6-test-policy-trustedcontainerpolicy", fmt.Sprintf("(combined from similar events): Compliant; no violation detected"))
		By("Checking if violation message contains the prefix")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return 0
			}
			return len(plc.Status.Details[0].History)
		}, defaultTimeoutSeconds, 1).Should(Equal(1))
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return ""
			}
			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant; no violation detected"))
		By("Checking if policy status is compliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
	})
	It("Should handle violation msg with just NonCompliant", func() {
		By("Generating an event in ns:" + testNamespace + " that only contains `NonCompliant`")
		managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(managedPlc, "Warning", "policy: managed/case6-test-policy-trustedcontainerpolicy", fmt.Sprintf("NonCompliant"))
		By("Checking if violation message is in history")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return 0
			}
			return len(plc.Status.Details[0].History)
		}, defaultTimeoutSeconds, 1).Should(Equal(1))
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return ""
			}
			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
		By("Checking if policy status is noncompliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
	})
	It("Should handle violation msg with just Compliant", func() {
		By("Generating an event in ns:" + testNamespace + " that only contains `Compliant`")
		managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(managedPlc, "Normal", "policy: managed/case6-test-policy-trustedcontainerpolicy", fmt.Sprintf("Compliant"))
		By("Checking if violation message is in history")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return 0
			}
			return len(plc.Status.Details[0].History)
		}, defaultTimeoutSeconds, 1).Should(Equal(1))
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return ""
			}
			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("Checking if policy status is compliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(clientManagedDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
	})
})
