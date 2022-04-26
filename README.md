[comment]: # ( Copyright Contributors to the Open Cluster Management project )

# Governance Policy Spec Sync [![KinD tests](https://github.com/open-cluster-management-io/governance-policy-spec-sync/actions/workflows/kind.yml/badge.svg?branch=main&event=push)](https://github.com/open-cluster-management-io/governance-policy-spec-sync/actions/workflows/kind.yml)[![License](https://img.shields.io/:license-apache-blue.svg)](http://www.apache.org/licenses/LICENSE-2.0.html)

## Description

The governance policy spec sync is a controller that runs on managed clusters, updating local `Policy` specs to match `Policies` in the cluster's namespace on the hub cluster. This controller is a part of the [governance-policy-framework](https://github.com/open-cluster-management/governance-policy-framework).

The operator watches for changes to Policies in the cluster's namespace on the hub cluster to trigger a reconcile. Every reconcile creates/updates/deletes replicated policies on the managed cluster to match the spec from the hub cluster.

## Geting started

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

The `deploy/operator.yaml` file is generated via Kustomize. The `deploy/rbac` directory of
Kustomize files is managed by the operator-sdk and Kubebuilder using
[markers](https://book.kubebuilder.io/reference/markers.html). After updating the markers or
any of the Kustomize files, you may regenerate `deploy/operator.yaml` by running
`make generate-operator-yaml`.

The `secret-sync` controller requires access to get, create, update, and delete Secret objects in
the managed cluster namespace. Since the managed cluster namespace is not known at build time, the
configuration in `deploy/operator.yaml` grants this access cluster wide. In a production
environment, limit this to just the managed cluster namespace.

## References

- The `governance-policy-spec-sync` is part of the `open-cluster-management` community. For more information, visit: [open-cluster-management.io](https://open-cluster-management.io).

<!---
Date: 11/08/2021
-->
