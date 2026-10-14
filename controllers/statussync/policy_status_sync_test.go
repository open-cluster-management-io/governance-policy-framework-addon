// Copyright Contributors to the Open Cluster Management project
package statussync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	depclient "github.com/stolostron/kubernetes-dependency-watches/client"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

func TestUpdateStatusesConditionallyFencesHubPolicyStatusWrites(t *testing.T) {
	tests := []struct {
		name               string
		configuredUID      string
		hubPolicyUID       string
		wantHubStatusWrite bool
	}{
		{
			name:               "matching nonempty UIDs allow the write",
			configuredUID:      "managed-cluster-uid",
			hubPolicyUID:       "managed-cluster-uid",
			wantHubStatusWrite: true,
		},
		{
			name:          "mismatched UIDs fence the write",
			configuredUID: "old-managed-cluster-uid",
			hubPolicyUID:  "new-managed-cluster-uid",
		},
		{
			name:               "unset configured UID skips the comparison",
			hubPolicyUID:       "some-managed-cluster-uid",
			wantHubStatusWrite: true,
		},
		{
			name:          "missing hub Policy UID fences the write",
			configuredUID: "managed-cluster-uid",
		},
		{
			name:               "unset configured UID skips the comparison when the marker is missing",
			wantHubStatusWrite: true,
		},
	}

	beforeSkipped := counterValue(t, managedClusterUIDFenceSkips)
	var wantSkipped float64

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			policyScheme := runtime.NewScheme()

			if err := policiesv1.AddToScheme(policyScheme); err != nil {
				t.Fatalf("failed to add Policy API to scheme: %v", err)
			}

			cachedHubPolicy := statusTestPolicy(tt.hubPolicyUID, "42")
			managedClient := fake.NewClientBuilder().WithScheme(policyScheme).Build()
			hubClient := &recordingStatusClient{Client: fake.NewClientBuilder().
				WithScheme(policyScheme).
				WithStatusSubresource(&policiesv1.Policy{}).
				WithObjects(cachedHubPolicy).
				Build()}

			reconciler := &PolicyReconciler{
				HubClient:             hubClient,
				ManagedClusterUID:     tt.configuredUID,
				ManagedClient:         managedClient,
				HubRecorder:           events.NewFakeRecorder(10),
				ClusterNamespaceOnHub: "cluster-ns",
			}

			instance := &policiesv1.Policy{}
			instance.Name = "root.policy"
			instance.Namespace = "spoke-ns"
			oldStatus := policiesv1.PolicyStatus{ComplianceState: policiesv1.Compliant}
			err := reconciler.updateStatuses(ctx, instance, cachedHubPolicy.DeepCopy(), oldStatus)

			if tt.wantHubStatusWrite {
				if err != nil {
					t.Fatalf("updateStatuses() error = %v", err)
				}
			} else if !errors.Is(err, errManagedClusterUIDFenceMismatch) {
				t.Fatalf("updateStatuses() error = %v, want UID fence mismatch", err)
			}

			gotHubStatusWrite := len(hubClient.statusUpdates) == 1
			if gotHubStatusWrite != tt.wantHubStatusWrite {
				t.Fatalf("hub status writes = %v, want %v", gotHubStatusWrite, tt.wantHubStatusWrite)
			}

			if tt.wantHubStatusWrite {
				if got := hubClient.statusUpdates[0].GetResourceVersion(); got != "42" {
					t.Errorf("status update resourceVersion = %q, want cached version %q", got, "42")
				}

				got := hubClient.statusUpdates[0].GetAnnotations()[utils.ManagedClusterUIDAnnotation]
				if got != tt.hubPolicyUID {
					t.Errorf("status update UID marker = %q, want cached marker %q", got, tt.hubPolicyUID)
				}
			} else {
				wantSkipped++
			}
		})
	}

	if got := counterValue(t, managedClusterUIDFenceSkips) - beforeSkipped; got != wantSkipped {
		t.Errorf("fenced hub status write count = %v, want %v", got, wantSkipped)
	}
}

