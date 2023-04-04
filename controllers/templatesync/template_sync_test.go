// Copyright Contributors to the Open Cluster Management project

package templatesync

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/record"
	configpoliciesv1 "open-cluster-management.io/config-policy-controller/api/v1"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func TestHandleSyncSuccessNoDoubleRemoveStatus(t *testing.T) {
	policy := policiesv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Policy",
			APIVersion: "policy.open-cluster-management.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-policy",
			Namespace: "managed",
		},
		Status: policiesv1.PolicyStatus{
			Details: []*policiesv1.DetailsPerTemplate{
				{
					ComplianceState: "NonCompliant",
					History: []policiesv1.ComplianceHistory{
						{
							Message: "template-error; some error",
						},
					},
				},
			},
		},
	}

	configPolicy := configpoliciesv1.ConfigurationPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigurationPolicy",
			APIVersion: "policy.open-cluster-management.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-configpolicy",
			Namespace: "managed",
		},
		Status: configpoliciesv1.ConfigurationPolicyStatus{
			ComplianceState: "",
		},
	}

	scheme := runtime.NewScheme()

	err := policiesv1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("Failed to set up the scheme: %s", err)
	}

	recorder := record.NewFakeRecorder(10)
	client := fake.NewSimpleDynamicClient(scheme, &policy, &configPolicy)
	gvr := schema.GroupVersionResource{
		Group:    configpoliciesv1.GroupVersion.Group,
		Version:  configpoliciesv1.GroupVersion.Version,
		Resource: "configurationpolicies",
	}
	res := client.Resource(gvr)

	unstructConfigPolicy, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&configPolicy)
	if err != nil {
		t.Fatalf("Failed to convert the ConfigurationPolicy to Unstructured: %s", err)
	}

	reconciler := PolicyReconciler{Recorder: recorder}

	err = reconciler.handleSyncSuccess(
		context.TODO(),
		&policy,
		0,
		configPolicy.Name,
		"Successfully created",
		res,
		gvr.GroupVersion(),
		&unstructured.Unstructured{Object: unstructConfigPolicy},
	)
	if err != nil {
		t.Fatalf("handleSyncSuccess failed unexpectedly: %s", err)
	}
}
