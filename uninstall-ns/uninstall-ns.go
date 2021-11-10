// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"flag"
	"os"

	"github.com/open-cluster-management/governance-policy-spec-sync/tool"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func main() {
	var namespace string
	var log = logf.Log.WithName("uninstall")

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
		if !errors.IsNotFound(err) {
			log.Error(err, "")
			os.Exit(1)
		}
	}
	log.Info("Finished uninstall ns...")
}
