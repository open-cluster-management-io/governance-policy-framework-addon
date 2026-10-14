package specsync

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"open-cluster-management.io/governance-policy-framework-addon/controllers/utils"
)

func TestReconcileRemovesManagedClusterUIDFromSpokePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		managedPolicy *policiesv1.Policy
	}{
		{
			name: "new spoke Policy",
		},
		{
			name: "existing spoke Policy with a stale hub-only annotation",
			managedPolicy: testPolicy("spoke", map[string]string{
				utils.ManagedClusterUIDAnnotation: "stale-uid",
				"user.example.com/setting":        "value",
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			policyScheme := runtime.NewScheme()

			if err := policiesv1.AddToScheme(policyScheme); err != nil {
				t.Fatalf("failed to add Policy API to scheme: %v", err)
			}

			hubPolicy := testPolicy("hub", map[string]string{
				utils.ManagedClusterUIDAnnotation: "current-uid",
				"user.example.com/setting":        "value",
			})
			hubPolicy.Name = "root.policy"
			hubPolicy.Namespace = "cluster-ns"
			managedClientBuilder := fake.NewClientBuilder().WithScheme(policyScheme)

			if tt.managedPolicy != nil {
				managedPolicy := tt.managedPolicy.DeepCopy()
				managedPolicy.Name = hubPolicy.Name
				managedPolicy.Namespace = "spoke-ns"
				managedClientBuilder = managedClientBuilder.WithObjects(managedPolicy)
			}

			managedClient := managedClientBuilder.Build()
			hubClient := fake.NewClientBuilder().WithScheme(policyScheme).WithObjects(hubPolicy).Build()

			reconciler := &PolicyReconciler{
				HubClient:       hubClient,
				ManagedClient:   managedClient,
				ManagedRecorder: events.NewFakeRecorder(10),
				TargetNamespace: "spoke-ns",
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: hubPolicy.Namespace, Name: hubPolicy.Name},
			})
			if err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}

			spokePolicy := &policiesv1.Policy{}
			if err := managedClient.Get(ctx, types.NamespacedName{Namespace: "spoke-ns", Name: hubPolicy.Name},
				spokePolicy); err != nil {
				t.Fatalf("failed to get spoke Policy after reconcile: %v", err)
			}

			if _, found := spokePolicy.GetAnnotations()[utils.ManagedClusterUIDAnnotation]; found {
				t.Fatal("spec-sync copied the hub-only ManagedCluster UID annotation to the spoke")
			}

			if got := spokePolicy.GetAnnotations()["user.example.com/setting"]; got != "value" {
				t.Fatalf("user annotation = %q, want %q", got, "value")
			}
		})
	}
}

func TestReconcileDoesNotRequestStatusSyncForUnchangedMarker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	policyScheme := runtime.NewScheme()

	if err := policiesv1.AddToScheme(policyScheme); err != nil {
		t.Fatalf("failed to add Policy API to scheme: %v", err)
	}

	hubPolicy := testPolicy("hub", map[string]string{
		utils.ManagedClusterUIDAnnotation: "current-uid",
		"user.example.com/setting":        "value",
	})
	hubPolicy.Name = "root.policy"
	hubPolicy.Namespace = "cluster-ns"

	spokePolicy := hubPolicy.DeepCopy()
	spokePolicy.Namespace = "spoke-ns"
	spokePolicy.SetAnnotations(map[string]string{"user.example.com/setting": "value"})

	managedClient := fake.NewClientBuilder().WithScheme(policyScheme).WithObjects(spokePolicy).Build()
	hubClient := fake.NewClientBuilder().WithScheme(policyScheme).WithObjects(hubPolicy).Build()
	statusSyncRequests := make(chan event.GenericEvent, 1)

	reconciler := &PolicyReconciler{
		HubClient:          hubClient,
		ManagedClient:      managedClient,
		ManagedRecorder:    events.NewFakeRecorder(10),
		TargetNamespace:    "spoke-ns",
		StatusSyncRequests: statusSyncRequests,
	}

	_, err := reconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: hubPolicy.Namespace, Name: hubPolicy.Name},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	select {
	case <-statusSyncRequests:
		t.Fatal("Reconcile() unexpectedly enqueued status-sync when Policy statuses already match")
	default:
	}
}

func testPolicy(namespace string, annotations map[string]string) *policiesv1.Policy {
	policy := &policiesv1.Policy{}
	policy.Namespace = namespace
	policy.SetAnnotations(annotations)

	return policy
}