func TestUpdateStatusesUsesRefreshedHubStatusBeforeWriting(t *testing.T) {
	ctx := context.Background()
	policyScheme := runtime.NewScheme()

	if err := policiesv1.AddToScheme(policyScheme); err != nil {
		t.Fatalf("failed to add Policy API to scheme: %v", err)
	}

	initialHubPolicy := statusTestPolicy("managed-cluster-uid", "41")
	refreshedHubPolicy := statusTestPolicy("managed-cluster-uid", "42")
	refreshedHubPolicy.Status.ComplianceState = policiesv1.Compliant
	hubClient := &recordingStatusClient{
		Client: fake.NewClientBuilder().
			WithScheme(policyScheme).
			WithStatusSubresource(&policiesv1.Policy{}).
			WithObjects(refreshedHubPolicy).
			Build(),
		cachedReadPolicy: refreshedHubPolicy,
	}
	reconciler := &PolicyReconciler{
		HubClient:             hubClient,
		ManagedClusterUID:     "managed-cluster-uid",
		ManagedClient:         fake.NewClientBuilder().WithScheme(policyScheme).Build(),
		ClusterNamespaceOnHub: "cluster-ns",
	}
	instance := &policiesv1.Policy{}
	instance.Name = "root.policy"
	instance.Namespace = "spoke-ns"
	instance.Status.ComplianceState = policiesv1.Compliant

	err := reconciler.updateStatuses(ctx, instance, initialHubPolicy,
		policiesv1.PolicyStatus{ComplianceState: policiesv1.Compliant})
	if err != nil {
		t.Fatalf("updateStatuses() error = %v", err)
	}

	if len(hubClient.statusUpdates) != 0 {
		t.Fatalf("hub status writes = %d, want none because the Policy already matches", len(hubClient.statusUpdates))
	}
}

func counterValue(t *testing.T, counter prometheus.Counter) float64 {
	t.Helper()

	metric := &dto.Metric{}
	if err := counter.Write(metric); err != nil {
		t.Fatalf("failed to read counter value: %v", err)
	}

	return metric.GetCounter().GetValue()
}

func TestUpdateStatusesConflictsWhenCachedUIDIsStale(t *testing.T) {
	ctx := context.Background()
	policyScheme := runtime.NewScheme()

	if err := policiesv1.AddToScheme(policyScheme); err != nil {
		t.Fatalf("failed to add Policy API to scheme: %v", err)
	}

	cachedHubPolicy := statusTestPolicy("old-managed-cluster-uid", "41")
	currentHubPolicy := statusTestPolicy("new-managed-cluster-uid", "42")
	hubClient := &recordingStatusClient{
		Client: fake.NewClientBuilder().
			WithScheme(policyScheme).
			WithStatusSubresource(&policiesv1.Policy{}).
			WithObjects(currentHubPolicy).
			Build(),
		cachedReadPolicy: cachedHubPolicy,
	}
	reconciler := &PolicyReconciler{
		HubClient:             hubClient,
		ManagedClusterUID:     "old-managed-cluster-uid",
		ManagedClient:         fake.NewClientBuilder().WithScheme(policyScheme).Build(),
		ClusterNamespaceOnHub: "cluster-ns",
	}
	instance := &policiesv1.Policy{}
	instance.Name = "root.policy"
	instance.Namespace = "spoke-ns"
	instance.Status.ComplianceState = policiesv1.Compliant

	err := reconciler.updateStatuses(ctx, instance, cachedHubPolicy,
		policiesv1.PolicyStatus{ComplianceState: policiesv1.Compliant})
	if !apierrors.IsConflict(err) {
		t.Fatalf("updateStatuses() error = %v, want a resource version conflict", err)
	}

	if len(hubClient.statusUpdates) != 1 {
		t.Fatalf("status update attempts = %d, want one using the stale cached object", len(hubClient.statusUpdates))
	}

	if got := hubClient.statusUpdates[0].GetResourceVersion(); got != "41" {
		t.Fatalf("status update resourceVersion = %q, want stale cached version %q", got, "41")
	}

	got := hubClient.statusUpdates[0].GetAnnotations()[utils.ManagedClusterUIDAnnotation]
	if got != "old-managed-cluster-uid" {
		t.Fatalf("status update UID marker = %q, want stale cached marker", got)
	}

	serverPolicy := &policiesv1.Policy{}

	err = hubClient.Client.Get(ctx, types.NamespacedName{Namespace: "cluster-ns", Name: "root.policy"}, serverPolicy)
	if err != nil {
		t.Fatalf("failed to get server Policy after conflict: %v", err)
	}

	if got := serverPolicy.GetAnnotations()[utils.ManagedClusterUIDAnnotation]; got != "new-managed-cluster-uid" {
		t.Fatalf("server UID marker = %q, want current marker to remain unchanged", got)
	}

	if got := serverPolicy.Status.ComplianceState; got != policiesv1.NonCompliant {
		t.Fatalf("server compliance = %q, want stale status write to be rejected", got)
	}
}

