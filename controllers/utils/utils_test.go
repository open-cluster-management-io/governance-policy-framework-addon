package utils

import (
	"testing"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func TestEquivalentReplicatedPoliciesIgnoresManagedClusterUID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		first     map[string]string
		second    map[string]string
		wantEqual bool
	}{
		{
			name: "different managed cluster UIDs are ignored",
			first: map[string]string{
				ManagedClusterUIDAnnotation: "old-uid",
				"user.example.com/setting":  "same",
			},
			second: map[string]string{
				ManagedClusterUIDAnnotation: "new-uid",
				"user.example.com/setting":  "same",
			},
			wantEqual: true,
		},
		{
			name:      "internal-only annotation equals no annotations",
			first:     map[string]string{ManagedClusterUIDAnnotation: "uid"},
			second:    nil,
			wantEqual: true,
		},
		{
			name: "other annotation differences are retained",
			first: map[string]string{
				ManagedClusterUIDAnnotation: "uid",
				"user.example.com/setting":  "old",
			},
			second:    map[string]string{"user.example.com/setting": "new"},
			wantEqual: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first := &policiesv1.Policy{}
			first.SetAnnotations(tt.first)

			second := &policiesv1.Policy{}
			second.SetAnnotations(tt.second)

			if got := EquivalentReplicatedPolicies(first, second); got != tt.wantEqual {
				t.Errorf("EquivalentReplicatedPolicies() = %v, want %v", got, tt.wantEqual)
			}
		})
	}
}

func TestPolicyAnnotationsForSpoke(t *testing.T) {
	t.Parallel()

	source := map[string]string{
		ManagedClusterUIDAnnotation: "hub-only-uid",
		"user.example.com/setting":  "preserved",
	}

	got := PolicyAnnotationsForSpoke(source)
	if _, found := got[ManagedClusterUIDAnnotation]; found {
		t.Fatal("PolicyAnnotationsForSpoke() retained the ManagedCluster UID annotation")
	}

	if got["user.example.com/setting"] != "preserved" {
		t.Fatalf("user annotation = %q, want %q", got["user.example.com/setting"], "preserved")
	}

	if source[ManagedClusterUIDAnnotation] != "hub-only-uid" {
		t.Fatal("PolicyAnnotationsForSpoke() mutated the source annotations")
	}

	if got := PolicyAnnotationsForSpoke(map[string]string{ManagedClusterUIDAnnotation: "only"}); got != nil {
		t.Fatalf("PolicyAnnotationsForSpoke() for an internal-only map = %#v, want nil", got)
	}
}
