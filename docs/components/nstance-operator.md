---
title: "Nstance Operator"
weight: 30
description: "A Kubernetes operator that syncs Cluster API resources to Nstance Servers, managing instance groups, drain coordination, and on-demand nodes."
---

# Nstance Operator

The `nstance-operator` is a Kubernetes operator that syncs Cluster API (CAPI) resources to Nstance Servers. It connects to every shard via gRPC with mTLS, syncing configuration and desired state from Kubernetes to each server and coordinating node drain when instances need to be removed or replaced. A single operator deployment manages one Nstance cluster and tenant.

The operator can run on a self-managed cluster (managing the Nstance cluster it is running on) or on an external management cluster, separate from the workload cluster — see [Deployment Scenarios](../reference/cluster-api.md#deployment-scenarios) for more details.

The operator is built with [Kubebuilder](https://book.kubebuilder.io/) and controller-runtime, following standard Kubernetes operator conventions. Leader election uses the standard Kubernetes Lease-based mechanism, not the S3-based election ([s3lect](../reference/leader-election.md)) used by nstance-server.

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `/etc/nstance/operator/config.yaml` | Path to the operator configuration file |
| `--health-probe-bind-address` | `:8081` | The address the health probe endpoint binds to |
| `--metrics-bind-address` | `0` (disabled) | The address the metrics endpoint binds to |
| `--leader-elect` | `false` | Enable leader election for controller manager |
| `--disable-webhooks` | `false` | Disable admission webhooks (useful for development) |

Standard zap logging flags are also available (e.g. `--zap-log-level`, `--zap-encoder`).

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NSTANCE_NAMESPACE` | *(pod namespace)* | Namespace for Nstance CRDs, CAPI resources (Cluster, MachinePool, Machine), Secrets, and ConfigMaps managed by the operator |
| `NSTANCE_CA_CONFIGMAP` | `nstance-cluster-ca` | ConfigMap name to load the Nstance cluster CA certificate from (`ca.crt` key) |
| `NSTANCE_CERT_SECRET` | `nstance-operator-cert` | Secret name for operator client certificate (`tls.crt`, `tls.key` keys) |
| `NSTANCE_KEY_SECRET` | `nstance-operator-key` | Secret name for operator keypair (`private.key`, `public.key` keys) |
| `NSTANCE_NONCE_SECRET` | `nstance-operator-nonce` | Secret name for registration nonce JWT (`nonce.jwt` key) |
| `NSTANCE_CAPI_ENDPOINT` | *(empty)* | External workload cluster API server endpoint. When set, the operator skips kubeconfig auto-management and the admin must provide the `<cluster>-kubeconfig` secret. See [Deployment Scenarios](../reference/cluster-api.md#deployment-scenarios) |
| `NSTANCE_CAPI_SERVICEACCOUNT` | `nstance-capi-workload` | ServiceAccount used to generate short-lived tokens for the auto-managed CAPI kubeconfig secret. Only used when `NSTANCE_CAPI_ENDPOINT` is not set |
| `NSTANCE_K8S_JSON` | *(empty)* | Set to `true` to use JSON content type for K8s API calls |

## Configuration File

The operator configuration file (default: `/etc/nstance/operator/config.yaml`) defines the cluster identity and shard endpoints:

```yaml
cluster_id: example-cluster
tenant: default
shards:
  us-west-2a:
    registration_addr: "10.0.0.1:8992"
    operator_addr: "10.0.0.1:8993"
  us-east-1a:
    registration_addr: "10.0.1.1:8992"
    operator_addr: "10.0.1.1:8993"
```

- `cluster_id` — Unique identifier for the Nstance cluster.
- `tenant` — Tenant identifier (typically `default` unless using a multi-tenant configuration).
- `shards` — Map of shard IDs to their gRPC endpoints. `registration_addr` is used during bootstrap; `operator_addr` is used for ongoing sync.

## Kubernetes Resources

The operator reads configuration from Kubernetes resources in its namespace. The names of thes resources are configurable via environment variables.

### Required Before Startup

| Resource | Default Name | Key(s) | Description |
|----------|-------------|--------|-------------|
| ConfigMap | `nstance-cluster-ca` | `ca.crt` | Cluster CA certificate used to verify server connections |
| Secret | `nstance-operator-nonce` | `nonce.jwt` | Registration nonce JWT for initial bootstrap (see [Registration](#registration)) |

### Created by Operator

| Resource | Default Name | Key(s) | Description |
|----------|-------------|--------|-------------|
| Secret | `nstance-operator-key` | `private.key`, `public.key` | Ed25519 keypair generated during registration |
| Secret | `nstance-operator-cert` | `tls.crt`, `tls.key` | Client certificate received after registration |

After the initial registration, the operator reuses the stored certificate and keypair on subsequent startups. Only the CA ConfigMap and nonce Secret need to be provisioned before deploying the operator.

## Functionality

The operator performs four core functions:

### Group Sync

MachinePool replicas are distributed across shards via NstanceShardGroup resources and synced to nstance-server groups. Kubernetes is the source of truth for replica counts — the operator pushes changes from Kubernetes to the servers, not the other way around.

On startup, the operator imports existing groups from all shards to create initial MachinePool and NstanceMachinePool resources. After that, changes flow unidirectionally from Kubernetes to the servers.

### Drain Coordination

When an instance is marked for deletion (spot termination, expiry, unhealthy replacement), the operator cordons and drains the corresponding Kubernetes node before acknowledging the deletion to the server. If the VM is already gone (provider reports stopped/deleted/failed), draining is skipped.

### Individual Instances

The operator reconciles Machine and NstanceMachine resources, calling `CreateInstance` and `DeleteInstance` on the appropriate shard to manage individual instances. This is used for on-demand nodes where a dedicated instance is provisioned for a specific workload, rather than being part of a scaled group.

### On-Demand Instances

Pods annotated with `on-demand.nstance.dev/group` automatically trigger creation of the Machine and NstanceMachine resources, providing a simple mechanism for creating on-demand nodes.

## Registration

The operator uses a nonce-based registration flow to obtain a client certificate for mTLS communication with nstance-servers.

### Bootstrap Steps

1. **Generate nonce** — Use `nstance-admin cluster nonce --expiry="3h"` to create a registration JWT. See [Nstance Admin](nstance-admin.md#nstance-admin-cluster-nonce) for details.

2. **Store nonce** — Create a Kubernetes Secret with the JWT:
   ```bash
   kubectl create secret generic nstance-operator-nonce \
     --from-file=nonce.jwt=<path-to-nonce>
   ```

3. **Store CA** — Create a ConfigMap with the cluster CA certificate:
   ```bash
   kubectl create configmap nstance-cluster-ca \
     --from-file=ca.crt=<path-to-ca-cert>
   ```

4. **Deploy operator** — On first startup, the operator generates an Ed25519 keypair, registers with any available shard using the nonce, and receives a signed client certificate. Both are stored as Kubernetes Secrets for reuse. On subsequent startups, the operator loads the existing certificate and skips registration. If the operator crashes between keypair generation and registration, it resumes from the stored keypair.

After registration, the operator connects to all shards using the certificate — all shards share the same cluster CA, so a single registration is sufficient. The nonce Secret is no longer needed and can be deleted.

The operator will exit with a fatal error if the configuration file is missing or invalid, if the nonce Secret is missing when registration is needed, or if all shards are unreachable during registration.

### Leader Election

When `--leader-elect` is enabled, only the elected leader performs registration and maintains gRPC connections (lease ID: `nstance-operator-leader-election`). If leadership is lost, the process exits to ensure clean state. A new leader resumes from the stored certificate and keypair Secrets.

### Further Reading

- [Operator Internals](../reference/operator-internals.md) — Sync mechanics, reconciliation loops, drain coordination, CRDs, and connection management
- [Cluster API CRDs](../reference/cluster-api.md) — Full CRD specifications (NstanceMachinePool, NstanceShardGroup, NstanceMachine, etc.)
- [Instance Lifecycle](../reference/instance-lifecycle.md) — How instances are created, replaced, and deleted
