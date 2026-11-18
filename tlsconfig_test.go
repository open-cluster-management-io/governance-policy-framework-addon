// Copyright (c) 2026 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	testclient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	restfake "k8s.io/client-go/rest/fake"
	k8stesting "k8s.io/client-go/testing"
	sdktls "open-cluster-management.io/sdk-go/pkg/tls"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestResolveTLSConfigFlagsTakePrecedence(t *testing.T) {
	wantVersion, err := sdktls.ParseTLSVersion("VersionTLS12")
	require.NoError(t, err)

	wantCiphers, unsupported := sdktls.ParseCipherSuites("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256")
	require.Empty(t, unsupported)

	tlsOptsFunc, watchAPIServer, spec, err := resolveEffectiveTLSConfig(
		t.Context(), nil, "VersionTLS12", "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256", "X25519")

	require.NoError(t, err)
	assert.False(t, watchAPIServer, "group flag means no APIServer watcher")
	assert.Equal(t, configv1.TLSProfileSpec{}, spec)

	got := &tls.Config{}
	tlsOptsFunc(got)

	assert.Equal(t, wantVersion, got.MinVersion)
	assert.Equal(t, wantCiphers, got.CipherSuites)
	assert.Equal(t, []tls.CurveID{tls.X25519}, got.CurvePreferences)
}

func TestResolveTLSConfigAPIServerProfile(t *testing.T) {
	for _, tt := range []struct {
		name      string
		wantWatch bool
	}{
		{name: "watch allowed", wantWatch: true},
		{name: "watch denied"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, watchAPIServer, resolvedSpec, err := resolveEffectiveTLSConfig(
				t.Context(), newFakeAPIServer(t, tt.wantWatch, true), "", "", "")

			require.NoError(t, err)
			assert.Equal(t, tt.wantWatch, watchAPIServer)
			assert.Equal(t, configv1.TLSProfiles[configv1.TLSProfileModernType].Groups, resolvedSpec.Groups)
		})
	}
}

func TestResolveTLSConfigKeepsWatcherOnAccessReviewError(t *testing.T) {
	cfg := newFakeAPIServer(t, false, true)
	innerTransport := cfg.Transport
	cfg.Transport = restfake.CreateHTTPClient(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews") {
			return nil, errors.New("connection refused")
		}

		return innerTransport.RoundTrip(r)
	}).Transport

	_, watchAPIServer, _, err := resolveEffectiveTLSConfig(t.Context(), cfg, "", "", "")

	require.NoError(t, err)
	assert.True(t, watchAPIServer, "a transient access review error must not disable the watcher")
}

func TestTLSConfigReadsConfigMap(t *testing.T) {
	kubeClient := testclient.NewSimpleClientset(newTLSConfigMap(map[string]string{
		sdktls.ConfigMapKeyMinVersion: "VersionTLS13",
	}))
	allowSelfSubjectAccessReviews(kubeClient)

	configMapCfg, err := startTLSConfigMapWatcher(t.Context(), kubeClient, "addon-ns")
	require.NoError(t, err)
	require.NotNil(t, configMapCfg)

	wantVersion, err := sdktls.ParseTLSVersion("VersionTLS13")
	require.NoError(t, err)
	assert.Equal(t, wantVersion, configMapCfg.MinVersion)
}

func TestResolveTLSConfigNoFlagsNoClusterUsesDefaults(t *testing.T) {
	tlsOptsFunc, watchAPIServer, spec, err := resolveEffectiveTLSConfig(t.Context(), nil, "", "", "")

	require.NoError(t, err)
	assert.False(t, watchAPIServer)
	assert.Equal(t, configv1.TLSProfileSpec{}, spec)

	got := &tls.Config{}
	tlsOptsFunc(got)

	assert.Equal(t, sdktls.GetDefaultTLSConfig().MinVersion, got.MinVersion)
}

func TestAPIServerCacheEntryOnlyAddedWhenAPIWatched(t *testing.T) {
	tests := []struct {
		name      string
		wantWatch bool
	}{
		{name: "API not served"},
		{name: "API served and watch allowed", wantWatch: true},
	}

	wantField := fields.SelectorFromSet(fields.Set{"metadata.name": openshifttls.APIServerName})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newFakeAPIServer(t, tt.wantWatch, tt.wantWatch)

			_, watchAPIServer, _, err := resolveEffectiveTLSConfig(t.Context(), cfg, "", "", "")
			require.NoError(t, err)
			require.Equal(t, tt.wantWatch, watchAPIServer)

			cacheOptions := cache.Options{ByObject: map[client.Object]cache.ByObject{}}
			addAPIServerCacheEntry(cacheOptions, watchAPIServer)

			if !tt.wantWatch {
				_, err = manager.New(cfg, manager.Options{
					Scheme: scheme,
					Cache:  cacheOptions,
				})
				require.NoError(t, err, "manager creation must not require the APIServer mapping")

				return
			}

			require.Len(t, cacheOptions.ByObject, 1)

			for _, entry := range cacheOptions.ByObject {
				assert.Equal(t, wantField, entry.Field)
			}
		})
	}
}