func TestUpdateStatusesKeepsLocalStatusUpdatedWhenHubWriteIsFenced(t *testing.T) {
	ctx := context.Background()
	policyScheme := runtime.NewScheme()

	if err := policiesv1.AddToScheme(policyScheme); err != nil {
		t.Fatalf("failed to add Policy API to scheme: %v", err)
	}

	managedPolicy := &policiesv1.Policy{}
	managedPolicy.Name = "root.policy"
	managedPolicy.Namespace = "spoke-ns"
	managedPolicy.Status.ComplianceState = policiesv1.NonCompliant
	managedClient := fake.NewClientBuilder().
		WithScheme(policyScheme).
		WithStatusSubresource(&policiesv1.Policy{}).
		WithObjects(managedPolicy).
		Build()
	cachedHubPolicy := statusTestPolicy("new-managed-cluster-uid", "42")
	hubClient := &recordingStatusClient{Client: fake.NewClientBuilder().
		WithScheme(policyScheme).
		WithStatusSubresource(&policiesv1.Policy{}).
		WithObjects(cachedHubPolicy).
		Build()}
	reconciler := &PolicyReconciler{
		HubClient:             hubClient,
		ManagedClusterUID:     "old-managed-cluster-uid",
		ManagedClient:         managedClient,
		ManagedRecorder:       events.NewFakeRecorder(10),
		ClusterNamespaceOnHub: "cluster-ns",
	}
	instance := managedPolicy.DeepCopy()
	oldStatus := policiesv1.PolicyStatus{ComplianceState: policiesv1.NonCompliant}

	if err := reconciler.updateStatuses(ctx, instance, cachedHubPolicy.DeepCopy(), oldStatus); !errors.Is(
		err, errManagedClusterUIDFenceMismatch,
	) {
		t.Fatalf("updateStatuses() error = %v, want UID fence mismatch", err)
	}

	if len(hubClient.statusUpdates) != 0 {
		t.Fatalf("hub status writes = %d, want 0 for a stale configured UID", len(hubClient.statusUpdates))
	}

	updatedManagedPolicy := &policiesv1.Policy{}
	if err := managedClient.Get(ctx, types.NamespacedName{Namespace: "spoke-ns", Name: "root.policy"},
		updatedManagedPolicy); err != nil {
		t.Fatalf("failed to get locally updated Policy: %v", err)
	}

	if updatedManagedPolicy.Status.ComplianceState != policiesv1.Compliant {
		t.Fatalf("local compliance = %q, want %q", updatedManagedPolicy.Status.ComplianceState, policiesv1.Compliant)
	}
}

