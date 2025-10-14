# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

The Governance Policy Framework Addon is a Kubernetes controller that runs on **managed clusters** in an Open Cluster Management (OCM) environment. It synchronizes policies between the hub cluster and managed clusters, manages policy templates, and reports compliance status back to the hub.

## Key Architecture Concepts

### Dual-Cluster Operation

This controller operates across **two clusters simultaneously**:
- **Hub Cluster**: The central management cluster where policies are defined
- **Managed Cluster**: The target cluster where policies are enforced and compliance is evaluated

The controller runs on the managed cluster but maintains connections to both clusters using separate kubeconfig files.

### Two Manager Pattern

The application runs **two controller-runtime managers concurrently**:

1. **Managed Manager** (`getManager`): Watches resources on the managed cluster
   - Runs template-sync, status-sync controllers
   - Leader election ID: `governance-policy-framework-addon.open-cluster-management.io`

2. **Hub Manager** (`getHubManager`): Watches resources on the hub cluster
   - Runs spec-sync, secret-sync controllers
   - Leader election ID: `governance-policy-framework-addon2.open-cluster-management.io`
   - Can be disabled with `--disable-spec-sync` flag

Both managers share a global metrics registry but only the managed manager exposes the metrics endpoint.

### Controller Communication

Controllers coordinate using **buffered channels** to trigger cross-controller reconciles:
- `specSyncRequests`: Status-sync triggers spec-sync when policy mismatches are detected
- `statusSyncRequests`: Spec-sync triggers status-sync after updating managed policies

This prevents circular reconciliation loops while ensuring eventual consistency.

### Core Controllers

**Spec Sync Controller** (`controllers/specsync/`)
- Watches policies in the cluster namespace on the **hub**
- Creates/updates/deletes replicated policies on the **managed** cluster
- Ensures managed cluster policies match hub cluster policy specs

**Status Sync Controller** (`controllers/statussync/`)
- Watches policies and events on the **managed** cluster
- Uses `kubernetes-dependency-watches` to dynamically watch policy template objects
- Collects compliance history from both cluster events and template status fields
- Updates policy status on both managed and hub clusters
- Deduplicates events and maintains up to 10 historical events per template

**Template Sync Controller** (`controllers/templatesync/`)
- Watches policies on the **managed** cluster
- Creates/updates/deletes objects defined in `spec.policy-templates`
- Uses `kubernetes-dependency-watches` to track created template objects
- Handles template validation and error reporting

**Gatekeeper Sync Controller** (`controllers/gatekeepersync/`)
- Dynamically started/stopped based on Gatekeeper installation
- Runs in a **separate manager** (`governance-policy-framework-addon3.open-cluster-management.io`)
- Syncs Gatekeeper constraint violation status to policy status
- Uses `manageGatekeeperSyncManager` to watch for Gatekeeper CRD installation

**Secret Sync Controller** (`controllers/secretsync/`)
- Syncs `policy-encryption-key` secret from hub to managed cluster
- Runs on the hub manager

**Uninstall Watcher** (`controllers/uninstall/`)
- Monitors for uninstall signals via deployment annotations
- Sets `DeploymentIsUninstalling` flag to pause reconciliation
- Separate `trigger-uninstall` CLI command for cleanup operations

## Development Commands

### Local Development Setup

```bash
# Create KinD clusters and install CRDs
make kind-bootstrap-cluster-dev

# Build Docker images
make build-images

# Deploy controller to KinD cluster
make kind-deploy-controller-dev

# For hosted mode testing
HOSTED=hosted make kind-deploy-controller-dev
```

### Testing

```bash
# Unit tests
make test-dependencies
make test

# Unit tests with coverage
make test-coverage

# E2E tests
make e2e-dependencies
make e2e-test

# E2E tests with coverage
make e2e-test-coverage

# Uninstall E2E tests
make e2e-test-uninstall-coverage
```

### Running Locally

```bash
# Run controller locally (requires kubeconfig files)
HUB_CONFIG=./kubeconfig_hub MANAGED_CONFIG=./kubeconfig_managed \
  go run ./main.go --leader-elect=false --cluster-namespace=managed
```

### Building

```bash
# Build binary
make build

# The binary will be at: build/_output/bin/governance-policy-framework-addon
```

### Code Generation

```bash
# Generate DeepCopy methods
make generate

# Generate RBAC manifests
make manifests

# Generate deploy/operator.yaml
make generate-operator-yaml
```

### Cleanup

```bash
# Delete KinD clusters
make kind-delete-cluster

# Clean build artifacts
make clean
```

### Debugging

```bash
# Show debug information from test environment
make e2e-debug
```

## Important Configuration

### Environment Variables

- `HUB_CONFIG`: Path to hub cluster kubeconfig (required)
- `MANAGED_CONFIG`: Path to managed cluster kubeconfig (optional, uses in-cluster config if not set)
- `OSDK_FORCE_RUN_MODE`: Set to `local` for local development
- `ON_MULTICLUSTERHUB`: Set to `true` to skip hub status updates (for hub-side deployments)

### CLI Flags

- `--cluster-namespace`: Namespace on managed cluster where policies are replicated (required)
- `--cluster-namespace-on-hub`: Namespace on hub cluster (defaults to `--cluster-namespace`)
- `--hub-cluster-configfile`: Path to hub kubeconfig
- `--disable-spec-sync`: Disable spec-sync controller (for hub-side deployments)
- `--disable-gk-sync`: Disable Gatekeeper integration
- `--enable-lease`: Enable status reporting via lease
- `--evaluation-concurrency`: Max concurrent reconciles (default varies by controller)

## Code Patterns

### Dynamic Watching with kubernetes-dependency-watches

Status-sync and template-sync use dynamic watching to track arbitrary Kubernetes objects:

```go
// Start query batch before reconcile
err := r.DynamicWatcher.StartQueryBatch(policyObjID)
defer r.DynamicWatcher.EndQueryBatch(policyObjID)

// Get and watch a dynamic object
obj, err := r.DynamicWatcher.Get(policyObjID, gvk, namespace, name)
```

This enables controllers to watch policy template objects without knowing their types at compile time.

### Health Endpoint Proxy

The main function runs a health proxy (`startHealthProxy`) that aggregates health checks from all managers. Each manager gets a unique local port, and the proxy at `--health-addr` (default :8081) combines their responses.

### Event Deduplication

Status-sync uses a custom event broadcaster with aggressive deduplication:
- `MaxIntervalInSeconds: 1` - Nearly disables event aggregation
- Custom `SpamKeyFunc` includes message content in deduplication key
- Prevents loss of distinct policy compliance events

## Repository Structure Notes

- `main.go`: Single entry point for both controllers and uninstall trigger
- `tool/options.go`: CLI flag definitions
- `controllers/utils/`: Shared utilities for event handling and policy comparison
- `test/e2e/`: E2E tests using Ginkgo/Gomega
- `test/resources/`: Test policy manifests organized by test case
- `deploy/`: Kustomize manifests for deployment
- `build/common/`: Shared Makefiles and scripts

## Testing Notes

- E2E tests create two KinD clusters (hub and managed)
- Tests use `_e2e` suffix kubeconfig files
- Gatekeeper integration requires Kubernetes >= 1.16.0
- Coverage threshold: 69% (set via `COVERAGE_MIN`)
- Tests support different Kubernetes versions via `KIND_VERSION` (latest/minimum)
