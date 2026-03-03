#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# Diagnostic script for the Nstance mock dev environment (dev-tmux-k8s).
# Assumes `make dev-tmux-k8s` is running locally, which starts dev-s3,
# nstance-server, dev-k8s, and nstance-operator all via Overmind.
#
# Checks:
# 1. Dev processes are running
# 2. Health endpoints are reachable
# 3. Dev-S3 storage
# 4. Operator config
# 5. Log files for errors
# 6. Dev-K8s file-based resources (temp/dev-k8s/)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR
# shellcheck source=../../check-dev-common.sh
source "${SCRIPT_DIR}/../../check-dev-common.sh"

K8S_DIR="${ROOT_DIR}/temp/dev-k8s"

# ============================================================================
# 1. Process Checks
# ============================================================================
section "Process Checks"

check_server_processes
check_process "dev-k8s" "dev-k8s"
check_process "nstance-operator" "nstance-operator.*--config"
check_overmind
check_tmux_agents

# ============================================================================
# 2. Health Endpoint Checks
# ============================================================================
section "Health Endpoints"

check_server_health_endpoints
check_health "Operator health" "http://localhost:8081/healthz"
check_health "dev-s3" "http://localhost:8989/"
check_port "dev-k8s" 6443

# ============================================================================
# 3. Dev-S3 Storage Check
# ============================================================================
section "Dev-S3 Storage"

check_dev_s3_storage

# ============================================================================
# 4. Operator Config Check
# ============================================================================
section "Operator Config"

check_operator_config

# ============================================================================
# 5. Log File Checks
# ============================================================================
section "Log Files"

check_common_logs
check_log "dev-k8s" "${LOG_DIR}/dev-k8s.log"

# ============================================================================
# 6. Dev-K8s Resource Checks (file-based, temp/dev-k8s/)
# ============================================================================
section "Dev-K8s Resources"

if [ ! -d "$K8S_DIR" ]; then
    fail "dev-k8s storage directory not found ($K8S_DIR)"
