// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package statussync

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func eventMapper(_ context.Context, obj client.Object) []reconcile.Request {
	//nolint:forcetypeassert
	event := obj.(*corev1.Event)

	log := log.WithValues("eventName", event.GetName(), "eventNamespace", event.GetNamespace())

	log.V(2).Info("Reconcile Request")

	var result []reconcile.Request

	request := reconcile.Request{NamespacedName: types.NamespacedName{
		Name:      event.InvolvedObject.Name,
		Namespace: event.InvolvedObject.Namespace,
	}}

	log.V(2).Info("Queueing event", "involvedName", event.InvolvedObject.Name,
		"involvedNamespace", event.InvolvedObject.Namespace)

	return append(result, request)
}
