# Nstance Operator Helm Chart

This chart installs the Nstance Kubernetes operator, its CRDs, RBAC, metrics
Service, and validating webhook.

## Prerequisites

- Kubernetes 1.24+
- Helm 3.8+
- cert-manager (for the operator validating webhook)
- Cluster API core CRDs, controllers, and webhooks
- Reachable nstance-server registration and operator endpoints
- A cluster CA certificate and an operator registration nonce

## Full installation

Create the namespace and bootstrap resources. The key names are fixed by the
operator and must be `ca.crt` and `nonce.jwt`:

```bash
kubectl create namespace nstance-system
kubectl create configmap nstance-cluster-ca \
  --namespace nstance-system \
  --from-file=ca.crt=./cluster-ca.crt
kubectl create secret generic nstance-operator-nonce \
  --namespace nstance-system \
  --from-file=nonce.jwt=./nonce.jwt
```

Generate `nonce.jwt` with `nstance-admin cluster nonce --expiry="3h"`.

Create an operator values file:

```yaml
config:
  clusterID: example-cluster
  tenant: default
  shards:
    us-east-1a:
      registration_addr: "nstance-server.example.com:8992"
      operator_addr: "nstance-server.example.com:8993"
```

Install into the same namespace as the bootstrap resources:

```bash
helm install nstance-operator ./deploy/helm \
  --namespace nstance-system \
  --create-namespace \
  --values operator-values.yaml
```

The chart can manage the bootstrap resources instead, although putting secret
material in a Helm values file may be inappropriate for some environments:

```yaml
clusterCA:
  enabled: true
  certificate: |
    -----BEGIN CERTIFICATE-----
    ...
    -----END CERTIFICATE-----
registrationSecret:
  enabled: true
  nonceJWT: "..."
```

After successful registration, the nonce is no longer used. The operator
creates and maintains its key and client-certificate Secrets.

Image and resource settings can be customized in the same values file:

```yaml
image:
  repository: ghcr.io/nstance-dev/nstance-operator
  tag: "1.0.0"
resources:
  limits:
    cpu: 500m
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

## Installing only CRDs

CRDs are regular chart templates controlled by `installCRDs`; this chart does
not use Helm's special `crds/` directory. There are two supported approaches.

For GitOps workflows, render only the CRD template:

```bash
helm template nstance-operator ./deploy/helm \
  --show-only templates/crds.yaml \
  --set deployment.enabled=false \
  --set operatorConfigMap.enabled=false | kubectl apply -f -
```

Alternatively, install a CRD-only Helm release:

```yaml
installCRDs: true
deployment:
  enabled: false
operatorConfigMap:
  enabled: false
serviceAccount:
  enabled: false
rbac:
  enabled: false
capi:
  serviceAccount:
    enabled: false
webhooks:
  enabled: false
```

```bash
helm install nstance-crds ./deploy/helm --values crds-only.yaml
```

Set `installCRDs: false` in the operator release when CRDs have a separate
owner.

## External workload cluster

For a separate workload cluster, set its API endpoint and disable the local
CAPI workload ServiceAccount:

```yaml
capi:
  endpoint: "https://workload-api.example.com:6443"
  serviceAccount:
    enabled: false
```

Pre-provision `<clusterID>--<tenant>-kubeconfig` in the operator namespace with
the kubeconfig stored under the `value` key.

## Existing resources

- `operatorConfigMap.enabled: false` uses the ConfigMap named by
  `operatorConfigMap.name` instead of creating it.
- `serviceAccount.enabled: false` uses `serviceAccount.name` (or `default`).
- `rbac.enabled: false` expects all operator and CAPI workload RBAC to exist.
- `capi.serviceAccount.enabled: false` uses `capi.serviceAccount.name` instead
  of creating it.
- `webhooks.enabled: false` disables the operator webhook and its resources.

## Important values

| Value | Description | Default |
|---|---|---|
| `image.repository` | Operator image repository | `ghcr.io/nstance-dev/nstance-operator` |
| `image.tag` | Operator image tag | Chart appVersion |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `replicaCount` | Operator replicas | `2` |
| `installCRDs` | Render the five Nstance CRDs | `true` |
| `config.clusterID` | Required Nstance cluster ID | `""` |
| `config.tenant` | Required tenant ID | `""` |
| `config.shards` | Required shard endpoint map | `{}` |
| `clusterCA.name` | Existing or managed CA ConfigMap | `nstance-cluster-ca` |
| `registrationSecret.name` | Existing or managed nonce Secret | `nstance-operator-nonce` |
| `managedSecretNames.privateKey` | Auto-managed operator key Secret | `nstance-operator-key` |
| `managedSecretNames.clientCert` | Auto-managed client certificate Secret | `nstance-operator-cert` |
| `operatorConfigMap.enabled` | Create the operator ConfigMap | `true` |
| `deployment.enabled` | Create the operator Deployment | `true` |
| `leaderElection.enabled` | Enable leader election | `true` |
| `metrics.enabled` | Enable the metrics endpoint | `true` |
| `metrics.service.enabled` | Create the metrics Service | `true` |
| `healthProbe.port` | Health and readiness port | `8081` |
| `webhooks.enabled` | Install and enable the validating webhook | `true` |
| `logLevel` | Log level (`debug`, `info`, `warn`, or `error`) | `info` |
| `serviceAccount.enabled` | Create the operator ServiceAccount | `true` |
| `rbac.enabled` | Create operator and CAPI workload RBAC | `true` |
| `capi.endpoint` | External workload API endpoint | `""` |
| `kubernetesJSON` | Use JSON for Kubernetes API calls | `false` |
| `resources` | Resource requests and limits | See `values.yaml` |

See `values.yaml` for image, resources, scheduling, security, and naming values.

## Usage

### On-demand nodes

Request a dedicated node for a Pod using Nstance annotations:

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

The referenced group must exist on an Nstance Server.

## Troubleshooting

### Check operator logs

```bash
kubectl logs -n nstance-system -l app.kubernetes.io/name=nstance-operator
```

### Verify registration

Successful registration creates the operator key and certificate Secrets:

```bash
kubectl get secret -n nstance-system nstance-operator-key nstance-operator-cert
```

### Check shard connections

```bash
kubectl logs -n nstance-system -l app.kubernetes.io/name=nstance-operator | \
  grep "connected to shard"
```

### Check synchronized resources

```bash
kubectl get clusters,nstanceclusters,machinepools,nstancemachinepools \
  -n nstance-system
```

## Uninstallation

```bash
helm uninstall nstance-operator --namespace nstance-system
```

CRDs carry `helm.sh/resource-policy: keep`, so uninstalling a release does not
delete custom resources. Remove them explicitly only when their data is no
longer needed:

```bash
kubectl delete crd \
  nstanceclusters.infrastructure.cluster.x-k8s.io \
  nstancemachinepools.infrastructure.cluster.x-k8s.io \
  nstancemachines.infrastructure.cluster.x-k8s.io \
  nstancemachinetemplates.infrastructure.cluster.x-k8s.io \
  nstanceshardgroups.infrastructure.cluster.x-k8s.io
```
