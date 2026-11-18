package main

import (
	gktemplatesv1 "github.com/open-policy-agent/frameworks/constraint/pkg/apis/templates/v1"
	gktemplatesv1beta1 "github.com/open-policy-agent/frameworks/constraint/pkg/apis/templates/v1beta1"
	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(eventsScheme))
	//+kubebuilder:scaffold:scheme

	// Only register the two resources this controller might use, rather than
	// `configv1.Install(scheme)`, which would pull in ~24 kinds.
	scheme.AddKnownTypes(configv1.GroupVersion, &configv1.APIServer{}, &configv1.APIServerList{})
	// AddKnownTypes registers the object kinds only. AddToGroupVersion also registers the
	// group version's meta types (ListOptions, DeleteOptions, WatchEvent) and conversions,
	// which the client and cache need to encode requests for this group.
	metav1.AddToGroupVersion(scheme, configv1.GroupVersion)
	utilruntime.Must(policiesv1.AddToScheme(scheme))
	utilruntime.Must(policiesv1.AddToScheme(eventsScheme))
	utilruntime.Must(extensionsv1.AddToScheme(scheme))
	utilruntime.Must(gktemplatesv1.AddToScheme(scheme))
	utilruntime.Must(gktemplatesv1beta1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
}
