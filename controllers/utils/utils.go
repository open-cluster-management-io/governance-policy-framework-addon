package utils

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

var (
	GvkConstraintTemplate = schema.GroupKind{
		Group: "templates.gatekeeper.sh",
		Kind:  "ConstraintTemplate",
	}
	GConstraint = "constraints.gatekeeper.sh"
)

const (
	PolicyFmtStr              = "policy: %s/%s"
	PolicyClusterScopedFmtStr = "policy: %s"
	ClusterwideFinalizer      = common.APIGroup + "/cleanup-cluster-scoped-policies"
	ParentPolicyLabel         = common.APIGroup + "/policy"
	PolicyTypeLabel           = common.APIGroup + "/policy-type"
)

// EquivalentReplicatedPolicies compares replicated policies. Returns true if they match. (Comparing
// labels is skipped here in part because in hosted mode the cluster-namespace label likely will not
// match.)
func EquivalentReplicatedPolicies(plc1 *policiesv1.Policy, plc2 *policiesv1.Policy) bool {
	// Compare annotations
	if !equality.Semantic.DeepEqual(plc1.GetAnnotations(), plc2.GetAnnotations()) {
		return false
	}

	// Compare the specs
	return equality.Semantic.DeepEqual(plc1.Spec, plc2.Spec)
}

// ApplyObjectDefaults marshals an object to JSON using its scheme in order to fill in default
// fields that would be added on applying the object to the cluster.
func ApplyObjectDefaults(scheme runtime.Scheme, object *unstructured.Unstructured) error {
	objectTyped, err := scheme.New(object.GroupVersionKind())
	if err != nil {
		if runtime.IsNotRegisteredError(err) {
			return nil
		}

		return err
	}

	errDefault := fmt.Sprintf(
		"an unexpected error occurred while filling in default fields for the %s: %%w",
		objectTyped.GetObjectKind().GroupVersionKind().Kind,
	)

	err = runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, objectTyped)
	if err != nil {
		return fmt.Errorf(errDefault, err)
	}

	scheme.Default(objectTyped)

	objectRaw, err := json.Marshal(objectTyped)
	if err != nil {
		return fmt.Errorf(errDefault, err)
	}

	objectMap := map[string]interface{}{}

	err = json.Unmarshal(objectRaw, &objectMap)
	if err != nil {
		return fmt.Errorf(errDefault, err)
	}

	object.Object = objectMap

	return nil
}
