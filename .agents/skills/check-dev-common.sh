#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# Common diagnostic functions shared by check-dev-operator (kind) and
# check-dev-mock (dev-k8s) health-check scripts.
#
# Source this file from a diagnostic script after setting SCRIPT_DIR:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "${SCRIPT_DIR}/../../../check-dev-common.sh"

set -euo pipefail

# ============================================================================
# Variables (may be overridden before sourcing)
# ============================================================================
ROOT_DIR="${ROOT_DIR:-$(cd "${SCRIPT_DIR}/../../../.." && pwd)}"
LOG_DIR="${ROOT_DIR}/temp/logs"

PASS=0
WARN=0
FAIL=0
SECTIONS=""
TOTAL_INSTANCES=0

# ============================================================================
# Helper Functions
# ============================================================================

pass() { PASS=$((PASS + 1)); echo "  ✅ $*"; }
warn() { WARN=$((WARN + 1)); echo "  ⚠️  $*"; }
fail() { FAIL=$((FAIL + 1)); echo "  ❌ $*"; }

section() {
    echo ""
    echo "━━━ $* ━━━"
    SECTIONS="${SECTIONS}${1}\n"
}

check_process() {
    local name="$1" pattern="$2"
    if pgrep -f "$pattern" >/dev/null 2>&1; then
        local pids
        pids=$(pgrep -f "$pattern" | tr '\n' ',' | sed 's/,$//')
        pass "$name is running (PIDs: $pids)"
    else
        fail "$name is NOT running"
    fi
}

check_health() {
    local name="$1" url="$2"
    local status
    status=$(curl -s -o /dev/null -w "%{http_code}" "$url" 2>/dev/null || true)
    if [ "$status" = "200" ]; then
        pass "$name ($url) → HTTP $status"
    elif [ "$status" = "000" ]; then
        fail "$name ($url) → connection refused"
    else
        fail "$name ($url) → HTTP $status"
    fi
}

# Leader election health is HTTPS and returns 200 (leader) or 503 (follower) — both are valid
check_leader() {
    local name="$1" url="$2"
    local status
    status=$(curl -sk -o /dev/null -w "%{http_code}" "$url" 2>/dev/null || true)
    if [ "$status" = "200" ]; then
        pass "$name ($url) → leader"
    elif [ "$status" = "503" ]; then
        pass "$name ($url) → follower"
    elif [ "$status" = "000" ]; then
        fail "$name ($url) → connection refused"
    else
        warn "$name ($url) → HTTP $status"
    fi
}

check_port() {
    local label="$1" port="$2"
    if nc -z localhost "$port" 2>/dev/null; then
        pass "$label (port $port) → listening"
    else
        fail "$label (port $port) → not listening"
    fi
}

check_log() {
    local name="$1" logfile="$2"
    if [ ! -f "$logfile" ]; then
        warn "$name log not found ($logfile)"
        return
    fi

    local size
    size=$(wc -c < "$logfile" | tr -d ' ')
    local lines
    lines=$(wc -l < "$logfile" | tr -d ' ')

    # Search for error indicators (case-insensitive), excluding common false positives
    local errors
    errors=$(grep -inE '"level":"error"|level=error|"severity":"error"|panic:|fatal:|FATAL|PANIC' "$logfile" \
        | grep -iv 'no error\|error_count.*0\|errors=0\|without error' \
        | tail -20 || true)

    local error_count=0
    if [ -n "$errors" ]; then
        error_count=$(echo "$errors" | wc -l | tr -d ' ')
    fi

    if [ "$error_count" -gt 0 ]; then
        fail "$name ($lines lines, ${size} bytes) — $error_count error(s) found:"
        echo "$errors" | while IFS= read -r line; do
            echo "      $(echo "$line" | cut -c1-200)"
        done
    else
        pass "$name ($lines lines, ${size} bytes) — no errors"
    fi
}

# ============================================================================
# Common Check Routines
# ============================================================================

# Check core server processes (nstance-server, dev-s3)
check_server_processes() {
    check_process "nstance-server" "nstance-server.*--id dev"
    check_process "dev-s3" "dev-s3"
}

# Check overmind socket and status
check_overmind() {
    if [ -S "${ROOT_DIR}/.overmind.sock" ]; then
        OVERMIND_STATUS=$(overmind status -s "${ROOT_DIR}/.overmind.sock" 2>/dev/null || echo "")
        if [ -n "$OVERMIND_STATUS" ]; then
            SERVER_COUNT=$(echo "$OVERMIND_STATUS" | grep -cE "^server" || true)
            pass "Overmind running (${SERVER_COUNT} server process(es))"
        else
            warn "Overmind socket exists but status unavailable"
        fi
    else
        warn "Overmind socket not found (dev-tmux may not be using overmind, or running externally)"
    fi
}

# Check tmux agent sessions and count instances (sets TOTAL_INSTANCES)
check_tmux_agents() {
    AGENT_SESSIONS=$(tmux list-sessions -F "#{session_name}" 2>/dev/null | grep "^nstance-.*-agents$" || true)
    TOTAL_INSTANCES=0
    if [ -n "$AGENT_SESSIONS" ]; then
        for session in $AGENT_SESSIONS; do
            WINDOW_COUNT=$(tmux list-windows -t "$session" -F "#{window_name}" 2>/dev/null | wc -l | tr -d ' ')
            shard_instances=$(tmux list-windows -t "$session" -F "#{window_name}" 2>/dev/null | grep -c "^nstance-agent-" || true)
            TOTAL_INSTANCES=$((TOTAL_INSTANCES + shard_instances))
            pass "Tmux agent session '$session' has $WINDOW_COUNT window(s)"
        done
        pass "$TOTAL_INSTANCES active instance(s) across shards"
    else
        warn "No tmux agent sessions found (no instances may have been created yet)"
    fi
}

