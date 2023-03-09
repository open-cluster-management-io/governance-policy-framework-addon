// Copyright Contributors to the Open Cluster Management project

package utils

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ComplianceEventSender handles sending policy template status events in the correct format.
type ComplianceEventSender struct {
	ClusterNamespace string
	InstanceName     string
	ClientSet        *kubernetes.Clientset
	ControllerName   string
}

// SendEvent will send a policy template status message update synchronously as opposed to EventRecorder
// sending events in the background asynchronously.
func (c *ComplianceEventSender) SendEvent(
	ctx context.Context,
	instance client.Object,
	owner metav1.OwnerReference,
	msg string,
	compliance policyv1.ComplianceState,
) error {
	msg = string(compliance) + "; " + msg
	if len([]rune(msg)) > 1024 {
		msg = string([]rune(msg)[:1021]) + "..."
	}

	now := time.Now()
	var reason string

	if instance.GetNamespace() == "" {
		reason = "policy: " + instance.GetName()
	} else {
		reason = fmt.Sprintf("policy: %s/%s", instance.GetNamespace(), instance.GetName())
	}

	gvk := instance.GetObjectKind().GroupVersionKind()

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			// This event name matches the convention of recorders from client-go
			Name:      fmt.Sprintf("%v.%x", owner.Name, now.UnixNano()),
			Namespace: c.ClusterNamespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       owner.Kind,
			Namespace:  c.ClusterNamespace, // k8s ensures owners are always in the same namespace
			Name:       owner.Name,
			UID:        owner.UID,
			APIVersion: owner.APIVersion,
		},

		Reason:  reason,
		Message: msg,
		Source: corev1.EventSource{
			Component: c.ControllerName,
			Host:      c.InstanceName,
		},
		FirstTimestamp: metav1.NewTime(now),
		LastTimestamp:  metav1.NewTime(now),
		Count:          1,
		Type:           "Normal",
		EventTime:      metav1.NewMicroTime(now),
		Action:         "ComplianceStateUpdate",
		Related: &corev1.ObjectReference{
			Kind:       gvk.Kind,
			Name:       instance.GetName(),
			Namespace:  instance.GetNamespace(),
			UID:        instance.GetUID(),
			APIVersion: gvk.GroupVersion().String(),
		},
		ReportingController: c.ControllerName,
		ReportingInstance:   c.InstanceName,
	}

	if compliance == policyv1.NonCompliant {
		event.Type = "Warning"
	}

	eventClient := c.ClientSet.CoreV1().Events(c.ClusterNamespace)
	_, err := eventClient.Create(ctx, event, metav1.CreateOptions{})

	return err
}
