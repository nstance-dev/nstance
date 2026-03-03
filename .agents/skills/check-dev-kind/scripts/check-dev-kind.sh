#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# Diagnostic script for the Nstance Operator dev environment with kind.
# Assumes `make dev-tmux` and `make dev-operator` are running locally
# with a Kind cluster (kind-nstance-dev context).
#
# Checks:
# 1. Dev processes are running
# 2. Health endpoints are reachable
# 3. Dev-S3 storage
# 4. Operator config
# 5. Log files for errors
# 6. Kubernetes resources via kubectl (kind-nstance-dev context)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR
# shellcheck source=../../check-dev-common.sh
source "${SCRIPT_DIR}/../../check-dev-common.sh"

KUBECONTEXT="kind-nstance-dev"

# ============================================================================
# 1. Process Checks
# ============================================================================
section "Process Checks"

check_server_processes
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

# ============================================================================
# 6. Kubernetes Resource Checks (via kubectl with kind-nstance-dev context)
# ============================================================================
section "Kubernetes Cluster"

if ! command -v kubectl >/dev/null 2>&1; then
    fail "kubectl not found in PATH"
elif ! kubectl --context "$KUBECONTEXT" cluster-info >/dev/null 2>&1; then
    fail "Cannot connect to kind-nstance-dev cluster (is Kind running?)"
