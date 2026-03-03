---
name: check-dev-kind
description: "Checks the status and diagnoses the health of the Nstance local dev environment when running kind and the nstance-operator. Checks dev-tmux and dev-operator logs for errors, verifies health endpoints, and inspects Kubernetes resources via kubectl with the kind-nstance-dev context. Use when asked to check or verify operator health/logs in dev or that it's working as expected."
---

# Checking Dev Health (Kind Environment)

Runs comprehensive diagnostics on a running Nstance dev environment (`make dev-tmux` + `make dev-operator` against a Kind cluster).

## What It Checks

1. **Processes** — nstance-server and nstance-operator must be running
2. **Health endpoints** — HTTP health checks for servers (8990, 9000), operator (8081), dev-s3 (8989); TCP checks on gRPC ports
3. **Log files** — Scans `temp/logs/` for errors/panics/fatals in dev-s3, server-1, server-2, and operator logs
4. **Kubernetes resources** (via `kubectl --context kind-nstance-dev`):
   - Nodes (Kind control-plane)
   - cert-manager pods
   - CAPI CRDs and capi-system pods
   - Nstance CRDs installed
   - Operator secrets (nonce, key, cert) and CA ConfigMap
   - NstanceMachinePools, NstanceShardGroups, NstanceMachines with condition checks
   - CAPI MachinePools
   - Recent warning events
5. **Dev-S3 storage** — CA cert/key, shard configs, instance state
6. **Operator config** — kubeconfig and shard endpoint config

## Usage

Run the diagnostic script:

```bash
./scripts/check-dev-kind.sh
```

The script exits 0 if healthy or degraded (warnings only), exits 1 if any failures are found.

### Investigating Failures

If the script reports failures or warnings, follow up:

1. **Check dev logs** for errors, panics, or unexpected behavior:
   - `temp/logs/operator.log` — operator controller reconciliation errors
   - `temp/logs/server-1.log` and `temp/logs/server-2.log` — server-side gRPC and reconciler errors
   - `temp/logs/dev-s3.log` — storage issues
   - Filter with `grep -i -E "error|warn|fail|panic"` to find relevant entries

2. **Correlate with component docs** under `docs/` for context on expected behavior:
   - `docs/components/nstance-operator.md` — operator reconciliation loops, sync, registration, CRDs
   - `docs/components/nstance-server.md` — server config, groups, gRPC services, health reports
   - `docs/components/nstance-agent.md` — agent registration, health reporting, certificate management
   - `docs/development/dev-with-kind.md` — dev environment setup and expected state
   - any other file under `docs/` which may be relevant

3. **Inspect Kubernetes resources** directly for more detail:
   - `kubectl --context kind-nstance-dev describe nstancemachinepool <name>` — check conditions and events
   - `kubectl --context kind-nstance-dev describe nstanceshardgroup <name>` — check per-shard sync status
   - `kubectl --context kind-nstance-dev get events --sort-by=.lastTimestamp` — recent cluster events

## Prerequisites

Per `../../../docs/development/dev-with-kind.md` the user should already have running:
- `make dev-tmux` running (starts dev-s3 + nstance-server via overmind)
- Kind cluster `nstance-dev` running with CAPI + cert-manager + Nstance CRDs installed
- `make dev-operator` running (operator with air against Kind kubeconfig)

Tools required to be installed: `kubectl`, `jq`, `curl`, `nc`, `tmux`
