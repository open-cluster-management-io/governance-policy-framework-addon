// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// getMetrics curls the metrics endpoint, filters the response with the given patterns,
// and returns the value(s) for the matching metric(s).
func getMetrics(metricPatterns ...string) []string {
	metricFilter := " | grep " + strings.Join(metricPatterns, " | grep ")
	metricsCmd := `curl localhost:8383/metrics` + metricFilter
	cmd := exec.Command("bash", "-c", metricsCmd)

	matchingMetricsRaw, err := cmd.Output()
	if err != nil {
		if err.Error() == "exit status 1" {
			return []string{} // exit 1 indicates that grep couldn't find a match.
		}

		return []string{err.Error()}
	}

	matchingMetrics := strings.Split(strings.TrimSpace(string(matchingMetricsRaw)), "\n")
	values := make([]string, len(matchingMetrics))

	for i, metric := range matchingMetrics {
		fields := strings.Fields(metric)
		if len(fields) > 0 {
			values[i] = fields[len(fields)-1]
		}
	}

	return values
}

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
			matches := getMetrics("controller_runtime_reconcile_total", "success", ctrl)
			Expect(matches).To(HaveLen(1))
		}
	})
})
