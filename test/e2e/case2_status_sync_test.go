// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case2PolicyName string = "default.case2-test-policy"
	case2PolicyYaml string = "../resources/case2_status_sync/case2-test-policy.yaml"
)

// const testNamespace string = "managed"

var _ = Describe("Test status sync", func() {
	BeforeEach(func() {
		By("Creating a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := utils.KubectlWithOutput("apply", "-f", case2PolicyYaml, "-n", clusterNamespaceOnHub,
			"--kubeconfig=../../kubeconfig_hub")
		Expect(err).To(BeNil())
		hubPlc := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			case2PolicyName,
			clusterNamespaceOnHub,
			true,
			defaultTimeoutSeconds)
		Expect(hubPlc).NotTo(BeNil())
		By("Creating a policy on managed cluster in ns:" + testNamespace)
		_, err = utils.KubectlWithOutput("apply", "-f", case2PolicyYaml, "-n", testNamespace,
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).To(BeNil())
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
	})
	AfterEach(func() {
		By("Deleting a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := utils.KubectlWithOutput("delete", "-f", case2PolicyYaml, "-n", clusterNamespaceOnHub,
			"--kubeconfig=../../kubeconfig_hub")
		Expect(err).To(BeNil())
		_, err = utils.KubectlWithOutput(
			"delete",
			"-f",
			case2PolicyYaml,
			"-n",
			testNamespace,
			"--ignore-not-found",
			"--kubeconfig=../../kubeconfig_managed",
		)
		Expect(err).To(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		utils.ListWithTimeout(clientManagedDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("Should set status to compliant", func() {
		By("Generating an compliant event on the policy")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case2-test-policy-trustedcontainerpolicy",
			"Compliant; No violation detected")
		By("Checking if policy status is compliant")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case2PolicyName,
				testNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case2PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
	})
	It("Should set status to NonCompliant", func() {
		By("Generating an compliant event on the policy")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Warning",
			"policy: managed/case2-test-policy-trustedcontainerpolicy",
			"NonCompliant; there is violation")
		By("Checking if policy status is noncompliant")
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case2PolicyName,
				testNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
		By("Checking if policy history is correct")
		managedPlc = utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		var plc *policiesv1.Policy
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
		Expect(err).To(BeNil())
		Expect(len(plc.Status.Details)).To(Equal(1))
		Expect(len(plc.Status.Details[0].History)).To(Equal(2))
		Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case2-test-policy-trustedcontainerpolicy"))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case2PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
	})
	It("Should set status to Compliant again", func() {
		By("Generating an compliant event on the policy")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case2-test-policy-trustedcontainerpolicy",
			"Compliant; No violation detected")
		By("Checking if policy status is compliant")
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case2PolicyName,
				testNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("Checking if policy history is correct")
		managedPlc = utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		var plc *policiesv1.Policy
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
		Expect(err).To(BeNil())
		Expect(len(plc.Status.Details)).To(Equal(1))
		Expect(len(plc.Status.Details[0].History)).To(Equal(3))
		Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case2-test-policy-trustedcontainerpolicy"))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case2PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
		By("clean up all events")
		_, err = utils.KubectlWithOutput("delete", "events", "-n", testNamespace, "--all",
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
	})
	It("Should hold up to last 10 history", func() {
		By("Generating an a lot of event on the policy")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			testNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		historyIndex := 1
		for historyIndex < 12 {
			if historyIndex%2 == 0 {
				managedRecorder.Event(
					managedPlc,
					"Normal",
					"policy: managed/case2-test-policy-trustedcontainerpolicy",
					fmt.Sprintf("Compliant; No violation detected %d", historyIndex))
			} else {
				managedRecorder.Event(
					managedPlc,
					"Warning",
					"policy: managed/case2-test-policy-trustedcontainerpolicy",
					fmt.Sprintf("NonCompliant; there is violation %d", historyIndex))
			}
			historyIndex++
			utils.Pause(1)
		}
		var plc *policiesv1.Policy
		By("Generating a no violation event")
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case2-test-policy-trustedcontainerpolicy",
			"Compliant; No violation assert")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case2PolicyName,
				testNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())

			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant; No violation assert"))
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
		Expect(err).To(BeNil())
		By("Checking no violation event is the first one in history")
		Expect(plc.Status.ComplianceState).To(Equal(policiesv1.Compliant))
		Expect(len(plc.Status.Details)).To(Equal(1))
		By("Checking if size of the history is 10")
		Expect(len(plc.Status.Details[0].History)).To(Equal(10))
		Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case2-test-policy-trustedcontainerpolicy"))
		By("Generating a violation event")
		managedRecorder.Event(
			managedPlc,
			"Warning",
			"policy: managed/case2-test-policy-trustedcontainerpolicy",
			"NonCompliant; Violation assert")
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case2PolicyName,
				testNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())

			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant; Violation assert"))
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
		Expect(err).To(BeNil())
		By("Checking violation event is the first one in history")
		Expect(plc.Status.ComplianceState).To(Equal(policiesv1.NonCompliant))
		Expect(len(plc.Status.Details)).To(Equal(1))
		By("Checking if size of the history is 10")
		Expect(len(plc.Status.Details[0].History)).To(Equal(10))
		Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case2-test-policy-trustedcontainerpolicy"))
		By("Checking if hub policy status is in sync")
		Eventually(func() interface{} {
			hubPlc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case2PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))
		By("clean up all events")
		_, err = utils.KubectlWithOutput(
			"delete",
			"events",
			"-n",
			testNamespace,
			"--all",
			"--kubeconfig=../../kubeconfig_managed")
		Expect(err).Should(BeNil())
	})
})