# Check server health endpoints by discovering ports from operator config.
# Falls back to default ports (8990-8994, 9000-9004) if config not found.
check_server_health_endpoints() {
    local op_config="${ROOT_DIR}/temp/operator/config.yaml"
    if [ -f "$op_config" ]; then
        local current_shard=""
        while IFS= read -r line; do
            if [[ "$line" =~ ^[[:space:]]+([a-z0-9-]+):$ ]]; then
                current_shard="${BASH_REMATCH[1]}"
            fi
            if [[ "$line" =~ registration_addr:[[:space:]]*\"([^\"]+)\" ]] && [ -n "$current_shard" ]; then
                local addr="${BASH_REMATCH[1]}"
                local reg_port="${addr##*:}"
                # Derive all 5 ports from registration port (health=reg-2, leader=reg-1, reg, operator=reg+1, agent=reg+2)
                local health_port=$((reg_port - 2))
                local leader_port=$((reg_port - 1))
                local op_port=$((reg_port + 1))
                local agent_port=$((reg_port + 2))

                check_health "$current_shard health" "http://localhost:${health_port}/healthz"
                check_leader "$current_shard leader" "https://localhost:${leader_port}/health/leadership/shard"
                check_port "$current_shard registration" "$reg_port"
                check_port "$current_shard operator" "$op_port"
                check_port "$current_shard agent" "$agent_port"
                current_shard=""
            fi
        done < "$op_config"
    else
        warn "Operator config not found, checking default ports (8990-8994, 9000-9004)"
        check_health "Server 1 health" "http://localhost:8990/healthz"
        check_leader "Server 1 leader" "https://localhost:8991/health/leadership/shard"
        check_port "Server 1 registration" 8992
        check_port "Server 1 operator" 8993
        check_port "Server 1 agent" 8994
        check_health "Server 2 health" "http://localhost:9000/healthz"
        check_leader "Server 2 leader" "https://localhost:9001/health/leadership/shard"
        check_port "Server 2 registration" 9002
        check_port "Server 2 operator" 9003
        check_port "Server 2 agent" 9004
    fi
}

# Check dev-s3 storage directory for expected files
check_dev_s3_storage() {
    local s3_dir="${ROOT_DIR}/temp/dev-s3"
    if [ -d "$s3_dir" ]; then
        # Cluster CA
        if [ -f "${s3_dir}/cluster/ca.crt" ]; then
            pass "Cluster CA certificate exists"
        else
            fail "Cluster CA certificate missing (${s3_dir}/cluster/ca.crt)"
        fi

        if [ -f "${s3_dir}/cluster/secret/ca.key" ]; then
            pass "Cluster CA key exists (in secrets store)"
        else
            fail "Cluster CA key missing (expected in ${s3_dir}/cluster/secret/ca.key)"
        fi

        # Shard configs
        local shard_count=0
        for shard_dir in "${s3_dir}/shard/"*/; do
            [ -d "$shard_dir" ] || continue
            shard_count=$((shard_count + 1))
            local shard_name
            shard_name=$(basename "$shard_dir")
            if [ -f "${shard_dir}config.jsonc" ]; then
                pass "Shard '$shard_name' config exists"
            else
                warn "Shard '$shard_name' config missing"
            fi
            if [ -f "${shard_dir}groups.jsonc" ]; then
                pass "Shard '$shard_name' groups config exists"
            else
                warn "Shard '$shard_name' groups config missing"
            fi
        done
        if [ "$shard_count" -eq 0 ]; then
            warn "No shard directories found in ${s3_dir}/shard/"
        fi
    else
        fail "dev-s3 storage directory not found ($s3_dir)"
    fi
}

# Check operator config (config.yaml and kubeconfig)
check_operator_config() {
    local op_dir="${ROOT_DIR}/temp/operator"
    if [ -f "${op_dir}/config.yaml" ]; then
        local shard_list
        shard_list=$(grep -E '^\s+\S+:$' "${op_dir}/config.yaml" | grep -v '^#' | sed 's/://;s/^ *//' | grep -v shards || true)
        if [ -n "$shard_list" ]; then
            pass "Operator config has shards: $(echo "$shard_list" | tr '\n' ' ')"
        else
            warn "Operator config exists but no shards found"
        fi
    else
        fail "Operator config not found (${op_dir}/config.yaml)"
    fi

    if [ -f "${op_dir}/kubeconfig" ]; then
        pass "Operator kubeconfig exists"
    else
        fail "Operator kubeconfig not found (${op_dir}/kubeconfig)"
    fi
}

# Check common log files (dev-s3, operator, server-*)
check_common_logs() {
    check_log "dev-s3" "${LOG_DIR}/dev-s3.log"
    check_log "Operator" "${LOG_DIR}/operator.log"

    for logfile in "${LOG_DIR}"/server-*.log; do
        [ -f "$logfile" ] || continue
        local name
        name=$(basename "$logfile" .log | sed 's/-/ /')
        check_log "$name" "$logfile"
    done
}

# Print summary and exit with appropriate code
print_summary() {
    echo ""
    echo "━━━ Summary ━━━"
    local total=$((PASS + WARN + FAIL))
    echo "  Total checks: $total"
    echo "  ✅ Passed: $PASS"
    echo "  ⚠️  Warnings: $WARN"
    echo "  ❌ Failed: $FAIL"

    if [ "$FAIL" -gt 0 ]; then
        echo ""
        echo "  Status: UNHEALTHY"
        exit 1
    elif [ "$WARN" -gt 0 ]; then
        echo ""
        echo "  Status: DEGRADED (warnings present)"
        exit 0
    else
        echo ""
        echo "  Status: HEALTHY"
        exit 0
    fi
}