func TestReconcileRetriesAfterHubStatusWriteIsFenced(t *testing.T) {
	ctx := context.Background()
	policyScheme := runtime.NewScheme()

	if err := policiesv1.AddToScheme(policyScheme); err != nil {
		t.Fatalf("failed to add Policy API to scheme: %v", err)
	}

	if err := corev1.AddToScheme(policyScheme); err != nil {
		t.Fatalf("failed to add core API to scheme: %v", err)
	}

	managedPolicy := &policiesv1.Policy{}
	managedPolicy.Name = "root.policy"
	managedPolicy.Namespace = "spoke-ns"
	managedPolicy.Status.ComplianceState = policiesv1.NonCompliant
	hubPolicy := statusTestPolicy("new-managed-cluster-uid", "42")
	hubPolicy.Status.ComplianceState = policiesv1.NonCompliant

	managedClient := fake.NewClientBuilder().
		WithScheme(policyScheme).
		WithStatusSubresource(&policiesv1.Policy{}).
		WithObjects(managedPolicy).
		Build()
	hubClient := &recordingStatusClient{Client: fake.NewClientBuilder().
		WithScheme(policyScheme).
		WithStatusSubresource(&policiesv1.Policy{}).
		WithObjects(hubPolicy).
		Build()}
	reconciler := &PolicyReconciler{
		HubClient:             hubClient,
		ManagedClusterUID:     "old-managed-cluster-uid",
		ManagedClient:         managedClient,
		DynamicWatcher:        idleDynamicWatcher{},
		HubRecorder:           events.NewFakeRecorder(10),
		ManagedRecorder:       events.NewFakeRecorder(10),
		ClusterNamespaceOnHub: "cluster-ns",
	}

	result, err := reconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "spoke-ns", Name: "root.policy"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if result.RequeueAfter != managedClusterUIDFenceRetryPeriod {
		t.Fatalf("Reconcile() RequeueAfter = %v, want %v", result.RequeueAfter, managedClusterUIDFenceRetryPeriod)
	}

	if len(hubClient.statusUpdates) != 0 {
		t.Fatalf("hub status writes = %d, want 0 for a stale configured UID", len(hubClient.statusUpdates))
	}
}

func statusTestPolicy(uid, resourceVersion string) *policiesv1.Policy {
	policy := &policiesv1.Policy{}
	policy.Name = "root.policy"
	policy.Namespace = "cluster-ns"
	policy.ResourceVersion = resourceVersion
	policy.Status.ComplianceState = policiesv1.NonCompliant

	if uid != "" {
		policy.SetAnnotations(map[string]string{utils.ManagedClusterUIDAnnotation: uid})
	}

	return policy
}

type idleDynamicWatcher struct {
	depclient.DynamicWatcher
}

func (idleDynamicWatcher) StartQueryBatch(depclient.ObjectIdentifier) error { return nil }

func (idleDynamicWatcher) EndQueryBatch(depclient.ObjectIdentifier) error { return nil }

type recordingStatusClient struct {
	client.Client
	cachedReadPolicy *policiesv1.Policy
	statusUpdates    []*policiesv1.Policy
}

func (c *recordingStatusClient) Get(
	ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption,
) error {
	if policy, ok := obj.(*policiesv1.Policy); c.cachedReadPolicy != nil && ok &&
		key.Namespace == c.cachedReadPolicy.Namespace && key.Name == c.cachedReadPolicy.Name {
		c.cachedReadPolicy.DeepCopyInto(policy)

		return nil
	}

	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *recordingStatusClient) Status() client.SubResourceWriter {
	return &recordingStatusWriter{SubResourceWriter: c.Client.Status(), client: c}
}

type recordingStatusWriter struct {
	client.SubResourceWriter
	client *recordingStatusClient
}

func (w *recordingStatusWriter) Update(
	ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption,
) error {
	if policy, ok := obj.(*policiesv1.Policy); ok {
		w.client.statusUpdates = append(w.client.statusUpdates, policy.DeepCopy())
	}

	return w.SubResourceWriter.Update(ctx, obj, opts...)
}
