// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"fmt"
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
)

var _ = Describe("Test status sync with multiple templates", Ordered, func() {
	const (
		case4PolicyName       string = "case4-test-policy"
		case4ConfigPolicyName string = "case4-test-policy-configurationpolicy"
		case4PolicyYaml       string = "../resources/case4_status_merge/case4-test-policy.yaml"
		pastTimeStr           string = "2024-12-23T17:01:23.456789Z"
		futureTimeStr         string = "2048-12-23T17:01:23.456789Z"
	)

	BeforeAll(func() {
		hubApplyPolicy(case4PolicyName, case4PolicyYaml)

		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case4PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		Expect(managedPlc).NotTo(BeNil())
	})

	AfterAll(func() {
		By("Deleting a policy on hub cluster in ns:" + clusterNamespaceOnHub)
		_, err := kubectlHub(
			"delete",
			"-f",
			case4PolicyYaml,
			"-n",
			clusterNamespaceOnHub,
		)
		Expect(err).ShouldNot(HaveOccurred())
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
		Expect(err).ShouldNot(HaveOccurred())
	})

	case4Event := func(ctx context.Context, uid types.UID, message string, evtime time.Time) error {
		event := &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				// This event name matches the convention of recorders from client-go
				Name:              fmt.Sprintf("%v.%x", case4PolicyName, evtime.UnixNano()),
				Namespace:         clusterNamespace,
				CreationTimestamp: metav1.NewTime(evtime),
			},
			InvolvedObject: corev1.ObjectReference{
				Kind:       "Policy",
				Namespace:  clusterNamespace,
				Name:       case4PolicyName,
				UID:        uid,
				APIVersion: "policy.open-cluster-management.io/v1",
			},
			Reason:  fmt.Sprintf("policy: %v/%v", clusterNamespace, case4PolicyName),
			Message: message,
			Source: corev1.EventSource{
				Component: "configuration-policy-controller",
			},
			FirstTimestamp: metav1.NewTime(evtime),
			LastTimestamp:  metav1.NewTime(evtime),
			Count:          1,
			Type:           "Normal",
		}

		_, err := clientManaged.CoreV1().Events(clusterNamespace).Create(ctx, event, metav1.CreateOptions{})

		return err
	}

	It("Should merge existing status with new status from event", func() {
		By("Generating some events in ns:" + clusterNamespace)
		managedPlc := utils.GetWithTimeout(
			clientManagedDynamic,
			gvrPolicy,
			case4PolicyName,
			clusterNamespace,
			true,
			defaultTimeoutSeconds)
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case4-test-policy-configurationpolicy",
			"Compliant; No violation detected")

		By("Checking if policy status is compliant")
		Eventually(func() any {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case4PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)

			return getCompliant(managedPlc)
		}, defaultTimeoutSeconds, 1).Should(Equal("Compliant"))

		// Wait for slow events to show up, otherwise the delete might not get all of them.
		time.Sleep(15 * time.Second)

		By("Delete events in ns:" + clusterNamespace)
		_, err := kubectlManaged(
			"delete",
			"event",
			"-n",
			clusterNamespace,
			"--all",
		)
		Expect(err).ShouldNot(HaveOccurred())
		utils.ListWithTimeout(
			clientManagedDynamic,
			gvrEvent,
			metav1.ListOptions{FieldSelector: "involvedObject.name=case4-test-policy,reason!=PolicyStatusSync"},
			0,
			true,
			defaultTimeoutSeconds)

		By("Generating some new events in ns:" + clusterNamespace)
		managedRecorder.Event(
			managedPlc,
			"Warning",
			"policy: managed/case4-test-policy-configurationpolicy",
			"NonCompliant; Violation detected")
		managedRecorder.Event(
			managedPlc,
			"Normal",
			"policy: managed/case4-test-policy-configurationpolicy",
			"Compliant; Violation no longer detected")

		By("Checking if history size = 3")
		var plc *policiesv1.Policy
		Eventually(func(g Gomega) any {
			managedPlc = utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case4PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case4-test-policy-configurationpolicy"))

			return plc.Status.Details[0].History
		}, defaultTimeoutSeconds, 1).Should(HaveLen(3))
	})

	It("Should merge status with new status on the template", func() {
		ev1String := `{"lastTimestamp": "` + pastTimeStr + `", "message": "compliant; happy festivus"}`
		ev2String := `{"lastTimestamp": "` + futureTimeStr + `", "message": "compliant; happy festivus again"}`

		_, err := kubectlManaged("patch", "configurationpolicy", case4ConfigPolicyName, "-n="+clusterNamespace,
			"--type=merge", "--subresource=status", `-p={"status": {"history": [`+ev1String+","+ev2String+`]}}`)
		Expect(err).ToNot(HaveOccurred())

		time.Sleep(2 * time.Second)

		configpol := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
			case4ConfigPolicyName, clusterNamespace, true, defaultTimeoutSeconds)
		Expect(configpol).ToNot(BeNil())

		evs, found, err := unstructured.NestedSlice(configpol.Object, "status", "history")
		Expect(found).To(BeTrue())
		Expect(err).ToNot(HaveOccurred())
		Expect(evs).To(HaveLen(2))

		By("Checking if history size = 5")
		var plc *policiesv1.Policy
		Eventually(func(g Gomega) any {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case4PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case4-test-policy-configurationpolicy"))

			return plc.Status.Details[0].History
		}, defaultTimeoutSeconds, 1).Should(HaveLen(5))

		By("Verifying that the events from the template status are sorted correctly")
		Expect(plc.Status.Details[0].History[0].EventName).To(
			Equal("case4-test-policy.2296a049fe5e2a08"))
		Expect(plc.Status.Details[0].History[4].EventName).To(
			Equal("case4-test-policy.1813dd024f3c2a08"))
	})

	It("Should de-duplicate an event present in both the template and on the cluster", func(ctx context.Context) {
		configpol := utils.GetWithTimeout(clientManagedDynamic, gvrConfigurationPolicy,
			case4ConfigPolicyName, clusterNamespace, true, defaultTimeoutSeconds)
		Expect(configpol).ToNot(BeNil())

		By("Emitting the 'future' event that is already in the template status")
		future, err := time.Parse(time.RFC3339Nano, futureTimeStr)
		Expect(err).NotTo(HaveOccurred())

		err = case4Event(ctx, configpol.GetUID(), "compliant; happy festivus again", future)
		Expect(err).NotTo(HaveOccurred())

		By("Checking if history size remains constant")
		var plc *policiesv1.Policy
		Consistently(func(g Gomega) any {
			managedPlc := utils.GetWithTimeout(
				clientManagedDynamic,
				gvrPolicy,
				case4PolicyName,
				clusterNamespace,
				true,
				defaultTimeoutSeconds)
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(managedPlc.Object, &plc)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(plc.Status.Details[0].TemplateMeta.GetName()).To(Equal("case4-test-policy-configurationpolicy"))

			return plc.Status.Details[0].History
		}, defaultTimeoutSeconds/2, 1).Should(HaveLen(5))
	})
})
