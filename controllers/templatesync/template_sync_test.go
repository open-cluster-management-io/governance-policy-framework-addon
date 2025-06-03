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

func TestGetDupName(t *testing.T) {
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

	dupName := getDupName(&policy)
	if dupName != "" {
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

	dupName = getDupName(&policy)
	if dupName != "test-configpolicy" {
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

	dupName = getDupName(&policy)
	if dupName != "test-configpolicy" {
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

	dupName = getDupName(&policy)
	if dupName != "test-configpolicy" { // expect duplicate detection to return true
		t.Fatal("Duplicate name not detected")
	}
}

func TestEquivalentTemplatesRecreateOption(t *testing.T) {
	existing := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "policy.open-cluster-management.io/v1",
			"kind":       "ConfigurationPolicy",
			"metadata": map[string]interface{}{
				"name":      "my-policy",
				"namespace": "local-cluster",
			},
			"spec": map[string]interface{}{
				"pruneObjectBehavior": "None",
				"object-templates": []interface{}{
					map[string]interface{}{
						"complianceType": "musthave",
						"recreateOption": "None",
						"objectDefinition": map[string]interface{}{
							"apiVersion": "v1",
							"kind":       "Pod",
							"metadata": map[string]interface{}{
								"name":      "nginx-pod-e2e",
								"namespace": "default",
							},
						},
					},
				},
			},
		},
	}

	template := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "policy.open-cluster-management.io/v1",
			"kind":       "ConfigurationPolicy",
			"metadata": map[string]interface{}{
				"name":      "my-policy",
				"namespace": "local-cluster",
			},
			"spec": map[string]interface{}{
				"object-templates": []interface{}{
					map[string]interface{}{
						"complianceType": "musthave",
						"objectDefinition": map[string]interface{}{
							"apiVersion": "v1",
							"kind":       "Pod",
							"metadata": map[string]interface{}{
								"name":      "nginx-pod-e2e",
								"namespace": "default",
							},
						},
					},
				},
			},
		},
	}

	if !equivalentTemplates(existing, template) {
		t.Fatal("Expected the templates to be equivalent")
	}
}

func TestEquivalentTemplatesOperatorPolicyComplianceConfig(t *testing.T) {
	existing := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "policy.open-cluster-management.io/v1beta1",
			"kind":       "OperatorPolicy",
			"metadata": map[string]interface{}{
				"name": "test-policy",
			},
			"spec": map[string]interface{}{
				"remediationAction": "inform",
				"severity":          "medium",
				"complianceType":    "musthave",
				"subscription:": map[string]interface{}{
					"channel":         "stable-3.10",
					"name":            "project-quay",
					"namespace":       "operator-policy-testns",
					"source":          "operatorhubio-catalog",
					"sourceNamespace": "olm",
					"startingCSV":     "quay-operator.v3.10.0",
				},
				"upgradeApproval": "Automatic",
				"complianceConfig": map[string]interface{}{
					"catalogSourceUnhealthy": "Compliant",
					"deploymentsUnavailable": "NonCompliant",
					"upgradesAvailable":      "Compliant",
				},
			},
		},
	}

	template := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "policy.open-cluster-management.io/v1beta1",
			"kind":       "OperatorPolicy",
			"metadata": map[string]interface{}{
				"name": "test-policy",
			},
			"spec": map[string]interface{}{
				"remediationAction": "inform",
				"severity":          "medium",
				"complianceType":    "musthave",
				"subscription:": map[string]interface{}{
					"channel":         "stable-3.10",
					"name":            "project-quay",
					"namespace":       "operator-policy-testns",
					"source":          "operatorhubio-catalog",
					"sourceNamespace": "olm",
					"startingCSV":     "quay-operator.v3.10.0",
				},
				"upgradeApproval": "Automatic",
			},
		},
	}

	if !equivalentTemplates(existing, template) {
		t.Fatal("Expected the templates to be equivalent")
	}
}

func TestEquivalentTemplatesExtraMetadata(t *testing.T) {
	existing := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "templates.gatekeeper.sh/v1",
			"kind":       "ConstraintTemplate",
			"metadata": map[string]interface{}{
				"name": "k8srequiredlabels",
			},
			"spec": map[string]interface{}{
				"crd": "fake",
			},
		},
	}

	existing.SetAnnotations(map[string]string{
		"gatekeeper.sh/block-vapb-generation-until": "the-future",
	})
	existing.SetLabels(map[string]string{
		"gatekeeper.sh/special": "fake",
	})

	template := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "templates.gatekeeper.sh/v1",
			"kind":       "ConstraintTemplate",
			"metadata": map[string]interface{}{
				"name": "k8srequiredlabels",
			},
			"spec": map[string]interface{}{
				"crd": "fake",
			},
		},
	}

	if !equivalentTemplates(existing, template) {
		t.Fatal("Expected the templates to be equivalent - the existing object has extra metadata")
	}

	// Note the positions have swapped!
	if equivalentTemplates(template, existing) {
		t.Fatal("Expected the templates not to be equivalent - the template has extra metadata")
	}
}

func TestGetDepNamespace(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		gvk       schema.GroupVersionKind
		namespace string
		expected  string
	}{
		"operator-policy-no-ns": {
			gvk: schema.GroupVersionKind{
				Group: policiesv1.GroupVersion.Group, Version: "v1beta1", Kind: "OperatorPolicy",
			},
			namespace: "",
			expected:  "local-cluster",
		},
		"operator-policy-with-ns": {
			gvk: schema.GroupVersionKind{
				Group: policiesv1.GroupVersion.Group, Version: "v1beta1", Kind: "OperatorPolicy",
			},
			namespace: "something",
			expected:  "local-cluster",
		},
		"config-policy-no-ns": {
			gvk: schema.GroupVersionKind{
				Group: policiesv1.GroupVersion.Group, Version: "v1", Kind: "ConfigurationPolicy",
			},
			namespace: "",
			expected:  "local-cluster",
		},
		"config-policy-with-ns": {
			gvk: schema.GroupVersionKind{
				Group: policiesv1.GroupVersion.Group, Version: "v1", Kind: "ConfigurationPolicy",
			},
			namespace: "something",
			expected:  "local-cluster",
		},
		"other-with-ns": {
			gvk: schema.GroupVersionKind{
				Group: "other", Version: "v1", Kind: "ConfigurationPolicy",
			},
			namespace: "something",
			expected:  "something",
		},
	}

	for testName, test := range tests {
		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			dep := policiesv1.PolicyDependency{
				TypeMeta: metav1.TypeMeta{
					Kind:       test.gvk.Kind,
					APIVersion: test.gvk.GroupVersion().String(),
				},
				Name:      "some-policy",
				Namespace: test.namespace,
			}

			depNamespace := getDepNamespace("local-cluster", dep)

			if depNamespace != test.expected {
				t.Fatalf("Expected namespace %s got %s", test.expected, depNamespace)
			}
		})
	}
}
