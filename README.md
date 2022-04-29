[comment]: # ( Copyright Contributors to the Open Cluster Management project )

# Governance Policy Template Sync [![KinD tests](https://github.com/stolostron/governance-policy-template-sync/actions/workflows/kind.yml/badge.svg?branch=main&event=push)](https://github.com/stolostron/governance-policy-template-sync/actions/workflows/kind.yml)

## Description

The governance policy template sync runs on managed clusters and updates objects defined in the templates of `Policies` in the cluster namespace. This controller is a part of the [governance-policy-framework](https://github.com/stolostron/governance-policy-framework).

This operator watches for changes on `Policies` in the cluster namespace on the managed cluster to trigger a reconcile. On each reconcile, it creates/updates/deletes objects defined in the `spec.policy-templates` of those `Policies`.

## Geting started 

Check the [Security guide](SECURITY.md) if you need to report a security issue.

### Build and deploy locally
You will need [kind](https://kind.sigs.k8s.io/docs/user/quick-start/) installed.

```bash
make kind-bootstrap-cluster-dev
make build-images
make kind-deploy-controller-dev
```
### Running tests
```
make test-dependencies
make test

make e2e-dependencies
make e2e-test
```

### Clean up
```
make kind-delete-cluster
```

## References

- The `governance-policy-template-sync` is part of the `open-cluster-management` community. For more information, visit: [open-cluster-management.io](https://open-cluster-management.io).

<!---
Date: April/29/2022
-->
