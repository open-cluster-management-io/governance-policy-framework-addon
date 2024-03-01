// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	case11PolicyYaml string = "../resources/case11_ts_collision/case11-policy.yaml"
	case11PolicyName string = "default.case11-test-policy"
	case11Event1     string = "default.case11-test-policy.171a96193d32cf17"
	case11Event2     string = "default.case11-test-policy.171a96193dea32f4"
	case11Event3     string = "default.case11-test-policy.171a96193dea32f8"
	case11Event4     string = "default.case11-test-policy.four.171a96193d32cf17"
	case11Event5     string = "default.case11-test-policy.five.171a96193d32cf17"
	case11Event6     string = "default.case11-test-policy.six.171a96193d32cf17"
)

func case11Event(ctx context.Context,
	uid types.UID,
	name, namespace, message, evtype string,
	evtime time.Time,
	includeMS bool,
) error {
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			CreationTimestamp: metav1.NewTime(evtime),
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "Policy",
			Namespace:  namespace,
			Name:       "default.case11-test-policy",
			UID:        uid,
			APIVersion: "policy.open-cluster-management.io/v1",
		},
		Reason:  "policy: managed/case11-cfg-policy",
		Message: message,
		Source: corev1.EventSource{
			Component: "configuration-policy-controller",
		},
		FirstTimestamp: metav1.NewTime(evtime),
		LastTimestamp:  metav1.NewTime(evtime),
		Count:          1,
		Type:           evtype,
	}

	if includeMS {
		event.EventTime = metav1.NewMicroTime(evtime)

		// These fields must also be added to satisfy eventsv1.Event validation
		event.Action = "filler"
		event.ReportingController = "filler"
		event.ReportingInstance = "filler"
	}

	_, err := clientManaged.CoreV1().Events(namespace).Create(
		ctx,
		event,
		metav1.CreateOptions{},
	)

	return err
}

func case11cleanup() {
	out, err := kubectlHub(
		"delete", "-f", case11PolicyYaml, "-n", clusterNamespaceOnHub)
	if err != nil {
		Expect(out).Should(ContainSubstring("NotFound"))
	}

	out, err = kubectlManaged(
		"delete", "-f", case11PolicyYaml, "-n", clusterNamespace)
	if err != nil {
		Expect(out).Should(ContainSubstring("NotFound"))
	}

	eventsToDelete := []string{
		case11Event1,
		case11Event2,
		case11Event3,
		case11Event4,
		case11Event5,
		case11Event6,
	}

	for _, evname := range eventsToDelete {
		out, err = kubectlManaged(
			"delete", "event", evname, "-n", clusterNamespace)
		if err != nil {
			Expect(out).Should(ContainSubstring("NotFound"))
		}
	}
}

