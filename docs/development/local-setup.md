---
title: "Local Development Setup"
weight: 10
description: "Setting up and running the full Nstance development environment locally with simulated infrastructure."
---

# Nstance Development Environment

This document describes the local development environment for Nstance, which simulates the full production architecture without requiring cloud infrastructure or real object storage servers / Kubernetes clusters.

There's also [Development with Kind](./dev-with-kind.md) which explains how to run a dev environment using Kind for testing nstance-operator with a real Kubernetes cluster instead the mock dev-k8s server used in this document.

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                           Development Environment                            │
│                                                                              │
│  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐     ┌─────────────┐ │
│  │   dev-s3    │     │   server    │     │   dev-k8s   │     │  operator   │ │
│  │  (gofakes3) │     │  (nstance-  │     │  (fake k8s  │     │  (nstance-  │ │
│  │   :8989     │◄────│   server)   │────►│    API)     │◄────│  operator)  │ │
│  │             │     │             │     │   :6443     │     │             │ │
│  └─────────────┘     └──────┬──────┘     └──────┬──────┘     └─────────────┘ │
│                             │                   │                            │
│                             │                   │                            │
│                      ┌──────▼──────┐     ┌──────▼──────┐                     │
│                      │    tmux     │     │    Nodes    │                     │
│                      │  session:   │     │   (fake)    │                     │
│                      │  nstance-   │────►│  created by │                     │
│                      │ dev-agents  │     │tmux provider│                     │
│                      │             │     │             │                     │
│                      │ ┌─────────┐ │     └─────────────┘                     │
│                      │ │ agent 1 │ │                                         │
│                      │ └─────────┘ │                                         │
│                      │ ┌─────────┐ │                                         │
│                      │ │ agent 2 │ │                                         │
│                      │ └─────────┘ │                                         │
│                      └─────────────┘                                         │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

## Components

### 1. dev-s3 (Fake S3 Server)

