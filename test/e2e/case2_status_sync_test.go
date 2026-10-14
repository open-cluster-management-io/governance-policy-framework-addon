// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"encoding/json"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"

	frameworkutils "open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

var _ = Describe("Test status sync", func() {
	const (
		case2PolicyName string = "case2-test-policy"
		case2PolicyYaml string = "../resources/case2_status_sync/case2-test-policy.yaml"
	)

	BeforeEach(func() {
		hubApplyPolicy(case2PolicyName, case2PolicyYaml)
	})
	AfterEach(func() {
		By("Deleting a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub("delete", "-f", case2PolicyYaml, "-n", clusterNamespaceOnHub)
		Expect(err).ToNot(HaveOccurred())

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
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Eventf(
			managedPlc,
			corev1.EventTypeNormal,
			"policy: managed/case2-test-policy-configurationpolicy",
			"Compliant; No violation detected")
		By("Checking if policy status is compliant")
		Eventually(func() any {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case2PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))
		By("Checking if hub policy status is in sync")
		Eventually(func() any {
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
	It("keeps local status current while fenced and resumes hub sync when the marker matches", Label("uid-fence"),
		func(ctx SpecContext) {
			expectedUID := os.Getenv("E2E_MANAGED_CLUSTER_UID")
			if expectedUID == "" {
				Skip("this case requires the uid-fence e2e target to configure the controller UID")
			}

			DeferCleanup(func() {
				_, err := kubectlManaged("delete", "events", "-n", clusterNamespace, "--all")
				Expect(err).ShouldNot(HaveOccurred())
			})

			By("setting a baseline local status and verifying it reaches the hub")

			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic, gvrPolicy, case2PolicyName, clusterNamespace, true, defaultTimeoutSeconds,
			)
			managedRecorder.Eventf(
				managedPlc,
				corev1.EventTypeNormal,
				"policy: managed/case2-test-policy-configurationpolicy",
				"Compliant; baseline for UID fence test",
			)

			Eventually(func() any {
				managedPlc = utils.GetWithTimeout(
					clientManagedDynamic, gvrPolicy, case2PolicyName, clusterNamespace, true, defaultTimeoutSeconds,
				)

				return getCompliant(managedPlc)
			}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

			var hubStatus any

			Eventually(func() any {
				hubPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, clusterNamespaceOnHub, true, defaultTimeoutSeconds,
				)
				hubStatus = runtime.DeepCopyJSONValue(hubPlc.Object["status"])

				return hubStatus
			}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))

			By("changing the hub marker to a different UID")
			setHubPolicyUIDAnnotation(ctx, case2PolicyName, expectedUID+"-stale")

			By("confirming local status still changes while the hub write is fenced")

			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic, gvrPolicy, case2PolicyName, clusterNamespace, true, defaultTimeoutSeconds,
			)
			managedRecorder.Eventf(
				managedPlc,
				corev1.EventTypeWarning,
				"policy: managed/case2-test-policy-configurationpolicy",
				"NonCompliant; status while UID fence is active",
			)

			var nonCompliantStatus any

			Eventually(func() string {
				managedPlc = utils.GetWithTimeout(
					clientManagedDynamic, gvrPolicy, case2PolicyName, clusterNamespace, true, defaultTimeoutSeconds,
				)
				nonCompliantStatus = runtime.DeepCopyJSONValue(managedPlc.Object["status"])

				return getCompliant(managedPlc)
			}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))
			Consistently(func() any {
				hubPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, clusterNamespaceOnHub, true, defaultTimeoutSeconds,
				)

				return hubPlc.Object["status"]
			}, 5*time.Second, time.Second).Should(Equal(hubStatus))

			By("restoring the matching UID and waiting for hub status synchronization to recover")
			setHubPolicyUIDAnnotation(ctx, case2PolicyName, expectedUID)
			Eventually(func() any {
				managedPlc = utils.GetWithTimeout(
					clientManagedDynamic, gvrPolicy, case2PolicyName, clusterNamespace, true, defaultTimeoutSeconds,
				)
				hubPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, clusterNamespaceOnHub, true, defaultTimeoutSeconds,
				)

				return hubPlc.Object["status"]
			}, 45*time.Second, time.Second).Should(Equal(nonCompliantStatus))
		},
	)
	It("Should set status to NonCompliant", func(ctx SpecContext) {
		By("Generating a noncompliant event on the policy")

		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Eventf(
			managedPlc,
			corev1.EventTypeWarning,
			"policy: managed/case2-test-policy-configurationpolicy",
			"NonCompliant; there is violation")

		By("Checking if policy status is noncompliant")
		Eventually(checkCompliance(ctx, case2PolicyName), defaultTimeoutSeconds, 1).Should(Equal("NonCompliant"))

		By("Checking if policy history is correct")

		managedPlc = utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		var plc *policiesv1.Policy
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
		Expect(err).ToNot(HaveOccurred())
		Expect((plc.Status.Details)).To(HaveLen(1))
		Expect((plc.Status.Details[0].History)).To(HaveLen(1))
		Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case2-test-policy-configurationpolicy"))
		By("Checking if hub policy status is in sync")
		Eventually(func() any {
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
	It("Should set status to Compliant again", func(ctx SpecContext) {
		By("Generating an compliant event on the policy")

		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
		managedRecorder.Eventf(
			managedPlc,
			corev1.EventTypeNormal,
			"policy: managed/case2-test-policy-configurationpolicy",
			"Compliant; No violation detected")

		By("Checking if policy status is compliant")
		Eventually(checkCompliance(ctx, case2PolicyName), defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		By("Checking if policy history is correct")

		managedPlc = utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		var plc *policiesv1.Policy
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
		Expect(err).ToNot(HaveOccurred())
		Expect((plc.Status.Details)).To(HaveLen(1))
		Expect((plc.Status.Details[0].History)).To(HaveLen(1))
		Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case2-test-policy-configurationpolicy"))

		By("Checking if the hub policy status is in sync")
		var hubPlc *unstructured.Unstructured

		Eventually(func() any {
			hubPlc = utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case2PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))

		By("Clearing the hub status to verify it gets recovered")
		delete(hubPlc.Object, "status")
		_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(clusterNamespaceOnHub).UpdateStatus(
			ctx, hubPlc, metav1.UpdateOptions{},
		)
		Expect(err).ShouldNot(HaveOccurred())

		Eventually(func() any {
			hubPlc = utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				case2PolicyName,
				clusterNamespaceOnHub,
				true,
				defaultTimeoutSeconds)

			return hubPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(Equal(managedPlc.Object["status"]))

		By("clean up all events")

		_, err = kubectlManaged("delete", "events", "-n", clusterNamespace, "--all")
		Expect(err).ShouldNot(HaveOccurred())
	})
	It("Should hold up to last 10 history", func() {
		By("Generating an a lot of event on the policy")

		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case2PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())

		historyIndex := 1
		for historyIndex < 12 {
			if historyIndex%2 == 0 {
				managedRecorder.Eventf(
					managedPlc,
					corev1.EventTypeNormal,
					"policy: managed/case2-test-policy-configurationpolicy",
					"Compliant; No violation detected %d", historyIndex)
			} else {
				managedRecorder.Eventf(
					managedPlc,
					corev1.EventTypeWarning,
					"policy: managed/case2-test-policy-configurationpolicy",
					"NonCompliant; there is violation %d", historyIndex)
			}

			historyIndex++

			utils.Pause(1)
		}
		var plc *policiesv1.Policy

		By("Generating a no violation event")
		managedRecorder.Eventf(
			managedPlc,
			corev1.EventTypeNormal,
			"policy: managed/case2-test-policy-configurationpolicy",
			"Compliant; No violation assert")
		Eventually(func(g Gomega) any {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case2PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(plc.Status.Details).ToNot(BeEmpty())
			g.Expect(plc.Status.Details[0].History).ToNot(BeEmpty())

			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant; No violation assert"))

		err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
		Expect(err).ToNot(HaveOccurred())
		By("Checking no violation event is the first one in history")
		Expect(plc.Status.ComplianceState).To(Equal(policiesv1.Compliant))
		Expect((plc.Status.Details)).To(HaveLen(1))
		By("Checking if size of the history is 10")
		Expect(plc.Status.Details[0].History).To(HaveLen(10))
		Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case2-test-policy-configurationpolicy"))
		By("Generating a violation event")
		managedRecorder.Eventf(
			managedPlc,
			corev1.EventTypeWarning,
			"policy: managed/case2-test-policy-configurationpolicy",
			"NonCompliant; Violation assert")
		Eventually(func(g Gomega) any {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case2PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			g.Expect(err).ToNot(HaveOccurred())

			return plc.Status.Details[0].History[0].Message
		}, defaultTimeoutSeconds, 1).Should(Equal("NonCompliant; Violation assert"))

		err = runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
		Expect(err).ToNot(HaveOccurred())
		By("Checking violation event is the first one in history")
		Expect(plc.Status.ComplianceState).To(Equal(policiesv1.NonCompliant))
		Expect((plc.Status.Details)).To(HaveLen(1))
		By("Checking if size of the history is 10")
		Expect(plc.Status.Details[0].History).To(HaveLen(10))
		Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case2-test-policy-configurationpolicy"))
		By("Checking if hub policy status is in sync")
		Eventually(func() any {
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

		_, err = kubectlManaged(
			"delete",
			"events",
			"-n",
			clusterNamespace,
			"--all",
		)
		Expect(err).ShouldNot(HaveOccurred())
	})
})

func setHubPolicyUIDAnnotation(ctx SpecContext, policyName, uid string) {
	patchBytes, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{frameworkutils.ManagedClusterUIDAnnotation: uid},
		},
	})
	Expect(err).ToNot(HaveOccurred())

	_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(clusterNamespaceOnHub).Patch(
		ctx,
		policyName,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	Expect(err).ToNot(HaveOccurred())
}
