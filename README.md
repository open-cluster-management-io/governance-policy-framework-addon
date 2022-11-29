[comment]: # " Copyright Contributors to the Open Cluster Management project "

# Governance Policy Framework Addon [![KinD tests](https://github.com/open-cluster-management-io/governance-policy-framework-addon/actions/workflows/kind.yml/badge.svg?branch=main&event=push)](https://github.com/open-cluster-management-io/governance-policy-framework-addon/actions/workflows/kind.yml)[![License](https://img.shields.io/:license-apache-blue.svg)](http://www.apache.org/licenses/LICENSE-2.0.html)

## Description

### Secret Sync Controller

The secret sync controller runs on managed clusters and syncs the `policy-encryption-key` `Secret` from the Hub to the
managed cluster. This controller requires access to get, create, update, and delete `Secret` objects in the managed
cluster namespace. Since the managed cluster namespace is not known at build time, the configuration in
`deploy/operator.yaml` grants this access cluster wide. In a production environment, limit this to just the managed
cluster namespace.

### Spec Sync Controller

The spec sync controller runs on managed clusters, updating local `Policy` specs to match `Policies` in the cluster's
namespace on the hub cluster.

The controller watches for changes to Policies in the cluster's namespace on the hub cluster to trigger a reconcile.
Every reconcile creates/updates/deletes replicated policies on the managed cluster to match the spec from the hub
cluster.

### Status Sync Controller

The status sync controller runs on managed clusters, updating `Policy` statuses on both the hub and (local) managed
clusters, based on events and changes in the managed cluster.

This controller watches for the following changes to trigger a reconcile:

1. policy changes in the watched cluster namespace on the managed cluster
2. events on policies in the watched cluster namespace on the managed cluster

Every reconcile does the following things:

1. Creates/updates the policy status on the hub and managed cluster in cluster namespace

### Template Sync Controller

The template sync controller runs on managed clusters and updates objects defined in the templates of `Policies` in the
cluster namespace.

This controller watches for changes on `Policies` in the cluster namespace on the managed cluster to trigger a
reconcile. On each reconcile, it creates/updates/deletes objects defined in the `spec.policy-templates` of those
`Policies`.

## Getting started

For documentation and installation guidance, see the
[Open Cluster Management documentation](https://open-cluster-management.io/getting-started/integration/policy-framework/).

Go to the
[Contributing guide](https://github.com/open-cluster-management-io/community/blob/main/sig-policy/contribution-guidelines.md)
to learn how to get involved.

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

### deploy/operator.yaml

The `deploy/operator.yaml` file is generated via Kustomize. The `deploy/rbac` directory of Kustomize files is managed by
the operator-sdk and Kubebuilder using [markers](https://book.kubebuilder.io/reference/markers.html). After updating the
markers or any of the Kustomize files, you may regenerate `deploy/operator.yaml` by running
`make generate-operator-yaml`.

## References

- The `governance-policy-framework-addon` is part of the `open-cluster-management` community. For more information,
  visit: [open-cluster-management.io](https://open-cluster-management.io).

<!---
Date: 2022-11-28
-->
