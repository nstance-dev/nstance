# Nstance Operator Helm Chart

This Helm chart deploys the Nstance Kubernetes Operator with support for Cluster API integration.

## Prerequisites

- Kubernetes 1.24+
- Cluster API CRDs installed
- Helm 3.8+
- A running nstance-server deployment with at least one shard

## Installation

### Installing Only CRDs

To install only the CRDs (useful for GitOps workflows or when managing the operator separately):

```bash
helm template nstance-operator ./helm \
  --show-only templates/crds.yaml | kubectl apply -f -
```

Or install with Helm but skip all other resources:

```yaml
# crds-only.yaml
installCRDs: true
# disable everything else...
deployment:
  create: false
operatorConfigMap:
  create: false
rbac:
  create: false
serviceAccount:
  create: false
```

```bash
# This will only install CRDs
helm install nstance-crds ./helm -f crds-only.yaml
```

### Full Installation

#### 1. Create Namespace and Registration Secret

First, create the namespace and generate a registration nonce using the nstance CLI:

```bash
# Create namespace
kubectl create namespace nstance-system

# Generate nonce using the nstance CLI
NONCE=$(nstance nonce --bucket my-bucket --shard us-west-2a)

# Create secret in Kubernetes
kubectl create secret generic nstance-registration \
  --namespace nstance-system \
  --from-literal=nonce="$NONCE"
```

#### 2. Configure Values

Create a `values.yaml` file with your configuration:

```yaml
# Shard endpoints (map of shard name to endpoint)
config:
  shards:
    us-east-1a: "nstance-server-us-east-1a.example.com:8443"
    us-east-1b: "nstance-server-us-east-1b.example.com:8443"

# Optional: use different namespace
namespace: nstance-system

# Optional: reference different registration secret
registrationSecret:
  name: nstance-registration
  key: nonce

# Optional: customize image
image:
  repository: ghcr.io/nstance-dev/nstance-operator
  tag: "0.1.0"

# Optional: resource limits
resources:
  limits:
    cpu: 500m
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

#### 3. Install the Chart

```bash
helm install nstance-operator ./helm \
  -f values.yaml
```

## Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Operator image repository | `ghcr.io/nstance-dev/nstance-operator` |
| `image.tag` | Operator image tag | Chart appVersion |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `replicaCount` | Number of replicas (leader election ensures only one active) | `2` |
| `namespace` | Namespace for operator resources | `nstance-system` |
| `config.shards` | Map of shard name to endpoint | `{}` |
| `registrationSecret.name` | Name of existing secret with registration nonce | `nstance-registration` |
| `registrationSecret.key` | Key in secret containing nonce | `nonce` |
| `managedSecretNames.privateKey` | Secret name for operator private key (auto-created) | `nstance-operator-key` |
| `managedSecretNames.clientCert` | Secret name for operator client certificate (auto-created) | `nstance-operator-cert` |
| `operatorConfigMap.name` | ConfigMap name for operator configuration | `nstance-operator-config` |
| `operatorConfigMap.create` | Create ConfigMap for operator configuration | `true` |
| `deployment.create` | Create operator Deployment resource | `true` |
| `leaderElection.enabled` | Enable leader election | `true` |
| `metricsAddr` | Metrics server bind address | `:8080` |
| `healthProbeAddr` | Health probe bind address | `:8081` |
| `logLevel` | Log level (debug, info, warn, error) | `info` |
| `serviceAccount.create` | Create ServiceAccount | `true` |
| `rbac.create` | Create RBAC resources | `true` |
| `resources` | Resource limits and requests | See `values.yaml` |
| `installCRDs` | Install CRDs | `true` |

## Usage

### Creating a Machine Pool

Once installed, you can create a MachinePool that will be synchronized with nstance-server groups:

```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachinePool
metadata:
  name: worker-pool
  namespace: default
spec:
  replicas: 3
  template:
    spec:
      infrastructureRef:
        kind: NstanceMachinePool
        name: worker-pool
---
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: NstanceMachinePool
metadata:
  name: worker-pool
  namespace: default
spec:
  group: workers
  instanceType: t3.medium
```

### On-Demand Nodes

Request dedicated nodes for specific pods using annotations:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-app
  annotations:
    on-demand.nstance.dev/group: "gpu-workers"
    on-demand.nstance.dev/instance-type: "g4dn.xlarge"
spec:
  containers:
  - name: app
    image: my-gpu-app:latest
```

Note that a group with key `gpu-workers` must exist on an Nstance Server for this to work.

## Uninstallation

```bash
helm uninstall nstance-operator
```

Note: CRDs are not automatically removed. To remove them:

```bash
kubectl delete crd nstancemachinepools.infrastructure.cluster.x-k8s.io
kubectl delete crd nstancemachines.infrastructure.cluster.x-k8s.io
kubectl delete crd nstancemachinetemplates.infrastructure.cluster.x-k8s.io
```

## Troubleshooting

### Check Operator Logs

```bash
kubectl logs -n nstance-system -l app.kubernetes.io/name=nstance-operator
```

### Verify Registration

Check if operator successfully registered by looking for the certificate secret:

```bash
# Check for operator certificate (created after successful registration)
kubectl get secret -n nstance-system nstance-operator-cert

# If it exists, registration was successful
# The operator also creates nstance-operator-key for its private key
kubectl get secret -n nstance-system nstance-operator-key
```

### Check Shard Connections

Look for connection logs:

```bash
kubectl logs -n nstance-system -l app.kubernetes.io/name=nstance-operator | grep "connected to shard"
```
