---
title: "Cluster API Integration"
weight: 70
description: "How Nstance implements the Cluster API (CAPI) infrastructure provider contract for infrastructure management in Kubernetes."
---

# Cluster API Integration

Nstance implements a [Cluster API](https://cluster-api.sigs.k8s.io/) (CAPI) infrastructure provider to enable:

1. Integration with [Cluster Autoscaler](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/cloudprovider/clusterapi/README.md) to drive scaling events for Nstance shard groups by adjusting the number of `MachinePool` replicas automatically.

2. Manually scaling Nstance shard groups by adjusting the number of `MachinePool` replicas e.g. via kubectl.

3. Creating on-demand instances via Pod annotations, where the Nstance Operator will then create the appropriate `Machine` resource for the Nstance Operator to assign and sync to a Nstance Server. This approach was taken to provide visibility to cluster administrators into requested (Machine) vs created (Node) instances.

Nstance does not provision control planes, however to satisfy CAPI contract requirements and enable the CAPI controllers to function correctly, cluster-level CAPI resources (`Cluster` / `NstanceCluster`) are created by Nstance.

## Deployment Scenarios

Cluster API has the concept of a "management" cluster, and a "workload" cluster, in which there are two deployment scenarios Nstance supports...

### Self-managed clusters

The CAPI operator runs inside the same cluster it manages.

CAPI's [runningOnWorkloadCluster](https://github.com/kubernetes-sigs/cluster-api/blob/main/controllers/clustercache/cluster_accessor_client.go#L145) check detects this by finding a matching CAPI controller pod UID and switches to in-cluster credentials. The kubeconfig SA token is only used for the initial health probe and pod lookup.

### External clusters

The CAPI operator manages instances on a different cluster.

The kubeconfig points to the management cluster (where CAPI runs), not the workload cluster. `runningOnWorkloadCluster` returns false (pod not found, 404) and CAPI uses the SA token for all workload cluster API calls.

## Custom Resource Types

Nstance defines four CAPI infrastructure provider CRDs:

| CRD | CAPI Contract | Purpose |
|-----|--------------|---------|
| `NstanceCluster` | InfraCluster | Stub that satisfies CAPI's cluster infrastructure ref requirement |
| `NstanceMachinePool` | InfraMachinePool | Maps an Nstance Group to a CAPI MachinePool, distributing replicas across zone shards |
| `NstanceMachine` | InfraMachine | Represents a single Nstance instance (used for on-demand nodes) |
| `NstanceMachineTemplate` | InfraMachineTemplate | Immutable template for stamping out Machine/NstanceMachine pairs |

In addition to the CAPI infrastructure provider CRDs, Nstance maintains its own `NstanceShardGroup` CRD, which the `NstanceMachinePool` effectively aggregates

- For example, if you have two shards both with a "test" group with 1 instance each, you will have two `NstanceShardGroup` resources with 1 instance in each, and these will map to a single `NstanceMachinePool` with 2 replicas.
- This approach was taken to provide visibility to cluster administrators into the aggregated vs distributed replicas count (rather than doing the (dis-)aggregation only at runtime).

## CAPI Cluster Resource

CAPI requires a `Cluster` resource as the ownership root for MachinePools and Machines. The operator creates this automatically on startup via `ensureCAPICluster` in the leader manager. The cluster name is derived from operator config as `<cluster_id>--<tenant_id>`. Note that neither cluster ID or tenant ID can contain consecutive hyphens.

All namespaced CAPI and Nstance CRD resources are created in the operator's namespace (configurable via `NSTANCE_NAMESPACE`, defaults to the pod's own namespace). CAPI's core controllers (typically in `capi-system`) watch across all namespaces, so they do not need to be co-located.

Within `ensureCAPICluster`, three resources are created together:

