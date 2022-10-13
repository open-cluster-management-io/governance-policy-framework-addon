// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project
// This contains ports of github.com/operator-framework/operator-sdk v0.19.4.
// This is required since operator-sdk is no longer a dependency after upgrading
// to operator-sdk v1.x.x for this project.

package tool

import (
	"fmt"
	"os"
	"strings"
)

type RunModeType string

const (
	ForceRunModeEnv             = "OSDK_FORCE_RUN_MODE"
	LocalRunMode    RunModeType = "local"
	ClusterRunMode  RunModeType = "cluster"
)

// ErrNoNamespace indicates that a namespace could not be found for the current
// environment
var ErrNoNamespace = fmt.Errorf("namespace not found for current environment")

// ErrRunLocal indicates that the operator is set to run in local mode (this error
// is returned by functions that only work on operators running in cluster mode)
var ErrRunLocal = fmt.Errorf("operator run mode forced to local")

func isRunModeLocal() bool {
	return os.Getenv(ForceRunModeEnv) == string(LocalRunMode)
}

// GetOperatorNamespace returns the namespace the operator should be running in.
func GetOperatorNamespace() (string, error) {
	if isRunModeLocal() {
		return "", ErrRunLocal
	}

	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNoNamespace
		}

		return "", err
	}

	ns := strings.TrimSpace(string(nsBytes))
	log.V(1).Info("Found namespace", "Namespace", ns)

	return ns, nil
}
