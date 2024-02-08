package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

var (
	GvkConstraintTemplate = schema.GroupKind{
		Group: "templates.gatekeeper.sh",
		Kind:  "ConstraintTemplate",
	}
	// Explicit allow list for policy groups and kinds--empty fields allow all, but
	// specifying a Group is required (policy CRDs labeled with policy-type=template are allowed by
	// default and do not need to be added to this list)
	policyAllowList = []schema.GroupKind{
		{Group: GvkConstraintTemplate.Group, Kind: GvkConstraintTemplate.Kind},
		{Group: GConstraint},
	}
	ErrNoVersionedResource = errors.New("the resource version was not found")
)

const (
	GConstraint                      = "constraints.gatekeeper.sh"
	PolicyFmtStr                     = "policy: %s/%s"
	PolicyClusterScopedFmtStr        = "policy: %s"
	ClusterwideFinalizer             = common.APIGroup + "/cleanup-cluster-scoped-policies"
	ParentPolicyLabel                = common.APIGroup + "/policy"
	PolicyTypeLabel                  = common.APIGroup + "/policy-type"
	PolicyDBIDAnnotation      string = "policy.open-cluster-management.io/policy-compliance-db-id"
	ParentDBIDAnnotation      string = "policy.open-cluster-management.io/parent-policy-compliance-db-id"
)

type ComplianceAPIEventCluster struct {
	Name      string `json:"name"`
	ClusterID string `json:"cluster_id"` //nolint:tagliatelle
}

type ComplianceAPIEventPolicyID struct {
	ID int32 `json:"id"`
}

type ComplianceAPIEvent struct {
	Compliance policiesv1.ComplianceState `json:"compliance"`
	Message    string                     `json:"message"`
	Timestamp  string                     `json:"timestamp"`
	ReportedBy string                     `json:"reported_by"` //nolint:tagliatelle
}

type ComplianceAPIEventRequest struct {
	UID          types.UID                   `json:"-"`
	Cluster      ComplianceAPIEventCluster   `json:"cluster"`
	Policy       ComplianceAPIEventPolicyID  `json:"policy"`
	ParentPolicy *ComplianceAPIEventPolicyID `json:"parent_policy,omitempty"` //nolint:tagliatelle
	Event        ComplianceAPIEvent          `json:"event"`
}

func GenerateDisabledEvent(
	parentPolicy *policiesv1.Policy, template *unstructured.Unstructured, msg string,
) (ComplianceAPIEventRequest, error) {
	ce := ComplianceAPIEventRequest{}

	tmplAnnotations := template.GetAnnotations()

	pID, err := strconv.ParseInt(tmplAnnotations[PolicyDBIDAnnotation], 10, 32)
	if err != nil {
		return ce, fmt.Errorf("the policy had an invalid policy ID: %s", tmplAnnotations[PolicyDBIDAnnotation])
	}

	ce.Policy = ComplianceAPIEventPolicyID{ID: int32(pID)}

	if parentPolicy.Annotations[ParentDBIDAnnotation] != "" {
		// The parent policy ID is optional so continue even if it's invalid
		ppID, err := strconv.ParseInt(parentPolicy.Annotations[ParentDBIDAnnotation], 10, 32)
		if err == nil {
			ce.ParentPolicy = &ComplianceAPIEventPolicyID{ID: int32(ppID)}
		}
	}

	ce.Event = ComplianceAPIEvent{
		Compliance: "Disabled",
		Message:    msg,
		Timestamp:  time.Now().Format(time.RFC3339Nano),
		ReportedBy: "governance-policy-framework",
	}

	return ce, nil
}

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

// IsAllowedPolicy returns a boolean whether a given GroupKind is present on the explicit allow
// list.
func IsAllowedPolicy(targetGVK schema.GroupKind) bool {
	for _, policyGVK := range policyAllowList {
		if targetGVK.Group == policyGVK.Group &&
			(policyGVK.Kind == "" || targetGVK.Kind == policyGVK.Kind) {
			return true
		}
	}

	return false
}

type ErrList []error

// (ErrList).Aggregate joins an ErrList into a single error separated by semicolons
func (e ErrList) Aggregate() error {
	var err error

	for i, errorItem := range e {
		if i == 0 {
			err = errorItem
		} else {
			err = fmt.Errorf("%s; %w", err.Error(), errorItem)
		}
	}

	return err
}

// GVRFromGVK uses the discovery client to get the versioned resource and determines if the resource is namespaced. If
// the resource is not found or could not be retrieved, an error is always returned.
func GVRFromGVK(
	discoveryClient discovery.DiscoveryInterface, gvk schema.GroupVersionKind,
) (
	schema.GroupVersionResource, bool, error,
) {
	rsrcList, err := discoveryClient.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return schema.GroupVersionResource{}, false, fmt.Errorf("%w: %s", ErrNoVersionedResource, gvk.String())
		}

		return schema.GroupVersionResource{}, false, err
	}

	for _, rsrc := range rsrcList.APIResources {
		if rsrc.Kind == gvk.Kind {
			gvr := schema.GroupVersionResource{
				Group:    gvk.Group,
				Version:  gvk.Version,
				Resource: rsrc.Name,
			}

			return gvr, rsrc.Namespaced, nil
		}
	}

	return schema.GroupVersionResource{}, false, fmt.Errorf(
		"%w: no matching kind was found: %s", ErrNoVersionedResource, gvk.String(),
	)
}