else
    pass "dev-k8s storage directory exists"

    # --- Operator Secrets & ConfigMaps ---
    echo ""
    echo "  Operator Secrets & ConfigMaps:"

    CA_CM="${K8S_DIR}/configmaps/default/nstance-cluster-ca.json"
    if [ -f "$CA_CM" ]; then
        HAS_CA=$(jq -r '.data["ca.crt"] // ""' "$CA_CM" 2>/dev/null | head -c 27)
        if [ "$HAS_CA" = "-----BEGIN CERTIFICATE-----" ]; then
            pass "ConfigMap nstance-cluster-ca — has valid ca.crt"
        else
            warn "ConfigMap nstance-cluster-ca — ca.crt may be invalid"
        fi
    else
        fail "ConfigMap nstance-cluster-ca — not found"
    fi

    NONCE_SECRET="${K8S_DIR}/secrets/default/nstance-operator-nonce.json"
    if [ -f "$NONCE_SECRET" ]; then
        HAS_NONCE=$(jq -r '.data["nonce.jwt"] // ""' "$NONCE_SECRET" 2>/dev/null)
        if [ -n "$HAS_NONCE" ]; then
            pass "Secret nstance-operator-nonce — has nonce.jwt"
        else
            warn "Secret nstance-operator-nonce — nonce.jwt key is empty"
        fi
    else
        fail "Secret nstance-operator-nonce — not found"
    fi

    if [ -f "${K8S_DIR}/secrets/default/nstance-operator-key.json" ]; then
        pass "Secret nstance-operator-key — exists (operator generated keypair)"
    else
        warn "Secret nstance-operator-key — not found (operator may not have registered yet)"
    fi

    if [ -f "${K8S_DIR}/secrets/default/nstance-operator-cert.json" ]; then
        pass "Secret nstance-operator-cert — exists (operator has registered)"
    else
        warn "Secret nstance-operator-cert — not found (operator may not have registered yet)"
    fi

    # --- NstanceMachinePools ---
    echo ""
    echo "  NstanceMachinePools:"
    POOL_DIR="${K8S_DIR}/nstancemachinepools/default"
    if [ -d "$POOL_DIR" ]; then
        POOL_COUNT=0
        for pool_file in "${POOL_DIR}"/*.json; do
            [ -f "$pool_file" ] || continue
            POOL_COUNT=$((POOL_COUNT + 1))
            pool_name=$(basename "$pool_file" .json)
            group=$(jq -r '.spec.group // "unknown"' "$pool_file" 2>/dev/null)
            ready=$(jq -r '.status.ready // false' "$pool_file" 2>/dev/null)
            if [ "$ready" = "true" ]; then
                pass "$pool_name (group=$group) — Ready"
            else
                fail "$pool_name (group=$group) — Not Ready"
                jq -r '.status.conditions // [] | .[] | "        \(.type): \(.status) — \(.message // "no message")"' "$pool_file" 2>/dev/null || true
            fi
        done
        if [ "$POOL_COUNT" -eq 0 ]; then
            warn "No NstanceMachinePools found"
        fi
    else
        warn "No NstanceMachinePools directory"
    fi

    # --- NstanceShardGroups ---
    echo ""
    echo "  NstanceShardGroups:"
    SG_DIR="${K8S_DIR}/nstanceshardgroups/default"
    if [ -d "$SG_DIR" ]; then
        SG_COUNT=0
        for sg_file in "${SG_DIR}"/*.json; do
            [ -f "$sg_file" ] || continue
            SG_COUNT=$((SG_COUNT + 1))
            sg_name=$(basename "$sg_file" .json)
            group=$(jq -r '.spec.group // "unknown"' "$sg_file" 2>/dev/null)
            shard=$(jq -r '.spec.shard // "unknown"' "$sg_file" 2>/dev/null)
            size=$(jq -r '.spec.size // 0' "$sg_file" 2>/dev/null)
            ready=$(jq -r '.status.conditions // [] | map(select(.type == "Ready")) | .[0].status // "Unknown"' "$sg_file" 2>/dev/null)
            bad_conditions=$(jq -r '.status.conditions // [] | map(select(.status != "True")) | length' "$sg_file" 2>/dev/null)
            if [ "$ready" = "True" ] && [ "$bad_conditions" -eq 0 ]; then
                pass "$sg_name (group=$group, shard=$shard, size=$size) — Ready"
            else
                warn "$sg_name (group=$group, shard=$shard, size=$size) — Ready=$ready, $bad_conditions issue(s)"
                jq -r '.status.conditions // [] | .[] | select(.status != "True") | "        \(.type): \(.status) — \(.message // "no message")"' "$sg_file" 2>/dev/null || true
            fi
        done
        if [ "$SG_COUNT" -eq 0 ]; then
            warn "No NstanceShardGroups found"
        fi
    else
        warn "No NstanceShardGroups directory"
    fi

    # --- NstanceMachines ---
    echo ""
    echo "  NstanceMachines:"
    NM_DIR="${K8S_DIR}/nstancemachines/default"
    if [ -d "$NM_DIR" ]; then
        NM_COUNT=0
        for nm_file in "${NM_DIR}"/*.json; do
            [ -f "$nm_file" ] || continue
            NM_COUNT=$((NM_COUNT + 1))
            nm_name=$(basename "$nm_file" .json)
            group=$(jq -r '.spec.group // "unknown"' "$nm_file" 2>/dev/null)
            instanceID=$(jq -r '.status.instanceID // "none"' "$nm_file" 2>/dev/null)
            ready=$(jq -r '.status.ready // false' "$nm_file" 2>/dev/null)
            if [ "$ready" = "true" ]; then
                pass "$nm_name (group=$group, instance=$instanceID) — Ready"
            else
                fail "$nm_name (group=$group, instance=$instanceID) — Not Ready"
            fi
        done
        if [ "$NM_COUNT" -eq 0 ]; then
            pass "No NstanceMachines (created on-demand)"
        fi
    else
        pass "No NstanceMachines (created on-demand)"
    fi

    # --- CAPI MachinePools ---
    echo ""
    echo "  CAPI MachinePools:"
    MP_DIR="${K8S_DIR}/machinepools/default"
    if [ -d "$MP_DIR" ]; then
        CAPI_POOL_COUNT=0
        ACTUAL_TOTAL=0
        for mp_file in "${MP_DIR}"/*.json; do
            [ -f "$mp_file" ] || continue
            CAPI_POOL_COUNT=$((CAPI_POOL_COUNT + 1))
            mp_name=$(basename "$mp_file" .json)
            desired=$(jq -r '.spec.replicas // 0' "$mp_file" 2>/dev/null)
            actual=$(jq -r '.status.replicas // 0' "$mp_file" 2>/dev/null)
            ready=$(jq -r '.status.readyReplicas // 0' "$mp_file" 2>/dev/null)
            phase=$(jq -r '.status.phase // "Unknown"' "$mp_file" 2>/dev/null)
            ACTUAL_TOTAL=$((ACTUAL_TOTAL + actual))
            if [ "$desired" = "$ready" ] 2>/dev/null; then
                pass "$mp_name (desired=$desired, actual=$actual, ready=$ready, phase=$phase)"
            else
                warn "$mp_name (desired=$desired, actual=$actual, ready=$ready, phase=$phase)"
            fi
        done
        if [ "$CAPI_POOL_COUNT" -eq 0 ]; then
            warn "No CAPI MachinePools found"
        fi

        # Cross-check: tmux instances vs actual replicas
        if [ "$TOTAL_INSTANCES" -gt 0 ] || [ "$CAPI_POOL_COUNT" -gt 0 ]; then
            if [ "$TOTAL_INSTANCES" -eq "$ACTUAL_TOTAL" ]; then
                pass "Instance count matches: $TOTAL_INSTANCES tmux instance(s) = $ACTUAL_TOTAL actual replica(s)"
            else
                warn "Instance count mismatch: $TOTAL_INSTANCES tmux instance(s) vs $ACTUAL_TOTAL actual replica(s)"
            fi
        fi
    else
        warn "No CAPI MachinePools directory"
    fi

    # --- Nodes (created by tmux provider) ---
    echo ""
    echo "  Nodes:"
    NODE_DIR="${K8S_DIR}/nodes"
    if [ -d "$NODE_DIR" ]; then
        NODE_COUNT=0
        for node_file in "${NODE_DIR}"/*.json; do
            [ -f "$node_file" ] || continue
            NODE_COUNT=$((NODE_COUNT + 1))
            node_name=$(basename "$node_file" .json)
            ready=$(jq -r '.status.conditions // [] | map(select(.type=="Ready")) | .[0].status // "Unknown"' "$node_file" 2>/dev/null)
            if [ "$ready" = "True" ]; then
                pass "Node $node_name — Ready"
            else
                warn "Node $node_name — Ready=$ready"
            fi
        done
        if [ "$NODE_COUNT" -eq 0 ]; then
            pass "No nodes (created when instances are provisioned)"
        fi
    else
        pass "No nodes directory (created when instances are provisioned)"
    fi
fi

# ============================================================================
# Summary
# ============================================================================
print_summary
