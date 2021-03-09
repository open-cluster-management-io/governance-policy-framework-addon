// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controller

import (
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// AddToManagerFuncs is a list of functions to add all Controllers to the Manager
var AddToManagerFuncs []func(manager.Manager) error

// AddToManagerFuncsWithCfg is a list of functions to add all controllers to the Manager with additional client cfg
var AddToManagerFuncsWithCfg []func(manager.Manager, *rest.Config) error

// AddToManager adds all Controllers to the Manager
func AddToManager(m manager.Manager, cfg *rest.Config) error {
	for _, f := range AddToManagerFuncs {
		if err := f(m); err != nil {
			return err
		}
	}

	for _, f := range AddToManagerFuncsWithCfg {
		if err := f(m, cfg); err != nil {
			return err
		}
	}
	return nil
}