else
    pass "Connected to kind-nstance-dev cluster"

    # --- Nodes ---
    echo ""
    echo "  Nodes:"
    NODES=$(kubectl --context "$KUBECONTEXT" get nodes -o json 2>/dev/null || echo '{"items":[]}')
    # Separate kind control-plane nodes from nstance-managed nodes
    KIND_NODES=$(echo "$NODES" | jq '[.items[] | select(.metadata.labels["node-role.kubernetes.io/control-plane"] != null)]')
    WORKER_NODES=$(echo "$NODES" | jq '[.items[] | select(.metadata.labels["node-role.kubernetes.io/control-plane"] == null)]')
    WORKER_NODE_COUNT=$(echo "$WORKER_NODES" | jq 'length')

    echo "$KIND_NODES" | jq -r '.[] | "\(.metadata.name) \(.status.conditions // [] | map(select(.type=="Ready")) | .[0].status // "Unknown")"' | \
    while read -r nname ready; do
        if [ "$ready" = "True" ]; then
            pass "Kind node $nname — Ready"
        else
            fail "Kind node $nname — Ready=$ready"
        fi
    done

    if [ "$WORKER_NODE_COUNT" -gt 0 ]; then
        echo "$WORKER_NODES" | jq -r '.[] | "\(.metadata.name) \(.status.conditions // [] | map(select(.type=="Ready")) | .[0].status // "Unknown")"' | \
        while read -r nname ready; do
            if [ "$ready" = "True" ]; then
                pass "Worker node $nname — Ready"
            else
                warn "Worker node $nname — Ready=$ready (tmux-managed nodes may show NotReady)"
            fi
        done
    fi

    # --- cert-manager ---
    echo ""
    echo "  cert-manager:"
    CM_PODS=$(kubectl --context "$KUBECONTEXT" get pods -n cert-manager -o json 2>/dev/null || echo '{"items":[]}')
    CM_POD_COUNT=$(echo "$CM_PODS" | jq '.items | length')
    if [ "$CM_POD_COUNT" -gt 0 ]; then
        echo "$CM_PODS" | jq -r '.items[] | "\(.metadata.name) \(.status.phase) \(.status.conditions // [] | map(select(.type=="Ready")) | .[0].status // "Unknown")"' | \
        while read -r pname phase ready; do
            if [ "$phase" = "Running" ] && [ "$ready" = "True" ]; then
                pass "Pod $pname — Running/Ready"
            else
                fail "Pod $pname — $phase (Ready=$ready)"
            fi
        done
    else
        fail "No cert-manager pods found"
    fi

    # --- CAPI CRDs and System ---
    echo ""
    echo "  Cluster API CRDs:"
    for crd in clusters.cluster.x-k8s.io machines.cluster.x-k8s.io machinepools.cluster.x-k8s.io; do
        if kubectl --context "$KUBECONTEXT" get crd "$crd" >/dev/null 2>&1; then
            pass "$crd"
        else
            fail "$crd — not installed (run CAPI core-components install)"
        fi
    done

    echo ""
    echo "  CAPI System:"
    CAPI_PODS=$(kubectl --context "$KUBECONTEXT" get pods -n capi-system -o json 2>/dev/null || echo '{"items":[]}')
    CAPI_POD_COUNT=$(echo "$CAPI_PODS" | jq '.items | length')
    if [ "$CAPI_POD_COUNT" -gt 0 ]; then
        echo "$CAPI_PODS" | jq -r '.items[] | "\(.metadata.name) \(.status.phase) \(.status.conditions // [] | map(select(.type=="Ready")) | .[0].status // "Unknown")"' | \
        while read -r pname phase ready; do
            if [ "$phase" = "Running" ] && [ "$ready" = "True" ]; then
                pass "Pod $pname — Running/Ready"
            else
                fail "Pod $pname — $phase (Ready=$ready)"
            fi
        done
    else
        fail "No CAPI pods found in capi-system namespace"
    fi

    # --- Nstance CRDs ---
    echo ""
    echo "  Nstance CRDs:"
    for crd in nstanceclusters.infrastructure.cluster.x-k8s.io \
               nstancemachines.infrastructure.cluster.x-k8s.io \
               nstancemachinepools.infrastructure.cluster.x-k8s.io \
               nstancemachinetemplates.infrastructure.cluster.x-k8s.io \
               nstanceshardgroups.infrastructure.cluster.x-k8s.io; do
        if kubectl --context "$KUBECONTEXT" get crd "$crd" >/dev/null 2>&1; then
            pass "$crd"
        else
            fail "$crd — not installed"
        fi
    done

    # --- Operator Secrets & ConfigMaps ---
    echo ""
    echo "  Operator Secrets & ConfigMaps:"

    if kubectl --context "$KUBECONTEXT" get configmap nstance-cluster-ca -n default >/dev/null 2>&1; then
        # Verify it has the ca.crt key
        HAS_CA=$(kubectl --context "$KUBECONTEXT" get configmap nstance-cluster-ca -n default -o jsonpath='{.data.ca\.crt}' 2>/dev/null | head -c 27)
        if [ "$HAS_CA" = "-----BEGIN CERTIFICATE-----" ]; then
            pass "ConfigMap nstance-cluster-ca — has valid ca.crt"
        else
            warn "ConfigMap nstance-cluster-ca — ca.crt may be invalid"
        fi
    else
        fail "ConfigMap nstance-cluster-ca — not found"
    fi

    if kubectl --context "$KUBECONTEXT" get secret nstance-operator-nonce -n default >/dev/null 2>&1; then
        HAS_NONCE=$(kubectl --context "$KUBECONTEXT" get secret nstance-operator-nonce -n default -o jsonpath='{.data.nonce\.jwt}' 2>/dev/null)
        if [ -n "$HAS_NONCE" ]; then
            pass "Secret nstance-operator-nonce — has nonce.jwt"
        else
            warn "Secret nstance-operator-nonce — nonce.jwt key is empty"
        fi
    else
        fail "Secret nstance-operator-nonce — not found"
    fi

    if kubectl --context "$KUBECONTEXT" get secret nstance-operator-key -n default >/dev/null 2>&1; then
        pass "Secret nstance-operator-key — exists (operator generated keypair)"
    else
        warn "Secret nstance-operator-key — not found (operator may not have registered yet)"
    fi

    if kubectl --context "$KUBECONTEXT" get secret nstance-operator-cert -n default >/dev/null 2>&1; then
        pass "Secret nstance-operator-cert — exists (operator has registered)"
    else
        warn "Secret nstance-operator-cert — not found (operator may not have registered yet)"
    fi

    # --- CAPI ServiceAccount & RBAC ---
    echo ""
    echo "  CAPI ServiceAccount & RBAC:"

    if kubectl --context "$KUBECONTEXT" get serviceaccount nstance-capi-workload -n default >/dev/null 2>&1; then
        pass "ServiceAccount nstance-capi-workload — exists"
    else
        fail "ServiceAccount nstance-capi-workload — not found (run make kind-provision)"
    fi

    if kubectl --context "$KUBECONTEXT" get clusterrole nstance-capi-workload >/dev/null 2>&1; then
        pass "ClusterRole nstance-capi-workload — exists"
    else
        fail "ClusterRole nstance-capi-workload — not found (run make kind-provision)"
    fi

    if kubectl --context "$KUBECONTEXT" get clusterrolebinding nstance-capi-workload >/dev/null 2>&1; then
        pass "ClusterRoleBinding nstance-capi-workload — exists"
    else
        fail "ClusterRoleBinding nstance-capi-workload — not found (run make kind-provision)"
    fi

    # Check kubeconfig secret token expiry
    CLUSTER_NAME=$(grep 'cluster_id' "${ROOT_DIR}/temp/operator/config.yaml" 2>/dev/null | awk '{print $2}' || true)
    TENANT=$(grep 'tenant' "${ROOT_DIR}/temp/operator/config.yaml" 2>/dev/null | awk '{print $2}' || true)
    if [ -n "$CLUSTER_NAME" ] && [ -n "$TENANT" ]; then
        KC_SECRET="${CLUSTER_NAME}--${TENANT}-kubeconfig"
        if kubectl --context "$KUBECONTEXT" get secret "$KC_SECRET" -n default >/dev/null 2>&1; then
            EXPIRY=$(kubectl --context "$KUBECONTEXT" get secret "$KC_SECRET" -n default -o jsonpath='{.metadata.annotations.nstance\.dev/token-expiry}' 2>/dev/null || true)
            if [ -n "$EXPIRY" ]; then
                # Strip colon from timezone offset for macOS date compatibility (+11:00 → +1100)
                # shellcheck disable=SC2001
                EXPIRY_CLEAN=$(echo "$EXPIRY" | sed 's/\([+-][0-9][0-9]\):\([0-9][0-9]\)$/\1\2/')
                EXPIRY_EPOCH=$(date -jf "%Y-%m-%dT%H:%M:%S%z" "$EXPIRY_CLEAN" +%s 2>/dev/null || date -d "$EXPIRY" +%s 2>/dev/null || echo "0")
                NOW_EPOCH=$(date +%s)
                if [ "$EXPIRY_EPOCH" -gt "$NOW_EPOCH" ]; then
                    REMAINING=$(( (EXPIRY_EPOCH - NOW_EPOCH) / 60 ))
                    pass "Secret $KC_SECRET — token expires in ${REMAINING}m"
                else
                    warn "Secret $KC_SECRET — token expired (will refresh on next sync)"
                fi
            else
                warn "Secret $KC_SECRET — missing token-expiry annotation"
            fi
        else
            warn "Secret $KC_SECRET — not found (operator may not have created CAPI cluster yet)"
        fi
    fi

    # --- Nstance CRD Resources ---
    echo ""
    echo "  NstanceMachinePools:"
    POOLS=$(kubectl --context "$KUBECONTEXT" get nstancemachinepools -A -o json 2>/dev/null || echo '{"items":[]}')
    POOL_COUNT=$(echo "$POOLS" | jq '.items | length')
    if [ "$POOL_COUNT" -gt 0 ]; then
        echo "$POOLS" | jq -r '.items[] | "\(.metadata.namespace)/\(.metadata.name) \(.spec.group) \(.status.ready // false) \(.status.conditions // [] | map(select(.status != "True")) | length)"' | \
        while read -r fullname group ready bad_conditions; do
            if [ "$ready" = "true" ] && [ "$bad_conditions" -eq 0 ]; then
                pass "$fullname (group=$group) — Ready"
            elif [ "$ready" = "true" ]; then
                warn "$fullname (group=$group) — Ready but $bad_conditions condition(s) not True"
            else
                fail "$fullname (group=$group) — Not Ready"
                # Show conditions
                echo "$POOLS" | jq -r --arg name "$(echo "$fullname" | cut -d/ -f2)" \
                    '.items[] | select(.metadata.name == $name) | .status.conditions // [] | .[] | "        \(.type): \(.status) — \(.message // "no message")"'
            fi
        done
    else
        warn "No NstanceMachinePools found"
    fi

    echo ""
    echo "  NstanceShardGroups:"
    SHARDGROUPS=$(kubectl --context "$KUBECONTEXT" get nstanceshardgroups -A -o json 2>/dev/null || echo '{"items":[]}')
    SG_COUNT=$(echo "$SHARDGROUPS" | jq '.items | length')
    if [ "$SG_COUNT" -gt 0 ]; then
        echo "$SHARDGROUPS" | jq -r '.items[] | "\(.metadata.namespace)/\(.metadata.name) \(.spec.group) \(.spec.shard) \(.spec.size) \(.status.conditions // [] | map(select(.type == "Ready")) | .[0].status // "Unknown") \(.status.conditions // [] | map(select(.status != "True")) | length)"' | \
        while read -r fullname group shard size ready bad_conditions; do
            if [ "$ready" = "True" ] && [ "$bad_conditions" -eq 0 ]; then
                pass "$fullname (group=$group, shard=$shard, size=$size) — Ready"
            else
                warn "$fullname (group=$group, shard=$shard, size=$size) — Ready=$ready, $bad_conditions issue(s)"
                echo "$SHARDGROUPS" | jq -r --arg name "$(echo "$fullname" | cut -d/ -f2)" \
                    '.items[] | select(.metadata.name == $name) | .status.conditions // [] | .[] | select(.status != "True") | "        \(.type): \(.status) — \(.message // "no message")"'
            fi
        done
    else
        warn "No NstanceShardGroups found"
    fi

    echo ""
    echo "  NstanceMachines:"
    MACHINES=$(kubectl --context "$KUBECONTEXT" get nstancemachines -A -o json 2>/dev/null || echo '{"items":[]}')
    MACHINE_COUNT=$(echo "$MACHINES" | jq '.items | length')
    if [ "$MACHINE_COUNT" -gt 0 ]; then
        echo "$MACHINES" | jq -r '.items[] | "\(.metadata.namespace)/\(.metadata.name) \(.spec.group) \(.status.instanceID // "none") \(.status.ready // false)"' | \
        while read -r fullname group instanceID ready; do
            if [ "$ready" = "true" ]; then
                pass "$fullname (group=$group, instance=$instanceID) — Ready"
            else
                fail "$fullname (group=$group, instance=$instanceID) — Not Ready"
            fi
        done
    else
        pass "No NstanceMachines (created on-demand)"
    fi

    # --- CAPI MachinePool Resources ---
    echo ""
    echo "  CAPI MachinePools:"
    CAPI_POOLS=$(kubectl --context "$KUBECONTEXT" get machinepools -A -o json 2>/dev/null || echo '{"items":[]}')
    CAPI_POOL_COUNT=$(echo "$CAPI_POOLS" | jq '.items | length')
    if [ "$CAPI_POOL_COUNT" -gt 0 ]; then
        echo "$CAPI_POOLS" | jq -r '.items[] | "\(.metadata.namespace)/\(.metadata.name) \(.spec.replicas // 0) \(.status.replicas // 0) \(.status.readyReplicas // 0) \(.status.phase // "Unknown")"' | \
        while read -r fullname desired actual ready phase; do
            if [ "$desired" = "$ready" ] 2>/dev/null; then
                pass "$fullname (desired=$desired, actual=$actual, ready=$ready, phase=$phase)"
            else
                warn "$fullname (desired=$desired, actual=$actual, ready=$ready, phase=$phase)"
            fi
        done
    else
        warn "No CAPI MachinePools found"
    fi

    # --- Cross-check: tmux instances vs actual replicas ---
    if [ "$TOTAL_INSTANCES" -gt 0 ] || [ "$CAPI_POOL_COUNT" -gt 0 ]; then
        ACTUAL_TOTAL=$(echo "$CAPI_POOLS" | jq '[.items[] | .status.replicas // 0] | add // 0')
        if [ "$TOTAL_INSTANCES" -eq "$ACTUAL_TOTAL" ]; then
            pass "Instance count matches: $TOTAL_INSTANCES tmux instance(s) = $ACTUAL_TOTAL actual replica(s)"
        else
            warn "Instance count mismatch: $TOTAL_INSTANCES tmux instance(s) vs $ACTUAL_TOTAL actual replica(s)"
        fi
    fi

    # --- Recent Events with warnings ---
    echo ""
    echo "  Recent Warning Events:"
    EVENTS=$(kubectl --context "$KUBECONTEXT" get events -A --field-selector type=Warning --sort-by=.lastTimestamp -o json 2>/dev/null || echo '{"items":[]}')
    EVENT_COUNT=$(echo "$EVENTS" | jq '.items | length')
    if [ "$EVENT_COUNT" -gt 0 ]; then
        # Show last 10 warning events
        echo "$EVENTS" | jq -r '.items | sort_by(.lastTimestamp) | reverse | .[0:10] | .[] | "      \(.lastTimestamp // .metadata.creationTimestamp) \(.involvedObject.kind)/\(.involvedObject.name): \(.reason) — \(.message)"'
        warn "$EVENT_COUNT warning event(s) found (showing last 10)"
    else
        pass "No warning events"
    fi
fi

# ============================================================================
# Summary
# ============================================================================
print_summary
