// Copyright Contributors to the Open Cluster Management project

package gatekeepersync

import (
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// policyPredicates filters out policies without Gatekeeper constraints and policy updates without the generation
// changing.
func policyPredicates() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			policy := e.Object.(*policiesv1.Policy)

			return hasGatekeeperConstraints(policy)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldPolicy := e.ObjectOld.(*policiesv1.Policy)
			updatedPolicy := e.ObjectNew.(*policiesv1.Policy)

			if oldPolicy.Generation == updatedPolicy.Generation {
				return false
			}

			// oldPolicy is also checked in the event all the Gatekeeper constraints were removed.
			return hasGatekeeperConstraints(oldPolicy) || hasGatekeeperConstraints(updatedPolicy)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}
}
