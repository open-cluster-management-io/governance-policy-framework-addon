// Copyright Contributors to the Open Cluster Management project
package statussync

import (
	"testing"
	"time"

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

	ctrl := true
	polRef := func(name string) metav1.OwnerReference {
		return metav1.OwnerReference{
			APIVersion: policiesv1.GroupVersion.String(),
			Kind:       policiesv1.Kind,
			Name:       name,
			Controller: &ctrl,
		}
	}
	buildTmpl := func(owner, parentLabel string, otherOwners ...string) *unstructured.Unstructured {
		tmpl := &unstructured.Unstructured{}

		var refs []metav1.OwnerReference
		if owner != "" {
			refs = append(refs, polRef(owner))
		}

		for _, name := range otherOwners {
			refs = append(refs, polRef(name))
		}

		if len(refs) > 0 {
			tmpl.SetOwnerReferences(refs)
		}

		if parentLabel != "" {
			tmpl.SetLabels(map[string]string{utils.ParentPolicyLabel: parentLabel})
		}

		return tmpl
	}

	tests := []struct {
		name   string
		policy string
		tmpl   *unstructured.Unstructured
		want   bool
	}{
		{
			name:   "Nil template returns false",
			policy: "policy-a",
			tmpl:   nil,
			want:   false,
		},
		{
			name:   "Empty template with no ownership metadata returns false",
			policy: "policy-a",
			tmpl:   &unstructured.Unstructured{},
			want:   false,
		},
		{
			name:   "Policy owner reference matches policy",
			policy: "policy-a",
			tmpl:   buildTmpl("policy-a", ""),
			want:   true,
		},
		{
			name:   "Policy owner reference does not match policy",
			policy: "policy-b",
			tmpl:   buildTmpl("policy-a", ""),
			want:   false,
		},
		{
			name:   "Parent policy label matches when owner reference is absent",
			policy: "policy-a",
			tmpl:   buildTmpl("", "policy-a"),
			want:   true,
		},
		{
			name:   "Parent policy label does not match when owner reference is absent",
			policy: "policy-b",
			tmpl:   buildTmpl("", "policy-a"),
			want:   false,
		},
		{
			name:   "Owner reference takes precedence over parent policy label",
			policy: "policy-b",
			tmpl:   buildTmpl("policy-a", "policy-b"),
			want:   false,
		},
		{
			name:   "First policy owner reference is used when multiple are present",
			policy: "policy-a",
			tmpl:   buildTmpl("policy-a", "", "policy-b"),
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := templateOwnedByPolicy(tt.tmpl, tt.policy); got != tt.want {
				t.Errorf("templateOwnedByPolicy() = %v, want %v", got, tt.want)
			}
		})
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

	makeTestTmpl := func() *unstructured.Unstructured {
		tmpl := &unstructured.Unstructured{}
		tmpl.SetGroupVersionKind(tmplGVK)
		tmpl.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: "policy.open-cluster-management.io/v1",
			Kind:       policiesv1.Kind,
			Name:       "policy-a",
			Controller: &controller,
		}})
		tmpl.SetLabels(map[string]string{utils.ParentPolicyLabel: "policy-a"})

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
			name:       "returns history from template status",
			template:   makeTestTmpl(),
			policyName: "policy-a",
			wantLen:    1,
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
