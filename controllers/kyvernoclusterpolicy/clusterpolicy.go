// Copyright Contributors to the Open Cluster Management project

package kyvernoclusterpolicy

import (
	"context"
	"strings"

	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"open-cluster-management.io/governance-policy-framework-addon/controllers/common"
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	wgpolicyk8s "sigs.k8s.io/wg-policy-prototypes/policy-report/pkg/api/wgpolicyk8s.io/v1alpha2"
)

const (
	ControllerName          string = "kyverno-clusterpolicy"
	clusterPolicyReportName string = "clusterpolicyreport"
	eventFmtStr             string = "policy: %s/%s"
)

var log = ctrl.Log.WithName(ControllerName)

var sentForClusterPolicy = map[string]bool{}
var notReadySentForClusterPolicy = map[string]bool{}

//+kubebuilder:rbac:groups=kyverno.io,resources=clusterpolicy,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&kyvernov1.ClusterPolicy{}).
		Complete(r)
}

// blank assignment to verify that ClusterPolicyReportReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &ClusterPolicyReconciler{}

// PolicyReportReconciler reconciles ClusterPolicyReport and PolicyReport objects
type ClusterPolicyReconciler struct {
	client.Client
	ClientSet        *kubernetes.Clientset
	ClusterNamespace string
	InstanceName     string
	Scheme           *runtime.Scheme
	Recorder         record.EventRecorder
	ControllerName   string
}

func (r *ClusterPolicyReconciler) GetInstanceName() string {
	return r.InstanceName
}

func (r *ClusterPolicyReconciler) GetClusterNamespace() string {
	return r.ClusterNamespace
}

func (r *ClusterPolicyReconciler) GetClientSet() *kubernetes.Clientset {
	return r.ClientSet
}

func (r *ClusterPolicyReconciler) GetControllerName() string {
	return r.ControllerName
}

func (r *ClusterPolicyReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	clusterPolicy := kyvernov1.ClusterPolicy{}
	err := r.Get(ctx, request.NamespacedName, &clusterPolicy)
	if err != nil {
		log.Error(err, "Failed to get the ClusterPolicy")

		return reconcile.Result{}, err
	}

	if clusterPolicy.Status.Ready {
		delete(notReadySentForClusterPolicy, clusterPolicy.Name)
	} else {
		if notReadySentForClusterPolicy[clusterPolicy.Name] {
			log.V(2).Info("Skipping sending not ready status event since it was already sent")

			return reconcile.Result{}, nil
		}

		msg := "The ClusterPolicy is not ready."
		err = common.SendComplianceEvent(r, &clusterPolicy, msg, policyv1.NonCompliant)
		if err != nil {
			return reconcile.Result{}, err
		}

		notReadySentForClusterPolicy[clusterPolicy.Name] = true

		return reconcile.Result{}, nil
	}

	if sentForClusterPolicy[clusterPolicy.Name] {
		// Flipping from non-compliant to compliant is handled by the ClusterPolicyReport controller.
		return reconcile.Result{}, nil
	}

	reports := wgpolicyk8s.ClusterPolicyReportList{}
	err = r.List(ctx, &reports)
	if err != nil {
		log.Error(err, "Failed to list the ClusterPolicyReports")

		return reconcile.Result{}, err
	}

	for _, report := range reports.Items {
		for _, result := range report.Results {
			// In some versions of Kyverno, the source starts with a capital K.
			if strings.ToLower(result.Source) != "kyverno" {
				continue
			}

			if result.Result == "fail" || result.Result == "error" {
				if result.Policy == report.Name {
					log.V(1).Info(
						"The ClusterPolicy has a failure result. The ClusterPolicyReport controller will handle this.",
					)

					return reconcile.Result{}, nil
				}
			}
		}
	}

	err = common.SendComplianceEvent(r, &clusterPolicy, "The ClusterPolicy is compliant.", policyv1.Compliant)
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}