1. **NstanceCluster** — infrastructure ref target with a `controlPlaneEndpoint` (host and port parsed from the management cluster's API server address). CAPI's `setPhase` requires a valid endpoint (non-empty host, non-zero port) for the Cluster to reach "Provisioned" phase.

2. **CAPI Cluster** — references the NstanceCluster via `spec.infrastructureRef`.

3. **Kubeconfig Secret** (`<cluster>-kubeconfig`) — provides CAPI's ClusterCache with credentials to connect to the "workload" cluster. Since Nstance does not provision control planes, this points at the management cluster itself.

## NstanceCluster Controller

The `NstanceClusterReconciler` has a single job: mark the NstanceCluster as provisioned and ready. On each reconcile it sets:

- `status.initialization.provisioned = true`
- A `Ready` condition with status `True`

There is no cluster-level infrastructure to provision — Nstance handles everything at the pool/machine level. However, for the required behaviour from the CAPI controllers, we must set this status to provisioned.

## Kubeconfig Secret

CAPI's MachinePool controller requires a `<cluster>-kubeconfig` secret to connect to the workload cluster via its ClusterCache.

The operator's behavior depends on whether `NSTANCE_CAPI_ENDPOINT` is set (see [Deployment Scenarios](#deployment-scenarios) above):

### Self-managed (default)

When `NSTANCE_CAPI_ENDPOINT` is not set, the operator auto-manages the kubeconfig secret with short-lived tokens from a dedicated ServiceAccount:

1. The operator calls the Kubernetes TokenRequest API against the `nstance-capi-workload` ServiceAccount (configurable via `NSTANCE_CAPI_SERVICEACCOUNT` env var).
2. A 1-hour token is issued and embedded in a kubeconfig pointing at `https://kubernetes.default.svc:443` (the in-cluster API server address, since CAPI controllers run as pods in the same cluster).
3. The token expiry is stored in the secret's `nstance.dev/token-expiry` annotation.
4. On each leader start, the operator checks the annotation and refreshes the token if it expires within 10 minutes.

### External cluster

When `NSTANCE_CAPI_ENDPOINT` is set (e.g. `https://workload.example.com:6443`), the operator uses the provided endpoint as the `controlPlaneEndpoint` on the NstanceCluster and skips kubeconfig secret management entirely. The administrator is responsible for creating and rotating the `<cluster>-kubeconfig` secret with credentials for the workload cluster.

The secret must be in the same namespace as the CAPI Cluster resource and carry the `cluster.x-k8s.io/cluster-name` label — CAPI's ClusterCache discovers it by label and namespace match.

## RBAC for the CAPI ServiceAccount

The `nstance-capi-workload` ServiceAccount is bound to the `nstance-capi-workload` ClusterRole with minimal permissions:

| Resource | Verbs | Reason |
|----------|-------|--------|
| `nodes` | get, list, watch | CAPI's ClusterCache needs node access for node ref matching |
| `pods` | get | CAPI's [`runningOnWorkloadCluster`](https://github.com/kubernetes-sigs/cluster-api/blob/main/controllers/clustercache/cluster_accessor_client.go#L145) GETs its own pod via the kubeconfig to detect if the management and workload clusters are the same. A non-404 error (e.g. 403 Forbidden) blocks the ClusterCache connection entirely. |
| `nonResourceURLs: ["/"]` | get | CAPI's health probe does `GET /` before establishing a ClusterCache connection. This is **not** covered by the standard `system:discovery` ClusterRole. |

These resources are deployed via the Helm chart (`capi-workload-*.yaml` templates) or the static manifests in `config/rbac/capi-workload.yaml`.

## MachinePool Integration

The operator creates a CAPI `MachinePool` for each `NstanceMachinePool`, setting:

- `spec.clusterName` — references the CAPI Cluster
- `spec.template.spec.infrastructureRef` — references the NstanceMachinePool
- `spec.template.spec.bootstrap.dataSecretName` — set to empty string (Nstance handles bootstrap independently)

Replica counts flow from the MachinePool through the NstanceMachinePool controller, which distributes them across `NstanceShardGroup` resources — one per zone shard. Each NstanceShardGroup controller calls `UpsertGroup` on its shard to reconcile server-side state.

### MachinePool Phase

CAPI's MachinePool controller computes the `phase` field using `deprecated.v1beta1.readyReplicas`, which it derives by matching `spec.providerIDList` entries against cluster Nodes with a matching `node.spec.providerID`. The phase reaches `Running` only when all provider IDs resolve to Ready Nodes.

If CAPI cannot match provider IDs to Nodes, the MachinePool phase will remain `ScalingUp`. For example this happens in development because the tmux provider does actually registered Kubernetes Nodes - in this case it is cosmetic, the NstanceMachinePool and NstanceShardGroup controllers function correctly regardless of the MachinePool phase.

## Key Source Files

| File | Role |
|------|------|
| `internal/operator/leader/manager.go` | `ensureCAPICluster`, `ensureKubeconfigSecret`, `parseAPIServerEndpoint` |
| `internal/operator/controller/nstancecluster_controller.go` | NstanceCluster reconciler |
| `internal/operator/controller/nstancemachinepool_controller.go` | NstanceMachinePool reconciler, MachinePool creation |
| `api/v1beta1/nstancecluster_types.go` | NstanceCluster CRD types |
| `api/v1beta1/nstancemachinepool_types.go` | NstanceMachinePool CRD types |
| `config/rbac/capi-workload.yaml` | Dev RBAC manifests for nstance-capi-workload |
| `deploy/helm/templates/capi-workload-serviceaccount.yaml` | Helm chart SA template |
| `deploy/helm/templates/capi-workload-clusterrole.yaml` | Helm chart ClusterRole template |
| `deploy/helm/templates/capi-workload-clusterrolebinding.yaml` | Helm chart ClusterRoleBinding template |
