// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package sync

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func eventMapper(obj client.Object) []reconcile.Request {
	event := obj.(*corev1.Event)
	log.Info(
		fmt.Sprintf(
			"Reconcile Request for Event %s in namespace %s",
			event.GetName(),
			event.GetNamespace(),
		),
	)

	var result []reconcile.Request
	request := reconcile.Request{NamespacedName: types.NamespacedName{
		Name:      event.InvolvedObject.Name,
		Namespace: event.InvolvedObject.Namespace,
	}}
	log.Info(
		fmt.Sprintf(
			"Queue event for Policy %s in namespace %s",
			event.InvolvedObject.Name,
			event.InvolvedObject.Namespace,
		),
	)
	return append(result, request)
}