var _ = Describe("Test event sorting by name when timestamps collide", Ordered, func() {
	var managedUID types.UID

	It("Creates the policy and one event, and shows compliant", func(ctx SpecContext) {
		hubApplyPolicy(case11PolicyName, case11PolicyYaml)

		_, err := kubectlManaged(
			"apply", "-f", case11PolicyYaml, "-n", clusterNamespace,
		)
		Expect(err).ShouldNot(HaveOccurred())

		managedPlc, err := clientManagedDynamic.Resource(gvrPolicy).Namespace(clusterNamespace).Get(
			ctx, case11PolicyName, metav1.GetOptions{},
		)
		Expect(err).ShouldNot(HaveOccurred())
		managedUID = managedPlc.GetUID()

		Expect(case11Event(
			ctx,
			managedUID,
			case11Event1,
			clusterNamespace,
			"Compliant; notification - this is the oldest event",
			"Normal",
			time.Date(2022, 10, 3, 14, 40, 47, 0, time.UTC),
			false,
		)).Should(Succeed())

		Eventually(checkCompliance(case11PolicyName), defaultTimeoutSeconds, 1).
			Should(Equal("Compliant"))
		Consistently(checkCompliance(case11PolicyName), "15s", 1).
			Should(Equal("Compliant"))
	})

	It("Creates a second event with the same timestamp, and shows noncompliant", func(ctx SpecContext) {
		Expect(case11Event(
			ctx,
			managedUID,
			case11Event2,
			clusterNamespace,
			"NonCompliant; violation - a problem sandwich",
			"Warning",
			time.Date(2022, 10, 3, 14, 40, 47, 0, time.UTC),
			false,
		)).Should(Succeed())

		Eventually(checkCompliance(case11PolicyName), defaultTimeoutSeconds, 1).
			Should(Equal("NonCompliant"))
		Consistently(checkCompliance(case11PolicyName), "15s", 1).
			Should(Equal("NonCompliant"))
	})

	It("Creates a third with the same timestamp, and shows compliant", func(ctx SpecContext) {
		Expect(case11Event(
			ctx,
			managedUID,
			case11Event3,
			clusterNamespace,
			"Compliant; notification - this should be the most recent",
			"Normal",
			time.Date(2022, 10, 3, 14, 40, 47, 0, time.UTC),
			false,
		)).Should(Succeed())

		Eventually(checkCompliance(case11PolicyName), defaultTimeoutSeconds, 1).
			Should(Equal("Compliant"))
		Consistently(checkCompliance(case11PolicyName), "15s", 1).
			Should(Equal("Compliant"))
	})

	AfterAll(case11cleanup)
})

var _ = Describe("Test event sorting by eventtime when timestamps collide", Ordered, func() {
	var managedUID types.UID

	It("Creates the policy and one event, and shows compliant", func(ctx SpecContext) {
		hubApplyPolicy(case11PolicyName, case11PolicyYaml)

		_, err := kubectlManaged(
			"apply", "-f", case11PolicyYaml, "-n", clusterNamespace,
		)
		Expect(err).ShouldNot(HaveOccurred())

		managedPlc, err := clientManagedDynamic.Resource(gvrPolicy).Namespace(clusterNamespace).Get(
			ctx, case11PolicyName, metav1.GetOptions{},
		)
		Expect(err).ShouldNot(HaveOccurred())
		managedUID = managedPlc.GetUID()

		Expect(case11Event(
			ctx,
			managedUID,
			case11Event4,
			clusterNamespace,
			"Compliant; notification - this is the oldest event",
			"Normal",
			time.Date(2022, 10, 3, 14, 40, 47, 111111, time.UTC),
			true,
		)).Should(Succeed())

		Eventually(checkCompliance(case11PolicyName), defaultTimeoutSeconds, 1).
			Should(Equal("Compliant"))
		Consistently(checkCompliance(case11PolicyName), "15s", 1).
			Should(Equal("Compliant"))
	})

	It("Creates a second event with the same timestamp, and shows noncompliant", func(ctx SpecContext) {
		Expect(case11Event(
			ctx,
			managedUID,
			case11Event5,
			clusterNamespace,
			"NonCompliant; violation - a problem sandwich",
			"Warning",
			time.Date(2022, 10, 3, 14, 40, 47, 222222, time.UTC),
			true,
		)).Should(Succeed())

		Eventually(checkCompliance(case11PolicyName), defaultTimeoutSeconds, 1).
			Should(Equal("NonCompliant"))
		Consistently(checkCompliance(case11PolicyName), "15s", 1).
			Should(Equal("NonCompliant"))
	})

	It("Creates a third with the same timestamp, and shows compliant", func(ctx SpecContext) {
		Expect(case11Event(
			ctx,
			managedUID,
			case11Event6,
			clusterNamespace,
			"Compliant; notification - this should be the most recent",
			"Warning",
			time.Date(2022, 10, 3, 14, 40, 47, 333333, time.UTC),
			true,
		)).Should(Succeed())

		Eventually(checkCompliance(case11PolicyName), defaultTimeoutSeconds, 1).
			Should(Equal("Compliant"))
		Consistently(checkCompliance(case11PolicyName), "15s", 1).
			Should(Equal("Compliant"))
	})

	AfterAll(case11cleanup)
})
