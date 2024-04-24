// Copyright (c) 2023 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package templatesync

import (
	"strings"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// templatePredicates filters out changes to policies that don't need to be
// considered by the template-sync controller.
func templatePredicates() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldPolicy := e.ObjectOld.(*policiesv1.Policy)
			updatedPolicy := e.ObjectNew.(*policiesv1.Policy)

			if oldPolicy.Generation != updatedPolicy.Generation {
				// The spec changed - templates need to be updated.
				return true
			}

			if hasAnyDependencies(updatedPolicy) {
				// if it has dependencies, and it's not currently Pending, then
				// it needs to re-calculate if it *should* be Pending.
				if updatedPolicy.Status.ComplianceState != "Pending" {
					return true
				}
			}

			// Look through the history for `template-error` events. If one is
			// found and the "current" (0th) event is not a `template-error`, then
			// reconcile to determine if the template-error event needs to be
			// re-sent.
			for _, dpt := range updatedPolicy.Status.Details {
				if dpt == nil {
					continue
				}

				for i, historyEvent := range dpt.History {
					if strings.Contains(historyEvent.Message, "template-error") {
						if i == 0 {
							break
						}

						return true
					}
				}
			}

			return false
		},
	}
}

// hasAnyDependencies returns true if the policy has any Dependencies or if
// any of its templates have any ExtraDependencies.
func hasAnyDependencies(pol *policiesv1.Policy) bool {
	if len(pol.Spec.Dependencies) > 0 {
		return true
	}

	for _, tmpl := range pol.Spec.PolicyTemplates {
		if len(tmpl.ExtraDependencies) > 0 {
			return true
		}
	}

	return false
}
