// Copyright (c) 2020 Red Hat, Inc.

package main

import (
	"flag"
	"os"

	"github.com/open-cluster-management/governance-policy-spec-sync/cmd/manager/tool"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/prometheus/common/log"
	"github.com/spf13/pflag"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

func main() {
	var namespace string

	// Add the zap logger flag set to the CLI. The flag set must
	// be added before calling pflag.Parse().
	pflag.CommandLine.AddFlagSet(zap.FlagSet())

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	pflag.StringVar(&namespace, "namespace", "", "namespace to delete")

	pflag.Parse()

	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Starting uninstall ns...")

	var generatedClient kubernetes.Interface = kubernetes.NewForConfigOrDie(cfg)
	if err := tool.DeleteClusterNs(&generatedClient, namespace); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}
	log.Info("Finished uninstall ns...")
}
