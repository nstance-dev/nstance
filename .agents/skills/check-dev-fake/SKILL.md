---
name: check-dev-fake
description: "Checks the status and diagnoses the health of the Nstance fake dev environment (make dev-tmux-k8s). Checks dev-tmux processes, dev-k8s fake API, health endpoints, log files for errors, and inspects file-based Kubernetes resources in temp/dev-k8s/. Use when asked to check or verify fake k8s dev environment health or that dev-tmux-k8s is working as expected."
---

# Checking Dev Health (Fake K8s Environment)

Runs comprehensive diagnostics on a running Nstance dev environment (`make dev-tmux-k8s`), which uses the fake dev-k8s API server instead of a real Kubernetes cluster.

## What It Checks

1. **Processes** — nstance-server, dev-s3, dev-k8s, and nstance-operator must be running
2. **Health endpoints** — HTTP health checks for servers (8990, 9000), operator (8081), dev-s3 (8989); TCP check on dev-k8s (6443); TCP checks on gRPC ports
3. **Log files** — Scans `temp/logs/` for errors/panics/fatals in dev-s3, dev-k8s, server-1, server-2, and operator logs
4. **Dev-K8s resources** (file-based in `temp/dev-k8s/`):
   - Operator secrets (nonce, key, cert) and CA ConfigMap
   - NstanceMachinePools, NstanceShardGroups, NstanceMachines with condition checks
   - CAPI MachinePools
   - Nodes (created by tmux provider)
   - Cross-check of tmux instances vs CAPI replicas
5. **Dev-S3 storage** — CA cert/key, shard configs, instance state
6. **Operator config** — kubeconfig and shard endpoint config

## Usage

Run the diagnostic script:

```bash
./scripts/check-dev-fake.sh
```

The script exits 0 if healthy or degraded (warnings only), exits 1 if any failures are found.

### Investigating Failures

If the script reports failures or warnings, follow up:

1. **Check dev logs** for errors, panics, or unexpected behavior:
   - `temp/logs/operator.log` — operator controller reconciliation errors
   - `temp/logs/server-1.log` and `temp/logs/server-2.log` — server-side gRPC and reconciler errors
   - `temp/logs/dev-s3.log` — storage issues
   - `temp/logs/dev-k8s.log` — fake K8s API issues
   - Filter with `grep -i -E "error|warn|fail|panic"` to find relevant entries

2. **Correlate with component docs** under `docs/` for context on expected behavior:
   - `docs/components/nstance-operator.md` — operator reconciliation loops, sync, registration, CRDs
   - `docs/components/nstance-server.md` — server config, groups, gRPC services, health reports
   - `docs/components/nstance-agent.md` — agent registration, health reporting, certificate management
   - `docs/development/local-setup.md` — dev environment setup and expected state
   - any other file under `docs/` which may be relevant

3. **Inspect dev-k8s resources** directly for more detail:
   - JSON files in `temp/dev-k8s/{resource_type}/{namespace}/{name}.json`
   - e.g., `cat temp/dev-k8s/nstancemachinepools/default/nstance-test.json | jq .`

## Prerequisites

Per `../../../docs/development/local-setup.md` the user should already have running:
- `make dev-tmux-k8s` running (starts dev-s3, nstance-server, dev-k8s, and nstance-operator via overmind)

Tools required to be installed: `jq`, `curl`, `nc`, `tmux`
