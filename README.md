[comment]: # ( Copyright Contributors to the Open Cluster Management project )

# Governance Policy Status Sync [![KinD tests](https://github.com/open-cluster-management-io/governance-policy-status-sync/actions/workflows/kind.yml/badge.svg?branch=main&event=push)](https://github.com/open-cluster-management-io/governance-policy-status-sync/actions/workflows/kind.yml)[![License](https://img.shields.io/:license-apache-blue.svg)](http://www.apache.org/licenses/LICENSE-2.0.html)

## Description

The governance policy status sync runs on managed clusters, updating `Policy` statuses on both the hub and (local) managed clusters, based on events and changes in the managed cluster. This controller is a part of the [governance-policy-framework](https://github.com/open-cluster-management-io/governance-policy-framework).

This operator watches for the following changes to trigger a reconcile:

1. policy changes in the watched cluster namespace on the managed cluster
2. events on policies in the watched cluster namespace on the managed cluster

Every reconcile does the following things:

1. Creates/updates the policy status on the hub and managed cluster in cluster namespace

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

- The `governance-policy-status-sync` is part of the `open-cluster-management` community. For more information, visit: [open-cluster-management.io](https://open-cluster-management.io).

<!---
Date: June/11/2021
-->
