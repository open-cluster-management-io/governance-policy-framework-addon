// Copyright (c) 2026 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	testclient "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	sdktls "open-cluster-management.io/sdk-go/pkg/tls"
)

// allowSelfSubjectAccessReviews makes a fake clientset report every SelfSubjectAccessReview as
// allowed. Without this, the fake client's default "create" reaction just stores and echoes back
// the submitted object, leaving Status.Allowed at its false zero value - indistinguishable from a
// real permission denial.
func allowSelfSubjectAccessReviews(client *testclient.Clientset) {
	client.PrependReactor("create", "selfsubjectaccessreviews",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			//nolint:forcetypeassert
			review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
			review.Status.Allowed = true

			return true, review, nil
		},
	)
}

func TestResolveTLSConfigFlagsTakePrecedence(t *testing.T) {
	t.Parallel()

	wantVersion, err := sdktls.ParseTLSVersion("VersionTLS13")
	if err != nil {
		t.Fatalf("failed to parse the expected TLS version: %v", err)
	}

	// A nil kubeClient would panic if resolveTLSConfig tried to use it, proving the ConfigMap
	// path is skipped when the flags are set.
	cfg, err := resolveTLSConfig(t.Context(), "VersionTLS13", "", nil, "", func() {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg == nil {
		t.Fatal("expected a non-nil TLS config")
	}

	if cfg.MinVersion != wantVersion {
		t.Errorf("got MinVersion %d, want %d", cfg.MinVersion, wantVersion)
	}
}

func TestResolveTLSConfigInvalidFlagsReturnsError(t *testing.T) {
	t.Parallel()

	_, err := resolveTLSConfig(t.Context(), "not-a-version", "", nil, "", func() {})
	if err == nil {
		t.Fatal("expected an error for an invalid TLS version flag")
	}
}

func TestResolveTLSConfigNoFlagsNoClusterFallsBackToNil(t *testing.T) {
	t.Parallel()

	cfg, err := resolveTLSConfig(t.Context(), "", "", nil, "", func() {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg != nil {
		t.Errorf("expected a nil config so the caller falls back to defaults, got %+v", cfg)
	}
}

func TestResolveTLSConfigReadsConfigMapWhenNoFlags(t *testing.T) {
	t.Parallel()

	client := testclient.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sdktls.ConfigMapName,
			Namespace: "addon-ns",
		},
		Data: map[string]string{
			sdktls.ConfigMapKeyMinVersion: "VersionTLS13",
		},
	})
	allowSelfSubjectAccessReviews(client)

	cfg, err := resolveTLSConfig(t.Context(), "", "", client, "addon-ns", func() {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg == nil {
		t.Fatal("expected a non-nil TLS config")
	}

	wantVersion, err := sdktls.ParseTLSVersion("VersionTLS13")
	if err != nil {
		t.Fatalf("failed to parse the expected TLS version: %v", err)
	}

	if cfg.MinVersion != wantVersion {
		t.Errorf("got MinVersion %d, want %d", cfg.MinVersion, wantVersion)
	}
}

func TestResolveTLSConfigDefaultsWhenConfigMapAbsent(t *testing.T) {
	t.Parallel()

	client := testclient.NewSimpleClientset()
	allowSelfSubjectAccessReviews(client)

	cfg, err := resolveTLSConfig(t.Context(), "", "", client, "addon-ns", func() {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg == nil {
		t.Fatal("expected a non-nil TLS config seeded with Go's TLS 1.2 defaults")
	}

	wantDefault := sdktls.GetDefaultTLSConfig()
	if cfg.MinVersion != wantDefault.MinVersion {
		t.Errorf("got MinVersion %d, want %d", cfg.MinVersion, wantDefault.MinVersion)
	}
}

func TestResolveTLSConfigMissingConfigMapPermissionReturnsError(t *testing.T) {
	t.Parallel()

	// No allowSelfSubjectAccessReviews call: the fake client's default reaction leaves
	// Status.Allowed at its false zero value, simulating denied RBAC.
	client := testclient.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sdktls.ConfigMapName,
			Namespace: "addon-ns",
		},
	})

	_, err := resolveTLSConfig(t.Context(), "", "", client, "addon-ns", func() {})
	if err == nil {
		t.Fatal("expected an error when list/watch permission on the ConfigMap is denied")
	}
}
