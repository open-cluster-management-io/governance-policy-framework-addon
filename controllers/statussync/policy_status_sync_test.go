// Copyright Contributors to the Open Cluster Management project
package statussync

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

func TestParseTimestampFromEventName(t *testing.T) {
	output, err := parseTimestampFromEventName("event.17b80d88a995e12c")
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	} else if output.UnixNano() != 1709130939198988588 {
		t.Errorf("Expected 1709130939198988588 but got: %d", output.UnixNano())
	}

	output, err = parseTimestampFromEventName("event.with-no-timestamp")
	if err == nil {
		t.Errorf("Expected an error but got none: %s", output)
	}
}

func TestTemplateOwnedByPolicy(t *testing.T) {
	t.Parallel()

	controller := true

	tests := []struct {
		name        string
		ownerRefs   []metav1.OwnerReference
		parentLabel string
		policyName  string
		want        bool
	}{
		{
			name: "Empty args return false",
			want: false,
		},
		{
			name: "Policy owner reference matches policy",
			ownerRefs: []metav1.OwnerReference{{
				APIVersion: "policy.open-cluster-management.io/v1",
				Kind:       policiesv1.Kind,
				Name:       "policy-a",
				Controller: &controller,
			}},
			policyName: "policy-a",
			want:       true,
		},
		{
			name: "Policy owner reference does not match policy",
			ownerRefs: []metav1.OwnerReference{{
				APIVersion: "policy.open-cluster-management.io/v1",
				Kind:       policiesv1.Kind,
				Name:       "policy-a",
				Controller: &controller,
			}},
			policyName: "policy-b",
			want:       false,
		},
		{
			name:        "Parent policy label matches when owner reference is absent",
			parentLabel: "policy-a",
			policyName:  "policy-a",
			want:        true,
		},
		{
			name:        "Parent policy label does not match when owner reference is absent",
			parentLabel: "policy-a",
			policyName:  "policy-b",
			want:        false,
		},
		{
			name:       "No owner reference or parent policy label",
			policyName: "policy-a",
			want:       false,
		},
		{
			name: "Owner reference takes precedence over parent policy label",
			ownerRefs: []metav1.OwnerReference{{
				APIVersion: "policy.open-cluster-management.io/v1",
				Kind:       policiesv1.Kind,
				Name:       "policy-a",
				Controller: &controller,
			}},
			parentLabel: "policy-b",
			policyName:  "policy-b",
			want:        false,
		},
		{
			name: "Policy owner reference selected over other owner references",
			ownerRefs: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					Name:       "other-owner",
					Controller: &controller,
				},
				{
					APIVersion: "policy.open-cluster-management.io/v1",
					Kind:       policiesv1.Kind,
					Name:       "policy-a",
					Controller: &controller,
				},
			},
			policyName: "policy-a",
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpl := &unstructured.Unstructured{}
			if len(tt.ownerRefs) > 0 {
				tmpl.SetOwnerReferences(tt.ownerRefs)
			}

			if tt.parentLabel != "" {
				tmpl.SetLabels(map[string]string{utils.ParentPolicyLabel: tt.parentLabel})
			}

			if got := templateOwnedByPolicy(tmpl, tt.policyName); got != tt.want {
				t.Errorf("templateOwnedByPolicy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeDetailsSuppressesStaleHistoryOnDuplicateTemplateConflict(t *testing.T) {
	t.Parallel()

	duplicateErrMsg := "NonCompliant; template-error; Template name must be unique. Policy template with " +
		"kind: ConfigurationPolicy name: config-policy already exists in policy policy-a"
	duplicateEvent := policiesv1.ComplianceHistory{
		EventName:     "policy-b.config-policy.18ad64b95843ed60",
		LastTimestamp: metav1.NewTime(time.Date(2026, 5, 27, 14, 58, 59, 0, time.UTC)),
		Message:       duplicateErrMsg,
	}

	// nil existingDPTs matches getDetails when the cluster template is not owned by the policy.
	details := mergeDetails(
		[]policiesv1.ComplianceHistory{duplicateEvent},
		nil,
		"config-policy",
		logr.Discard(),
	)

	if len(details.History) != 1 {
		t.Fatalf("mergeDetails() history len = %d, want 1", len(details.History))
	}

	if details.History[0].Message != duplicateErrMsg {
		t.Errorf("mergeDetails() kept message = %q, want duplicate error", details.History[0].Message)
	}
}

func TestMergeDetailsPreservesHistoryWithoutDuplicateTemplateConflict(t *testing.T) {
	t.Parallel()

	existingEvent := policiesv1.ComplianceHistory{
		EventName:     "policy-a.18ad64b95843ed60",
		LastTimestamp: metav1.NewTime(time.Date(2026, 5, 7, 21, 9, 35, 0, time.UTC)),
		Message:       "Compliant; prior status",
	}
	existing := []*policiesv1.DetailsPerTemplate{{
		TemplateMeta: metav1.ObjectMeta{Name: "config-policy"},
		History:      []policiesv1.ComplianceHistory{existingEvent},
	}}

	details := mergeDetails(nil, existing, "config-policy", logr.Discard())

	if len(details.History) != 1 {
		t.Fatalf("mergeDetails() history len = %d, want 1", len(details.History))
	}

	if details.History[0].Message != existingEvent.Message {
		t.Errorf("mergeDetails() message = %q, want %q", details.History[0].Message, existingEvent.Message)
	}
}

func TestGetEventsInTemplate(t *testing.T) {
	t.Parallel()

	controller := true
	tmplGVK := schema.GroupVersionKind{
		Group:   "policy.open-cluster-management.io",
		Version: "v1",
		Kind:    "ConfigurationPolicy",
	}
	historyTime := metav1.NewTime(time.Date(2024, 12, 23, 17, 1, 23, 456789000, time.UTC))
	historyMsg := "Compliant; history message for policy-a"

	makeTemplate := func(ownerPolicy string) *unstructured.Unstructured {
		tmpl := &unstructured.Unstructured{}
		tmpl.SetGroupVersionKind(tmplGVK)
		tmpl.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: "policy.open-cluster-management.io/v1",
			Kind:       policiesv1.Kind,
			Name:       ownerPolicy,
			Controller: &controller,
		}})
		tmpl.SetLabels(map[string]string{utils.ParentPolicyLabel: ownerPolicy})

		err := unstructured.SetNestedSlice(tmpl.Object, []any{
			map[string]any{
				"lastTimestamp": historyTime.Format(time.RFC3339Nano),
				"message":       historyMsg,
			},
		}, "status", "history")
		if err != nil {
			t.Fatalf("failed to set template history: %v", err)
		}

		return tmpl
	}

	tests := []struct {
		name       string
		template   *unstructured.Unstructured
		policyName string
		wantLen    int
	}{
		{
			name:       "returns history for owned template",
			template:   makeTemplate("policy-a"),
			policyName: "policy-a",
			wantLen:    1,
		},
		{
			name:       "skips history for template owned by another policy",
			template:   makeTemplate("policy-a"),
			policyName: "policy-b",
			wantLen:    0,
		},
		{
			name:       "returns empty list when template is missing",
			policyName: "policy-a",
			wantLen:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			events := getEventsInTemplate(tt.template, tt.policyName)

			if len(events) != tt.wantLen {
				t.Fatalf("getEventsInTemplate() len = %d, want %d", len(events), tt.wantLen)
			}

			if tt.wantLen == 1 && events[0].Message != historyMsg {
				t.Errorf("getEventsInTemplate() message = %q, want %q", events[0].Message, historyMsg)
			}
		})
	}
}
