// Copyright (c) 2026 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	sdktls "open-cluster-management.io/sdk-go/pkg/tls"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"open-cluster-management.io/governance-policy-framework-addon/tool"
)

// errInvalidTLSGroups marks an invalid --tls-groups value so the caller exits instead of using defaults.
var errInvalidTLSGroups = errors.New("invalid --tls-groups value")

// resolveEffectiveTLSConfig merges settings by precedence: flags, the ocm-tls-profile ConfigMap,
// the APIServer TLS profile (groups only), then Go's TLS defaults.
func resolveEffectiveTLSConfig(
	ctx context.Context, cfg *rest.Config, minVersion, cipherSuites, groups string,
) (tlsOptsFunc func(*tls.Config), watchAPIServer bool, resolvedSpec configv1.TLSProfileSpec, err error) {
	flagCfg, err := sdktls.ConfigFromFlags(minVersion, cipherSuites)
	if err != nil {
		return nil, false, configv1.TLSProfileSpec{}, err
	}

	var configMapTLS *sdktls.TLSConfig

	if strings.TrimSpace(minVersion) == "" || strings.TrimSpace(cipherSuites) == "" {
		configMapTLS, err = resolveOCMTLSConfigMap(ctx, cfg)
		if err != nil {
			log.Error(err, "Failed to resolve the ocm-tls-profile ConfigMap; using defaults")
		}
	}

	tlsSource := sdktls.GetDefaultTLSConfig()
	if configMapTLS != nil {
		tlsSource = configMapTLS
	}

	if flagCfg != nil {
		if strings.TrimSpace(minVersion) != "" {
			tlsSource.MinVersion = flagCfg.MinVersion
		}

		if strings.TrimSpace(cipherSuites) != "" {
			tlsSource.CipherSuites = flagCfg.CipherSuites
		}
	}

	curves, unsupportedGroups := libgocrypto.TLSGroupsToCurveIDs(splitTLSGroups(groups))

	if len(unsupportedGroups) > 0 {
		return nil, false, configv1.TLSProfileSpec{}, fmt.Errorf("%w: %v", errInvalidTLSGroups, unsupportedGroups)
	}

	if len(curves) == 0 {
		resolvedSpec, watchAPIServer = readAPIServerTLSProfileSpec(ctx, cfg)
		curves, unsupportedGroups = libgocrypto.TLSGroupsToCurveIDs(resolvedSpec.Groups)

		if len(unsupportedGroups) > 0 {
			log.Info("Some TLS groups are not supported and will be ignored", "groups", unsupportedGroups)
		}

		if watchAPIServer {
			allowed, permErr := canWatchAPIServerTLSProfile(ctx, kubernetes.NewForConfigOrDie(cfg))
			if permErr != nil {
				log.Error(permErr, "Failed to check permission to watch the APIServer TLS Profile; "+
					"will keep watching in case this was transient")
			} else if !allowed {
				log.Info("Access to the APIServer TLS Profile list/watch is forbidden; not watching")

				watchAPIServer = false
			}
		}
	}

	applySourceTLS := sdktls.ConfigToFunc(tlsSource)

	tlsOptsFunc = func(tlsConf *tls.Config) {
		applySourceTLS(tlsConf)

		if len(curves) > 0 {
			tlsConf.CurvePreferences = curves
		}
	}

	log.Info("Resolved TLS configuration",
		"minVersion", sdktls.VersionToString(tlsSource.MinVersion),
		"cipherSuites", sdktls.CipherSuitesToString(tlsSource.CipherSuites),
		"tlsGroups", fmt.Sprint(curves))

	return tlsOptsFunc, watchAPIServer, resolvedSpec, nil
}

// addAPIServerCacheEntry adds the singleton APIServer profile, scoped by name for resourceName
// RBAC, but only when watchable: clusters without config.openshift.io/v1 cannot resolve its REST
// mapping, which would fail manager creation.
func addAPIServerCacheEntry(options cache.Options, watchAPIServer bool) {
	if watchAPIServer {
		options.ByObject[&configv1.APIServer{}] = cache.ByObject{
			Field: fields.SelectorFromSet(fields.Set{"metadata.name": openshifttls.APIServerName}),
		}
	}
}

func resolveOCMTLSConfigMap(ctx context.Context, cfg *rest.Config) (*sdktls.TLSConfig, error) {
	if cfg == nil {
		return nil, nil
	}

	namespace, err := tool.GetOperatorNamespace()
	if err != nil {
		if errors.Is(err, tool.ErrNoNamespace) || errors.Is(err, tool.ErrRunLocal) {
			log.Info("Not running in a cluster; skipping the ocm-tls-profile ConfigMap watch")
		} else {
			log.Error(err, "Failed to get operator namespace; skipping the ocm-tls-profile ConfigMap watch")
		}

		return nil, nil
	}

	return startTLSConfigMapWatcher(ctx, kubernetes.NewForConfigOrDie(cfg), namespace)
}

// The ConfigMap watcher below needs get/list/watch on the ocm-tls-profile ConfigMap in the
// addon's own namespace. sdktls.StartTLSConfigMapWatcher lists/watches with a metadata.name field
// selector matching ConfigMapName, which is what lets list/watch be scoped by resourceNames here
// (see the secretsync package's identical pattern for policy-encryption-key).
//+kubebuilder:rbac:groups=core,resources=configmaps,resourceNames=ocm-tls-profile,verbs=get;list;watch

