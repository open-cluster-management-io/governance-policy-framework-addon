// Copyright Contributors to the Open Cluster Management project

package templatesync

import (
	"context"
	"testing"

	gktemplatesv1 "github.com/open-policy-agent/frameworks/constraint/pkg/apis/templates/v1"
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

func TestHasDuplicateNames(t *testing.T) {
	policy := policiesv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Policy",
			APIVersion: "policy.open-cluster-management.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-policy",
			Namespace: "managed",
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
	}

	outBytes, err := runtime.Encode(unstructured.UnstructuredJSONScheme, &configPolicy)
	if err != nil {
		t.Fatalf("Could not serialize the config policy: %s", err)
	}

	raw := runtime.RawExtension{
		Raw: outBytes,
	}

	x := policiesv1.PolicyTemplate{
		ObjectDefinition: raw,
	}

	policy.Spec.PolicyTemplates = append(policy.Spec.PolicyTemplates, &x)

	has := hasDupName(&policy)
	if has {
		t.Fatal("Unexpected duplicate policy template names")
	}

	// add a gatekeeper constraint template with a duplicate name
	gkt := gktemplatesv1.ConstraintTemplate{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConstraintTemplate",
			APIVersion: "templates.gatekeeper.sh/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-configpolicy",
		},
	}

	outBytes, err = runtime.Encode(unstructured.UnstructuredJSONScheme, &gkt)
	if err != nil {
		t.Fatalf("Could not serialize the constraint template: %s", err)
	}

	y := policiesv1.PolicyTemplate{
		ObjectDefinition: runtime.RawExtension{
			Raw: outBytes,
		},
	}

	policy.Spec.PolicyTemplates = append(policy.Spec.PolicyTemplates, &y)

	has = hasDupName(&policy)
	if !has {
		t.Fatal("Duplicate names for templates not detected")
	}

	// add a gatekeeper constraint with a duplicate name
	gkc := gktemplatesv1.ConstraintTemplate{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ContainerEnvMaxMemory",
			APIVersion: "constraints.gatekeeper.sh/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-configpolicy",
		},
	}

	outBytes, err = runtime.Encode(unstructured.UnstructuredJSONScheme, &gkc)
	if err != nil {
		t.Fatalf("Could not serialize the constraint template: %s", err)
	}

	z := policiesv1.PolicyTemplate{
		ObjectDefinition: runtime.RawExtension{
			Raw: outBytes,
		},
	}

	policy.Spec.PolicyTemplates = append(policy.Spec.PolicyTemplates, &z)

	has = hasDupName(&policy)
	if !has {
		t.Fatal("Duplicate names for templates not detected")
	}

	// add a config policy with a duplicate name
	outBytes, err = runtime.Encode(unstructured.UnstructuredJSONScheme, &configPolicy)
	if err != nil {
		t.Fatalf("Could not serialize the config policy: %s", err)
	}

	x2 := policiesv1.PolicyTemplate{
		ObjectDefinition: runtime.RawExtension{
			Raw: outBytes,
		},
	}

	policy.Spec.PolicyTemplates = append(policy.Spec.PolicyTemplates, &x2)

	has = hasDupName(&policy)
	if !has { // expect duplicate detection to return true
		t.Fatal("Duplicate name not detected")
	}
}