func TestResolveTLSConfigInvalidInputReturnsError(t *testing.T) {
	tests := []struct {
		name       string
		minVersion string
		groups     string
		wantErrIs  error
	}{
		{name: "invalid groups", groups: "not-a-group", wantErrIs: errInvalidTLSGroups},
		{name: "invalid min version", minVersion: "not-a-version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tlsOptsFunc, watchAPIServer, spec, err := resolveEffectiveTLSConfig(
				t.Context(), nil, tt.minVersion, "", tt.groups)

			require.Error(t, err)

			if tt.wantErrIs != nil {
				require.ErrorIs(t, err, tt.wantErrIs)
			}

			assert.Nil(t, tlsOptsFunc)
			assert.False(t, watchAPIServer)
			assert.Equal(t, configv1.TLSProfileSpec{}, spec)
		})
	}
}

func TestStartTLSConfigMapWatcher(t *testing.T) {
	allowedClient := testclient.NewSimpleClientset()
	allowSelfSubjectAccessReviews(allowedClient)

	errorClient := testclient.NewSimpleClientset()
	errorClient.PrependReactor("create", "selfsubjectaccessreviews",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("review failed")
		},
	)

	deniedClient := testclient.NewSimpleClientset(newTLSConfigMap(nil))

	defaultMinVersion := sdktls.GetDefaultTLSConfig().MinVersion

	tests := []struct {
		name           string
		client         *testclient.Clientset
		wantErr        string
		wantMinVersion uint16
	}{
		{name: "no config map uses default", client: allowedClient, wantMinVersion: defaultMinVersion},
		{name: "missing perm returns error", client: deniedClient, wantErr: "missing permission"},
		{name: "permission check error", client: errorClient, wantErr: "failed to check permission to watch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configMapCfg, err := startTLSConfigMapWatcher(t.Context(), tt.client, "addon-ns")

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, configMapCfg)
			assert.Equal(t, tt.wantMinVersion, configMapCfg.MinVersion)
		})
	}
}

func TestCanWatchAPIServerTLSProfile(t *testing.T) {
	kubeClient := testclient.NewSimpleClientset()

	var reviews []*authorizationv1.SelfSubjectAccessReview

	allowSelfSubjectAccessReviews(kubeClient, func(review *authorizationv1.SelfSubjectAccessReview) {
		reviews = append(reviews, review.DeepCopy())
	})

	allowed, err := canWatchAPIServerTLSProfile(t.Context(), kubeClient)
	require.NoError(t, err)
	assert.True(t, allowed)
	require.Len(t, reviews, 2)

	for i, verb := range []string{"list", "watch"} {
		attrs := reviews[i].Spec.ResourceAttributes
		require.NotNil(t, attrs)
		assert.Equal(t, verb, attrs.Verb)
		assert.Equal(t, configv1.GroupName, attrs.Group)
		assert.Equal(t, "apiservers", attrs.Resource)
		assert.Equal(t, openshifttls.APIServerName, attrs.Name)
		assert.Empty(t, attrs.Namespace)
	}
}

func TestFetchAPIServerTLSProfileSpecErrorHandling(t *testing.T) {
	apiServerGR := schema.GroupResource{Group: configv1.GroupName, Resource: "apiservers"}

	tests := []struct {
		name          string
		getErr        error
		wantWatchable bool
	}{
		{
			name:   "forbidden means use defaults",
			getErr: apierrors.NewForbidden(apiServerGR, openshifttls.APIServerName, errors.New("forbidden")),
		},
		{
			name:   "not found means the profile is not served",
			getErr: apierrors.NewNotFound(apiServerGR, openshifttls.APIServerName),
		},
		{
			name: "no matching resource means the profile is not served",
			getErr: &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{
				Group: apiServerGR.Group, Resource: apiServerGR.Resource,
			}},
		},
		{
			name:          "transient error keeps watching",
			getErr:        errors.New("connection refused"),
			wantWatchable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiServerClient := fakeclient.NewClientBuilder().
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object,
						_ ...client.GetOption,
					) error {
						return tt.getErr
					},
				}).
				Build()

			spec, watchable := fetchAPIServerTLSProfileSpec(t.Context(), apiServerClient)

			assert.Equal(t, tt.wantWatchable, watchable)
			assert.Equal(t, configv1.TLSProfileSpec{}, spec)
		})
	}
}

