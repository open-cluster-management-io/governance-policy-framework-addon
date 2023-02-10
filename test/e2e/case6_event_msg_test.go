// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test event message handling", func() {
	const (
		case6PolicyName string = "case6-test-policy"
		case6PolicyYaml string = "../resources/case6_event_msg/case6-test-policy.yaml"
	)
	BeforeEach(func() {
		hubApplyPolicy(case6PolicyName, case6PolicyYaml)

		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case6PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
	})
	AfterEach(func() {
		By("Deleting a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub(
			"delete",
			"-f",
			case6PolicyYaml,
			"-n",
			clusterNamespaceOnHub,
		)
		Expect(err).Should(BeNil())
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(
			clientHubDynamic,
			gvrPolicy,
			opt,
			0,
			true,
			defaultTimeoutSeconds)
		utils.ListWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			opt,
			0,
			true,
			defaultTimeoutSeconds)
		By("clean up all events")
		_, err = kubectlManaged(
			"delete",
			"events",
			"-n",
			clusterNamespace,
			"--all",
		)
		Expect(err).Should(BeNil())
	})
	It("Should remove `(combined from similar events):` prefix but still noncompliant", func() {
		By("Generating an event in ns:" + clusterNamespace + " that contains `(combined from similar events):` prefix")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case6PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Warning",
			"policy: managed/case6-test-policy-configurationpolicy",
			"(combined from similar events): NonCompliant; Violation detected")
		By("Checking if violation message contains the prefix")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case6PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return 0
			}

			return len(plc.Status.Details[0].History)
		}, defaultTimeoutSeconds, 1).Should(Equal(1))
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case6PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return ""
			}

			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant; Violation detected"))
		By("Checking if policy status is noncompliant")
		Eventually(checkCompliance(case6PolicyName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
	})
	It("Should remove `(combined from similar events):` prefix but still compliant", func() {
		By("Generating an event in ns:" + clusterNamespace + " that contains `(combined from similar events):` prefix")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case6PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case6-test-policy-configurationpolicy",
			"(combined from similar events): Compliant; no violation detected")
		By("Checking if violation message contains the prefix")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case6PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return 0
			}

			return len(plc.Status.Details[0].History)
		}, defaultTimeoutSeconds, 1).Should(Equal(1))
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case6PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return ""
			}

			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant; no violation detected"))
		By("Checking if policy status is compliant")
		Eventually(checkCompliance(case6PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
	})
	It("Should handle violation msg with just NonCompliant", func() {
		By("Generating an event in ns:" + clusterNamespace + " that only contains `NonCompliant`")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case6PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Warning",
			"policy: managed/case6-test-policy-configurationpolicy",
			"NonCompliant")
		By("Checking if violation message is in history")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case6PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return 0
			}

			return len(plc.Status.Details[0].History)
		}, defaultTimeoutSeconds, 1).Should(Equal(1))
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case6PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return ""
			}

			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
		By("Checking if policy status is noncompliant")
		Eventually(checkCompliance(case6PolicyName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
	})
	It("Should handle violation msg with just Compliant", func() {
		By("Generating an event in ns:" + clusterNamespace + " that only contains `Compliant`")
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case6PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case6-test-policy-configurationpolicy",
			"Compliant")
		By("Checking if violation message is in history")
		var plc *policiesv1.Policy
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case6PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return 0
			}

			return len(plc.Status.Details[0].History)
		}, defaultTimeoutSeconds, 1).Should(Equal(1))
		Eventually(func() interface{} {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case6PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			Expect(err).To(BeNil())
			if len(plc.Status.Details) < 1 {
				return ""
			}

			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("Checking if policy status is compliant")
		Eventually(checkCompliance(case6PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
	})
})
