// Copyright Contributors to the Open Cluster Management project

package clusterpolicyreport

import (
	"context"
	"strings"

	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
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
	ControllerName string = "clusterpolicyreport-status-relay"
	eventFmtStr    string = "policy: %s/%s"
)

var log = ctrl.Log.WithName(ControllerName)

var sentForClusterPolicy = map[string]string{}

//+kubebuilder:rbac:groups=wgpolicyk8s.io,resources=clusterpolicyreports,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterPolicyReportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&wgpolicyk8s.ClusterPolicyReport{}).
		Complete(r)
}

// blank assignment to verify that ClusterPolicyReportReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &ClusterPolicyReportReconciler{}

// ClusterPolicyReportReconciler reconciles ClusterPolicyReport and PolicyReport objects
type ClusterPolicyReportReconciler struct {
	client.Client
	ClientSet        *kubernetes.Clientset
	ClusterNamespace string
	InstanceName     string
	Scheme           *runtime.Scheme
	Recorder         record.EventRecorder
	ControllerName   string
}

func (r *ClusterPolicyReportReconciler) GetInstanceName() string {
	return r.InstanceName
}

func (r *ClusterPolicyReportReconciler) GetClusterNamespace() string {
	return r.ClusterNamespace
}

func (r *ClusterPolicyReportReconciler) GetClientSet() *kubernetes.Clientset {
	return r.ClientSet
}

func (r *ClusterPolicyReportReconciler) GetControllerName() string {
	return r.ControllerName
}

func (r *ClusterPolicyReportReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling the ClusterPolicyReport")

	report := &wgpolicyk8s.ClusterPolicyReport{}

	err := r.Get(ctx, request.NamespacedName, report)
	if err != nil {
		if errors.IsNotFound(err) {
			log.V(2).Info("The ClusterPolicyReport was deleted")

			return reconcile.Result{}, nil
		}

		log.Error(err, "Failed to get the ClusterPolicyReport")

		return reconcile.Result{}, err
	}

	labelReq, err := labels.NewRequirement(
		"policy.open-cluster-management.io/cluster-namespace", selection.Equals, []string{r.ClusterNamespace},
	)
	if err != nil {
		panic(
			"The ClusterNamespace " + r.ClusterNamespace + " couldn't be used to construct a label selector: " +
				err.Error(),
		)
	}
	labelSelector := labels.NewSelector()
	labelSelector = labelSelector.Add(*labelReq)
	kyvernoPolicies := kyvernov1.ClusterPolicyList{}

	err = r.List(ctx, &kyvernoPolicies, &client.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		log.Error(err, "Failed to query for the Kyverno ClusterPolicies owned by OCM Policies")

		return reconcile.Result{}, err
	}

	kyvernoPoliciesLeft := map[string]*kyvernov1.ClusterPolicy{}

	for _, kyvernoPolicy := range kyvernoPolicies.Items {
		kyvernoPoliciesLeft[kyvernoPolicy.GetName()] = kyvernoPolicy.DeepCopy()
	}

	kyvernoPolicyResults := map[string][]*wgpolicyk8s.PolicyReportResult{}

	for _, result := range report.Results {
		// In some versions of Kyverno, the source starts with a capital K.
		if strings.ToLower(result.Source) != "kyverno" {
			continue
		}

		// Skip non-OCM managed Kyverno policies.
		if kyvernoPoliciesLeft[result.Policy] == nil {
			continue
		}

		kyvernoPolicyResults[result.Policy] = append(kyvernoPolicyResults[result.Policy], result)
	}

	for kyvernoPolicyName, results := range kyvernoPolicyResults {
		kyvernoPolicy := kyvernoPoliciesLeft[kyvernoPolicyName]

		var complianceMsg string
		compliance := policyv1.Compliant

		for _, result := range results {
			if result.Result == "fail" || result.Result == "error" {
				compliance = policyv1.NonCompliant
			}

			if complianceMsg != "" {
				complianceMsg += "; "
			}

			complianceMsg += result.Description

			if len(result.Subjects) == 0 {
				continue
			}

			complianceMsg += " ("

			for i, subject := range result.Subjects {
				if i != 0 {
					complianceMsg += "; "
				}

				complianceMsg += objectReferenceToString(subject)
			}

			complianceMsg += ")"
		}

		if sentForClusterPolicy[kyvernoPolicy.GetName()] != complianceMsg {
			err = common.SendComplianceEvent(r, kyvernoPolicy, complianceMsg, compliance)
			if err != nil {
				return reconcile.Result{}, err
			}

			sentForClusterPolicy[kyvernoPolicy.GetName()] = complianceMsg
		}

		delete(kyvernoPoliciesLeft, kyvernoPolicyName)
	}

	for _, kyvernoPolicy := range kyvernoPoliciesLeft {
		if !kyvernoPolicy.Status.Ready {
			continue
		}

		if sentForClusterPolicy[kyvernoPolicy.GetName()] == "" {
			continue
		}

		// This means that previously, an noncompliant event was sent and now it's compliant.
		err := common.SendComplianceEvent(r, kyvernoPolicy, "The ClusterPolicy is compliant.", policyv1.Compliant)
		if err != nil {
			return reconcile.Result{}, err
		}

		delete(sentForClusterPolicy, kyvernoPolicy.GetName())
	}

	return reconcile.Result{}, nil
}

func objectReferenceToString(objRef *corev1.ObjectReference) string {
	rv := objRef.APIVersion + " " + objRef.Kind + " "

	if objRef.Namespace != "" {
		rv += objRef.Namespace + "/"
	}

	rv += objRef.Name

	return rv
}
