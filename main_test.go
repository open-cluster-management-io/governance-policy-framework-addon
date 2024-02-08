//go:build e2e
// +build e2e

// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"fmt"
	"os"
	"testing"
)

// TestRunMain wraps the main() function in order to build a test binary and collection coverage for
// E2E/Integration tests. Controller CLI flags are also passed in here.
func TestRunMain(t *testing.T) {
	clusterNs := os.Getenv("E2E_CLUSTER_NAMESPACE")
	if clusterNs == "" {
		clusterNs = os.Getenv("MANAGED_CLUSTER_NAME")
	}

	clusterNsHub := os.Getenv("E2E_CLUSTER_NAMESPACE_ON_HUB")
	if clusterNsHub == "" {
		clusterNsHub = clusterNs
	}

	disableGkStr := os.Getenv("DISABLE_GK_SYNC")
	disableGk := disableGkStr == "true"

	complianceAPIURL := os.Getenv("COMPLIANCE_API_URL")

	os.Args = append(
		os.Args,
		"--leader-elect=false",
		fmt.Sprintf("--disable-gatekeeper-sync=%t", disableGk),
		fmt.Sprintf("--cluster-namespace=%s", clusterNs),
		fmt.Sprintf("--cluster-namespace-on-hub=%s", clusterNsHub),
		fmt.Sprintf("--compliance-api-url=%s", complianceAPIURL),
	)

	main()
}
