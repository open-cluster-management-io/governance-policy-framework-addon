[comment]: # ( Copyright Contributors to the Open Cluster Management project )

# Governance Policy Spec Sync [![KinD tests](https://github.com/open-cluster-management/governance-policy-spec-sync/actions/workflows/kind.yml/badge.svg?branch=main&event=push)](https://github.com/open-cluster-management/governance-policy-spec-sync/actions/workflows/kind.yml)
Red Hat Advanced Cluster Management Governance - Policy Spec Sync

## How it works

This operator watches for following changes to trigger reconcile


1. policies changes in watching cluster namespace on hub

Every reconcile does following things:

1. Create/update/delete replicated policy on managed cluster in cluster namespace

## Run
```
export WATCH_NAMESPACE=cluster_namespace_on_hub
operator-sdk run --local --operator-flags "--hub-cluster-configfile=path_to_kubeconfig --kubeconfig=path_to_kubeconfig"
```

<!---
Date: Nov/10/2020
-->
