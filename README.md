# Governance Policy Template Sync
Red Hat Advance Cluster Management Governance - Policy Template Sync

## How it works

This operator watches for following changes to trigger reconcile


1. policies changes in watching cluster namespace on managed cluster

Every reconcile does following things:

1. Create/update/delete objects defined in spec.policy-templates on managed cluster in cluster namespace

## Run
```
operator-sdk run --local --operator-flags "--kubeconfig=path_to_kubeconfig"
```

<!---
Date: Jan/04/2021
-->
