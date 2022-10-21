// Copyright Contributors to the Open Cluster Management project

package common

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

const (
	eventFmtStr string = "policy: %s/%s"
)

type EventSender interface {
	GetInstanceName() string
	GetClusterNamespace() string
	GetClientSet() *kubernetes.Clientset
	GetControllerName() string
}

func SendComplianceEvent(e EventSender, instance client.Object, msg string, compliance policyv1.ComplianceState) error {
	if len(instance.GetOwnerReferences()) == 0 {
		return nil // there is nothing to do, since no owner is set
	}

	msg = string(compliance) + "; " + msg
	if len([]rune(msg)) > 1024 {
		msg = string([]rune(msg)[:1021]) + "..."
	}

	// we are making an assumption that the GRC policy has a single owner, or we chose the first owner in the list
	ownerRef := instance.GetOwnerReferences()[0]
	now := time.Now()
	var reason string

	if instance.GetNamespace() == "" {
		reason = fmt.Sprintf(eventFmtStr, "ClusterScoped", instance.GetName())
	} else {
		reason = fmt.Sprintf(eventFmtStr, instance.GetNamespace(), instance.GetName())
	}

	gvk := instance.GetObjectKind().GroupVersionKind()

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			// This event name matches the convention of recorders from client-go
			Name:      fmt.Sprintf("%v.%x", ownerRef.Name, now.UnixNano()),
			Namespace: instance.GetNamespace(),
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       ownerRef.Kind,
			Namespace:  e.GetClusterNamespace(), // k8s ensures owners are always in the same namespace
			Name:       ownerRef.Name,
			UID:        ownerRef.UID,
			APIVersion: ownerRef.APIVersion,
		},

		// TODO: Handle non-namespaced better
		Reason:  reason,
		Message: msg,
		Source: corev1.EventSource{
			Component: e.GetControllerName(),
			Host:      e.GetInstanceName(),
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
			UID:        instance.GetUID(),
			APIVersion: gvk.GroupVersion().String(),
		},
		ReportingController: e.GetControllerName(),
		ReportingInstance:   e.GetInstanceName(),
	}

	if compliance == policyv1.NonCompliant {
		event.Type = "Warning"
	}

	eventClient := e.GetClientSet().CoreV1().Events(e.GetClusterNamespace())
	_, err := eventClient.Create(context.TODO(), event, metav1.CreateOptions{})

	return err
}
