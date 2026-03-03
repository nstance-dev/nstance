# Nstance Operator Kubernetes Manifests

This directory contains auto-generated Kubernetes manifests for the Nstance Operator.

## Generation

All manifests are generated from Go code annotations using `controller-gen`:

```bash
make manifests
```

This generates:
- **CRD manifests** in `crd/bases/` from `api/v1beta1/*_types.go`
- **RBAC manifests** in `rbac/` from `+kubebuilder:rbac` markers in controllers

## Directory Structure

```
config/
├── crd/
│   └── bases/
│       ├── infrastructure.cluster.x-k8s.io_nstancemachinepools.yaml
│       ├── infrastructure.cluster.x-k8s.io_nstancemachines.yaml
│       └── infrastructure.cluster.x-k8s.io_nstancemachinetemplates.yaml
└── rbac/
    └── role.yaml
```

## API Group

Nstance uses `infrastructure.cluster.x-k8s.io` as the API group, per official CAPI documentatio:

> "The domain for Cluster API resources is cluster.x-k8s.io, and infrastructure providers generally use infrastructure.cluster.x-k8s.io."  
> — [CAPI Developer Guide](https://cluster-api.sigs.k8s.io/developer/providers/getting-started/initialize-repo-and-api-types)

This is the standard for all CAPI infrastructure providers (both official and third-party) and ensures:
- ✅ **Compatibility** - CAPI core controllers can automatically discover and manage our resources
- ✅ **No extra RBAC** - Works out-of-box without custom aggregation rules
- ✅ **Ecosystem alignment** - Follows the same pattern as Tinkerbell, Metal3, etc.
- ✅ **Tooling support** - Seamless integration with `clusterctl` and CAPI ecosystem

## Custom Resource Definitions (CRDs)

### NstanceMachinePool
Infrastructure provider for CAPI MachinePools. Maps to Nstance Groups with dynamic sizing.

### NstanceMachine  
Infrastructure provider for CAPI Machines. Maps to individual Nstance VM instances (use for On-Demand Instances functionality).

### NstanceMachineTemplate
Immutable template pattern for stamping out Machine → NstanceMachine pairs.

## RBAC

The operator requires cluster-wide permissions to:
- Manage all three Nstance CRDs
- Update status and finalizers on CRDs
- Cordon and drain Kubernetes Nodes (for drain coordination)

## Development

After modifying CRD types or adding RBAC markers to controllers, regenerate manifests:

```bash
make manifests
```

Never edit the generated YAML files directly - they will be overwritten.