A fake S3 server using [gofakes3](https://github.com/johannesboyne/gofakes3) that stores files on the local filesystem.

- **Port:** 8989
- **Storage:** `temp/dev-s3/`
- **Bucket:** `dev`

Used by nstance-server to store:
- Configuration files
- Secrets and encryption keys
- CA certificates and keys
- Instance state

### 2. nstance-server

The main Nstance control plane server running with the **tmux provider**.

**Ports:**
| Port | Service | Description |
|------|---------|-------------|
| 8990 | Health | HTTP health checks |
| 8991 | Leader | Leader election health |
| 8992 | Registration | gRPC - Agent/Operator registration (TLS, no client cert) |
| 8993 | Operator | gRPC - Operator sync/drain (mTLS) |
| 8994 | Agent | gRPC - Agent communication (mTLS) |

**Tmux Provider Behavior:**
- Creates agents as tmux windows (running nstance-agent processes directly) instead of cloud VMs
- Runs agents via Air for hot-reload of agent code
- Creates fake Kubernetes Node resources as JSON files in dev-k8s temp dir when instances are created
- Cleans up Node resource JSON files when instances are deleted

### 3. dev-k8s (Fake Kubernetes API)

A minimal fake Kubernetes API server that stores/reads resources to/from JSON files.

- **Port:** 6443
- **Storage:** `temp/dev-k8s/`

**Supported Resources:**
- Core API (`/api/v1`): Secrets, ConfigMaps, Namespaces, Nodes, Pods
- Nstance CRDs (`infrastructure.cluster.x-k8s.io/v1beta1`): NstanceCluster, NstanceMachine, NstanceMachinePool, NstanceMachineTemplate
- Cluster API (`cluster.x-k8s.io/v1beta2`): Cluster, Machine, MachinePool
- Coordination (`coordination.k8s.io/v1`): Lease

**How it works:**
- Resources are stored as JSON files in `temp/dev-k8s/{resource}/{namespace}/{name}.json`
- Cluster-scoped resources (Nodes) are stored in `temp/dev-k8s/{resource}/{name}.json`
- No schema validation - accepts any valid JSON
- Supports watch via file system notifications (fsnotify) so you can change resources simply by editing the respective JSON file

### 4. nstance-operator

The Kubernetes operator that syncs instance groups between nstance-server and Kubernetes.

**Configuration:**
- Uses a generated kubeconfig pointing to dev-k8s
- Connects to nstance-server on separate ports for registration (8992) and operations (8993)
- Uses JSON content type instead of protobuf (dev-k8s limitation)

**Startup Flow:**
1. Waits for nstance-server to be healthy
2. Waits for dev-k8s to be healthy
3. Creates CA ConfigMap from `temp/dev-s3/cluster/ca.crt`
4. Generates registration nonce via `nstance-admin` CLI tool and writes to a Secret
5. Creates operator config with shard endpoints
6. Starts operator with Air for hot-reload

### 5. tmux Agent Session

Agents run in a dedicated tmux session for isolation from Overmind's tmux session.

- **Session name:** `nstance-dev-agents`
- **Window naming:** `nstance-agent-{instanceID}`

Each agent window runs Air for hot-reload, enabling code changes to automatically restart agents.

## Quick Start

### Prerequisites

```bash
make setup  # Verifies dependencies (go, tmux, air, overmind, shellcheck, golangci-lint) and installs git hooks
```

### Starting the Environment

```bash
# Full stack: s3 + server + dev-k8s + operator (clean start recommended)
make clean-dev && make dev-tmux-k8s

# Server only: s3 + server (for admin CLI testing or running operator separately)
make clean-dev && make dev-tmux
```

This starts components via Overmind:
- `s3`: dev-s3 fake object storage server
- `server`: nstance-server with tmux dev provider (2 instances)
- `k8s`: dev-k8s fake Kubernetes API (`dev-tmux-k8s` only)
- `operator`: nstance-operator (`dev-tmux-k8s` only)

### Viewing Logs

Overmind shows combined logs. To view specific component logs:

```bash
# In the Overmind terminal, press:
# Ctrl+C to stop all
# Or use overmind commands:
overmind connect server    # Connect to server process
overmind connect operator  # Connect to operator process
```

### Viewing Agent Logs

Agents run in a separate tmux session:

```bash
tmux attach -t nstance-dev-agents
# Use Ctrl+B, N to switch between agent windows
# Use Ctrl+B, D to detach
```

## Directory Structure

```
temp/
├── cache/                    # nstance-server cache (SQLite, etc.)
│   └── db/
│       └── nstance.db
├── dev-k8s/                  # dev-k8s resource storage
│   ├── configmaps/
│   │   └── default/
│   │       └── nstance-cluster-ca.json
│   ├── secrets/
│   │   └── default/
│   │       ├── nstance-operator-cert.json
│   │       ├── nstance-operator-key.json
│   │       └── nstance-operator-nonce.json
│   ├── nstancemachinepools/
│   │   └── default/
│   │       └── nstance-test.json
│   ├── machinepools/
│   │   └── default/
│   │       └── nstance-test.json
│   └── nodes/
│       └── {instanceID}.json   # Created by tmux provider
├── dev-s3/                   # dev-s3 file storage (object storage bucket contents)
│   ├── ca.crt                # Cluster CA certificate
│   ├── ca.key                # Cluster CA private key (encrypted)
│   ├── config/
│   │   └── config.jsonc      # Server configuration
│   └── secret/
│       └── ...               # Encrypted secrets
└── operator/                 # Operator runtime config
    ├── config.yaml           # Shard endpoints
    └── kubeconfig            # dev-k8s kubeconfig
```

## Configuration

### Server Configuration

The server reads configuration from `examples/config-tmux.jsonc`. Key settings for dev mode:

```jsonc
{
  "server": {
    "provider": {
      "kind": "tmux",     // Uses tmux provider (local agents in tmux)
      "region": "dev",
      "zone": "deva"
    },
    "bind": {
      "health_addr": "0.0.0.0:8990",
      "election_addr": "0.0.0.0:8991",
      "registration_addr": "0.0.0.0:8992",
      "operator_addr": "0.0.0.0:8993",
      "agent_addr": "0.0.0.0:8994"
    }
  }
}
```

### Operator Configuration

Generated automatically by `scripts/dev-operator.sh`:

```yaml
cluster_id: example-cluster
tenant: default
shards:
  dev:
    registration_addr: "127.0.0.1:8992"  # For initial mTLS registration
    operator_addr: "127.0.0.1:8993"      # For ongoing sync/drain operations
```

Note: Uses `127.0.0.1` instead of `localhost` to avoid IPv6 resolution issues on macOS.

## How Registration Works

1. **Operator starts** and loads CA certificate from `nstance-cluster-ca` ConfigMap
2. **Operator generates keypair** and stores in `nstance-operator-key` Secret
3. **Operator loads nonce** from `nstance-operator-nonce` Secret
4. **Operator connects** to registration port (8992) with TLS (server auth only)
5. **Server issues certificate** signed by cluster CA
6. **Operator stores certificate** in `nstance-operator-cert` Secret
7. **Operator connects** to operator port (8993) with mTLS for sync/drain

## How Instance Creation Works

1. **Reconciler** decides to create an instance
2. **Tmux provider** creates temp directory with identity files (nonce, CA cert)
3. **Tmux provider** creates tmux window running `air -c scripts/air/agent.toml`
4. **Tmux provider** creates fake Node JSON in `temp/dev-k8s/nodes/`
5. **Agent** starts, registers with server using nonce
6. **Server** issues client certificate to agent
7. **Agent** connects to agent service (8994) with mTLS
8. **Operator** can see the Node via dev-k8s and perform drain operations

## Troubleshooting

### Operator can't connect to server

Check that you're using `127.0.0.1` instead of `localhost` in shard endpoints. macOS resolves `localhost` to IPv6 first, but the server binds to `0.0.0.0` (IPv4 only).

### Operator can't read secrets after creating them

This is a controller-runtime cache issue. The code now builds TLS config directly from PEM data instead of re-reading from Kubernetes.

### "unknown service" errors from operator

The operator might be connecting to the wrong port. Registration happens on 8992, but sync/drain operations use 8993.

### Agents not appearing

Check the tmux session:
```bash
tmux attach -t nstance-dev-agents
```

If the session doesn't exist, the tmux provider will create it on next instance creation.

### dev-k8s doesn't have a resource type

Add the resource to `cmd/dev-k8s/handle_discovery.go`. The CRUD handlers are generic and work with any resource.

## Cleaning Up

```bash
# Clean all dev state
make clean-dev

# This removes:
# - temp/ directory (all dev state)
# - Kills tmux agent session
```

# Using with a Real Kubernetes Cluster (kind)

Here we'll cover how to run the Nstance operator locally and have it connecting to a [kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker) cluster, and a local Nstance server using the `dev-tmux` provider, for fast development iteration and debugging.

### Start an Nstance Server

The first thing you'll want to do is ensure you have your `nstance-server` running with a `dev-s3`, as this will generate a fresh CA certificate, which the operator will require a copy of.

**1. Start the nstance-server** (in one terminal):
```bash
make clean-dev && make dev-tmux
```
This starts s3 + server only (no dev-k8s or operator), which is what you want since the operator will run separately against kind.

### Start a Kubernetes Cluster

**2. Create a kind configuration file** at `temp/kind-config.yaml`:

```bash
mkdir -p temp
cat > temp/kind-config.yaml << 'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
EOF
```

**3. Create and start the kind cluster**:

```bash
kind create cluster --name nstance-dev --config temp/kind-config.yaml
```

Check your current kubectl context is set to `kind-nstance-dev`:

```bash
kubectl config current-context
```

Verify cluster is running:

```bash
kubectl cluster-info
```

### Prepare the Kubernetes Cluster

**4. Install cert-manager** (required by CAPI for webhook TLS certificates):

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=120s
```

**5. Deploy the Cluster API (CAPI) components**:

The Nstance operator is a CAPI infrastructure provider and requires the core CAPI CRDs, controllers, and validation webhooks:

```bash
CAPI_VERSION=$(curl -sL https://api.github.com/repos/kubernetes-sigs/cluster-api/releases/latest | jq -r .tag_name)
curl -sL "https://github.com/kubernetes-sigs/cluster-api/releases/download/$CAPI_VERSION/core-components.yaml" \
  | sed -E 's/\$\{[A-Za-z_][A-Za-z0-9_]*:=([^}]*)\}/\1/g' \
  | kubectl apply --server-side -f -
kubectl wait --for=condition=Available deployment --all -n capi-system --timeout=120s
```

**6. Deploy the Nstance CRDs**:
```bash
kubectl apply -k config/crd/
```

**7. Deploy the Nstance Cluster CA certificate ConfigMap**

The `nstance-server` you started in step 1 will generate a new CA certificate and upload it to the `dev-s3` server, stored at `./temp/dev-s3/cluster/ca.crt`.

Let's create a new ConfigMap with it embedded, for the Nstance Operator to use:

```bash
kubectl create configmap nstance-cluster-ca \
  --from-file=ca.crt=temp/dev-s3/cluster/ca.crt
```

**8. Export the Kind kubeconfig for the Operator**:

```bash
mkdir -p temp/operator
kind get kubeconfig --name nstance-dev > temp/operator/kubeconfig
```

**9. Create the operator config file** (read from `--config` flag, not a ConfigMap):

The shard IDs and ports must match the `dev-tmux` server instances. By default, `dev-tmux` runs 2 server instances (`server=2`) with port scheme: base + (instance-1) * 10.

```bash
cat > temp/operator/config.yaml << 'EOF'
cluster_id: example-cluster
tenant: default
shards:
  dev-1:
    registration_addr: "127.0.0.1:8992"
    operator_addr: "127.0.0.1:8993"
  dev-2:
    registration_addr: "127.0.0.1:9002"
    operator_addr: "127.0.0.1:9003"
EOF
```

**10. Generate the registration nonce & store in a Kubernetes Secret**:

Compile and use the `nstance-admin` binary and run it against the `dev-s3` service (started in step 1):

```bash
make nstance-admin
NONCE_JWT=$(AWS_ACCESS_KEY_ID=dev \
AWS_SECRET_ACCESS_KEY=dev \
AWS_ENDPOINT_URL=http://localhost:8989 \
AWS_S3_USE_PATH_STYLE=true \
NSTANCE_ENCRYPTION_KEY=thisisatest32bytekey123456789012 \
./bin/nstance-admin cluster nonce \
 --cluster-id example-cluster \
 --storage-bucket dev \
 --key-provider env \
 --output \
-)
kubectl create secret generic nstance-operator-nonce \
  --from-literal=nonce.jwt="$NONCE_JWT"
```

This will generate a nonce valid for 3 hours by default (extend e.g. with `--expiry 24h` passed to the `nstance-admin cluster nonce` command above).

### Run the Operator Locally Against the Kubernetes Cluster

**11. Run the operator**:

```bash
make dev-operator
```

This runs the operator with Air for hot-reload, and configures the namespace, kubeconfig, and `nstance-operator` config file automatically. Note the differences in runtime dependencies when run locally vs in-cluster:

**namespace**: (for Secrets & ConfigMaps)
  
- in-cluster: `/var/run/secrets/kubernetes.io/serviceaccount/namespace`
- locally: `NSTANCE_NAMESPACE=default`

**kubeconfig**:
  
- in-cluster: `/var/run/secrets/kubernetes.io/serviceaccount/`.
- locally: `temp/operator/kubeconfig`

**nstance-operator config**:
  
- in-cluster: `/etc/nstance/operator/config.yaml` (Helm chart mounts from ConfigMap).
- locally: `temp/operator/config.yaml` (specified via `--config` argument).

### What Happens

The operator will:
- Connect to the Kind cluster via the kubeconfig file
- Load the cluster CA from the `nstance-cluster-ca` ConfigMap
- Load or generate an Ed25519 keypair (stored in `nstance-operator-key` Secret)
- Register with nstance-server using the nonce from `nstance-operator-nonce` Secret
- Receive and store a client certificate in `nstance-operator-cert` Secret
- Connect to the operator gRPC port (8993) with mTLS for sync/drain operations
- Reconcile `NstanceMachinePool`, `NstanceMachine`, and `NstanceShardGroup` CRDs

This setup allows for rapid development cycles without needing to rebuild container images for each change.

### Cleaning Up

```bash
kind delete cluster --name nstance-dev
```
