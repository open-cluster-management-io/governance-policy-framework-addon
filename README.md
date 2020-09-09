# Governance Policy Status Sync
Red Hat Advance Cluster Management Governance - Policy Status Sync

## How it works

This operator watches for following changes to trigger reconcile


1. policies changes in watching cluster namespace on managed cluster
2. events changes on policies in watch cluster namespace on managed cluster

Every reconcile does following things:

1. Create/update policy status on managed cluster in cluster namespace

## Run
```
export WATCH_NAMESPACE=cluster_namespace_on_managed
operator-sdk run --local --operator-flags "--hub-cluster-configfile=path_to_kubeconfig --kubeconfig=path_to_kubeconfig"
```
<!---
Date: 9/09/2020
-->