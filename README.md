# Governance Policy Spec Sync
Red Hat Advance Cluster Management Governance - Policy Spec Sync

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
