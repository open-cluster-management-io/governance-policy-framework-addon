// Copyright (c) 2020 Red Hat, Inc.
package controller

import (
	"github.com/open-cluster-management/governance-policy-spec-sync/pkg/controller/sync"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncsWithCfg = append(AddToManagerFuncsWithCfg, sync.Add)
}