// startTLSConfigMapWatcher returns the ConfigMap TLS config and starts its watcher, erroring if list/watch is denied.
func startTLSConfigMapWatcher(
	ctx context.Context, kubeClient kubernetes.Interface, namespace string,
) (*sdktls.TLSConfig, error) {
	allowed, err := canWatchTLSProfileConfigMap(ctx, kubeClient, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to check permission to watch the %s ConfigMap: %w",
			sdktls.ConfigMapName, err)
	}

	if !allowed {
		return nil, fmt.Errorf("missing permission to list/watch the %s ConfigMap in namespace %s",
			sdktls.ConfigMapName, namespace)
	}

	onConfigMapChange := func() {
		log.Info("TLS configuration changed; exiting so the pod restarts with the new settings")
		os.Exit(0)
	}

	return sdktls.StartTLSConfigMapWatcher(ctx, kubeClient, namespace, onConfigMapChange)
}

func canWatchTLSProfileConfigMap(ctx context.Context, kubeClient kubernetes.Interface, namespace string) (bool, error) {
	return canWatchResource(ctx, kubeClient, namespace, "", "configmaps", sdktls.ConfigMapName)
}

func canWatchAPIServerTLSProfile(ctx context.Context, kubeClient kubernetes.Interface) (bool, error) {
	return canWatchResource(ctx, kubeClient, "", configv1.GroupName, "apiservers", openshifttls.APIServerName)
}

func canWatchResource(
	ctx context.Context, kubeClient kubernetes.Interface, namespace, group, resource, name string,
) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, verb := range []string{"list", "watch"} {
		review := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: namespace,
					Verb:      verb,
					Group:     group,
					Resource:  resource,
					Name:      name,
				},
			},
		}

		result, err := kubeClient.AuthorizationV1().SelfSubjectAccessReviews().Create(
			ctx, review, metav1.CreateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to check %s permission on %s/%s: %w",
				verb, resource, name, err)
		}

		if !result.Status.Allowed {
			return false, nil
		}
	}

	return true, nil
}

// splitTLSGroups splits the comma-separated --tls-groups value into group names.
func splitTLSGroups(groups string) []configv1.TLSGroup {
	var parsed []configv1.TLSGroup

	for group := range strings.SplitSeq(groups, ",") {
		group = strings.TrimSpace(group)
		if group != "" {
			parsed = append(parsed, configv1.TLSGroup(group))
		}
	}

	return parsed
}

// readAPIServerTLSProfileSpec fetches the APIServer's TLS profile spec using a client built from cfg.
func readAPIServerTLSProfileSpec(
	ctx context.Context, cfg *rest.Config,
) (spec configv1.TLSProfileSpec, watchable bool) {
	if cfg == nil {
		return configv1.TLSProfileSpec{}, false
	}

	apiServerClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "Failed to create a client for the APIServer resource")

		return configv1.TLSProfileSpec{}, false
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	return fetchAPIServerTLSProfileSpec(fetchCtx, apiServerClient)
}

// fetchAPIServerTLSProfileSpec returns the resolved spec and whether it can be watched: false when
// the profile is unavailable or forbidden, true otherwise so transient errors can recover.
func fetchAPIServerTLSProfileSpec(
	ctx context.Context, apiServerClient client.Client,
) (spec configv1.TLSProfileSpec, watchable bool) {
	spec, err := openshifttls.FetchAPIServerTLSProfile(ctx, apiServerClient)
	switch {
	case err == nil:
		return spec, true
	case meta.IsNoMatchError(err) || apierrors.IsNotFound(err):
		log.Info("The APIServer TLS Profile is not served by this cluster; using Go TLS defaults")

		return configv1.TLSProfileSpec{}, false
	case apierrors.IsForbidden(err):
		log.Info("Access to the APIServer TLS Profile is forbidden; using Go TLS defaults")

		return configv1.TLSProfileSpec{}, false
	default:
		log.Error(err, "Failed to read the APIServer TLS Profile; "+
			"will keep watching it in case this was transient")

		return configv1.TLSProfileSpec{}, true
	}
}

// apiServerTLSWatcherSetup defers registering the APIServer TLS profile watcher until Start, so a
// denied list/watch cannot block cache sync; the cache and reconcile retry until it is granted.
type apiServerTLSWatcherSetup struct {
	mgr         ctrl.Manager
	initialSpec configv1.TLSProfileSpec
}

// NeedLeaderElection returns false so every replica watches the profile.
func (*apiServerTLSWatcherSetup) NeedLeaderElection() bool {
	return false
}

func (w *apiServerTLSWatcherSetup) Start(context.Context) error {
	watcher := &openshifttls.SecurityProfileWatcher{
		Client:                w.mgr.GetClient(),
		InitialTLSProfileSpec: w.initialSpec,
		OnProfileChange: func(_ context.Context, old, current configv1.TLSProfileSpec) {
			if slices.Equal(old.Groups, current.Groups) {
				return
			}

			log.Info("The APIServer TLS security profile changed; " +
				"exiting to apply the new settings")

			os.Exit(0)
		},
	}

	if err := watcher.SetupWithManager(w.mgr); err != nil {
		log.Error(err, "Unable to watch the APIServer TLS profile")
	}

	return nil
}
