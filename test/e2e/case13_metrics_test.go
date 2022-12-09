// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"open-cluster-management.io/governance-policy-framework-addon/test/utils"
)

var _ = Describe("Test metrics are exposed", Ordered, func() {
	It("Should expose the controller runtime reconcile total for each controller", func() {
		controllers := []string{
			"policy-spec-sync",
			"policy-status-sync",
			"policy-template-sync",
			"secret-sync",
		}

		for _, ctrl := range controllers {
			By("Checking for the " + ctrl + " controller metric")
			matches := utils.GetMetrics("controller_runtime_reconcile_total", "success", ctrl)
			Expect(matches).To(HaveLen(1))
		}
	})
})
