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
	reason string,
	msg string,
	compliance policyv1.ComplianceState,
) error {
	msg = string(compliance) + "; " + msg

	now := time.Now()

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
		FirstTimestamp:      metav1.NewTime(now),
		LastTimestamp:       metav1.NewTime(now),
		Count:               1,
		Action:              "ComplianceStateUpdate",
		ReportingController: c.ControllerName,
		ReportingInstance:   c.InstanceName,
	}

	if instance != nil {
		gvk := instance.GetObjectKind().GroupVersionKind()

		event.Related = &corev1.ObjectReference{
			Kind:       gvk.Kind,
			Name:       instance.GetName(),
			Namespace:  instance.GetNamespace(),
			UID:        instance.GetUID(),
			APIVersion: gvk.GroupVersion().String(),
		}

		eventAnnotations := map[string]string{}

		instanceAnnotations := instance.GetAnnotations()
		if instanceAnnotations[ParentDBIDAnnotation] != "" {
			eventAnnotations[ParentDBIDAnnotation] = instanceAnnotations[ParentDBIDAnnotation]
		}

		if instanceAnnotations[PolicyDBIDAnnotation] != "" {
			eventAnnotations[PolicyDBIDAnnotation] = instanceAnnotations[PolicyDBIDAnnotation]
		}

		if len(eventAnnotations) > 0 {
			event.Annotations = eventAnnotations
		}
	}

	if compliance == policyv1.Compliant {
		event.Type = "Normal"
	} else {
		event.Type = "Warning"
	}

	eventClient := c.ClientSet.CoreV1().Events(c.ClusterNamespace)
	_, err := eventClient.Create(ctx, event, metav1.CreateOptions{})

	return err
}

func EventReason(ns, name string) string {
	if ns == "" {
		return fmt.Sprintf(PolicyClusterScopedFmtStr, name)
	}

	return fmt.Sprintf(PolicyFmtStr, ns, name)
}