func TestReadAPIServerTLSProfileSpecClientError(t *testing.T) {
	cfg := &rest.Config{
		Host:            "https://api.example.test",
		TLSClientConfig: rest.TLSClientConfig{CAData: []byte("not a certificate")},
	}

	spec, watchable := readAPIServerTLSProfileSpec(t.Context(), cfg)

	assert.Equal(t, configv1.TLSProfileSpec{}, spec)
	assert.False(t, watchable)
}

func TestAPIServerTLSWatcherStart(t *testing.T) {
	realManager, err := manager.New(newFakeAPIServer(t, false, true), manager.Options{
		Scheme: scheme,
		NewClient: func(*rest.Config, client.Options) (client.Client, error) {
			return newFakeAPIServerClient(), nil
		},
	})
	require.NoError(t, err)

	mgr := &recordingManager{Manager: realManager}

	initialSpec := *configv1.TLSProfiles[configv1.TLSProfileModernType]
	initialSpec.Ciphers = []string{"force-change"}

	require.NoError(t, mgr.Add(&apiServerTLSWatcherSetup{mgr: mgr, initialSpec: initialSpec}))
	require.Len(t, mgr.added, 1)

	setup, ok := mgr.added[0].(*apiServerTLSWatcherSetup)
	require.True(t, ok)
	assert.False(t, setup.NeedLeaderElection())

	require.NoError(t, setup.Start(t.Context()))
	require.Len(t, mgr.added, 2, "watcher controller registered with manager")

	watcherController, ok := mgr.added[1].(controller.Controller)
	require.True(t, ok)

	_, err = watcherController.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: openshifttls.APIServerName},
	})
	require.NoError(t, err, "same groups must not trigger a process exit")
}

func newTLSConfigMap(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: sdktls.ConfigMapName, Namespace: "addon-ns"},
		Data:       data,
	}
}

// allowSelfSubjectAccessReviews makes the fake clientset allow every SelfSubjectAccessReview;
// otherwise the echoed object's zero Allowed status looks like a denial.
func allowSelfSubjectAccessReviews(
	client *testclient.Clientset, onReview ...func(*authorizationv1.SelfSubjectAccessReview),
) {
	client.PrependReactor("create", "selfsubjectaccessreviews",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			//nolint:forcetypeassert
			review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
			review.Status.Allowed = true

			for _, fn := range onReview {
				fn(review)
			}

			return true, review, nil
		},
	)
}

func newFakeAPIServerClient() client.Client {
	return fakeclient.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&configv1.APIServer{
			ObjectMeta: metav1.ObjectMeta{Name: openshifttls.APIServerName},
			Spec: configv1.APIServerSpec{
				TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
			},
		}).
		Build()
}

// newFakeAPIServer serves discovery, the APIServer object when serveOpenShiftAPI is set, and access reviews.
func newFakeAPIServer(t *testing.T, allowWatch, serveOpenShiftAPI bool) *rest.Config {
	t.Helper()

	group := "apps"
	if serveOpenShiftAPI {
		group = configv1.GroupName
	}

	groupVersion := group + "/v1"
	apiGroups := fmt.Sprintf(
		`{"kind":"APIGroupList","apiVersion":"v1","groups":[`+
			`{"name":%q,"versions":[{"groupVersion":%q,"version":"v1"}],`+
			`"preferredVersion":{"groupVersion":%q,"version":"v1"}}]}`,
		group, groupVersion, groupVersion)

	return &rest.Config{
		Host: "https://api.example.test",
		Transport: restfake.CreateHTTPClient(func(r *http.Request) (*http.Response, error) {
			path := r.URL.Path

			var body string

			status := http.StatusOK

			switch {
			case path == "/api":
				body = `{"kind":"APIVersions","apiVersion":"v1","versions":["v1"]}`
			case path == "/apis":
				body = apiGroups
			case serveOpenShiftAPI && strings.HasSuffix(path, "/apiservers/"+openshifttls.APIServerName):
				body = `{"apiVersion":"config.openshift.io/v1","kind":"APIServer",` +
					`"metadata":{"name":"cluster"},"spec":{"tlsSecurityProfile":{"type":"Modern"}}}`
			case strings.HasSuffix(path, "/selfsubjectaccessreviews"):
				body = fmt.Sprintf(
					`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","status":{"allowed":%t}}`,
					allowWatch)
			case serveOpenShiftAPI && strings.HasPrefix(path, "/apis/"):
				body = `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"` +
					strings.TrimPrefix(path, "/apis/") +
					`","resources":[{"name":"apiservers","kind":"APIServer","namespaced":false}]}`
			case strings.HasPrefix(path, "/apis/"):
				status = http.StatusNotFound
				body = `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`
			default:
				return nil, errors.New("unexpected path: " + path)
			}

			return &http.Response{
				StatusCode: status,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}).Transport,
	}
}

type recordingManager struct {
	manager.Manager
	added []manager.Runnable
}

func (m *recordingManager) Add(r manager.Runnable) error {
	m.added = append(m.added, r)

	return nil
}
