package controller

import (
	"github.com/open-cluster-management/governance-policy-syncer/pkg/controller/syncer"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, syncer.Add)
}
