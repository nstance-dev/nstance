#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

# Shared helper functions for e2e test scripts.
# Source this file: source "$(dirname "${BASH_SOURCE[0]}")/test-helpers.sh"

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEV_K8S_DIR="${ROOT_DIR}/temp/dev-k8s"
DEV_S3_DIR="${ROOT_DIR}/temp/dev-s3"

# Polls a condition until true or timeout, printing success/failure
wait_for() {
    local desc="$1" timeout="$2" check_fn="$3"
    if eval "$check_fn"; then
        echo "✓ ${desc}"; return 0
    fi
    echo -n "Waiting for ${desc}..."
    for i in $(seq 1 "$timeout"); do
        sleep 1
        if eval "$check_fn"; then
            echo ""; echo "✓ ${desc}"; return 0
        fi
        [ "$i" -eq "$timeout" ] && { echo ""; echo "Error: ${desc} after ${timeout}s"; exit 1; }
        echo -n "."
    done
}

# Counts instances for a group, optionally filtering to on-demand only
count_instances() {
    local group="$1" on_demand_only="${2:-false}"
    local count=0
    shopt -s nullglob
    for f in "${DEV_S3_DIR}/instance/"*/*.json; do
        local g s od
        g=$(jq -r '.group // empty' "$f" 2>/dev/null)
        s=$(jq -r '.status // empty' "$f" 2>/dev/null)
        od=$(jq -r '.on_demand // false' "$f" 2>/dev/null)
        [ "$g" = "$group" ] && [ "$s" != "deleting" ] || continue
        [ "$on_demand_only" = "true" ] && [ "$od" != "true" ] && continue
        count=$((count + 1))
    done
    echo "$count"
}

# Updates MachinePool replica count
set_replicas() {
    local pool="$1" replicas="$2"
    local file="${DEV_K8S_DIR}/machinepools/default/${pool}.json"
    [ -f "$file" ] || return 1
    jq ".spec.replicas = $replicas" "$file" > "${file}.tmp" && mv "${file}.tmp" "$file"
}

# Sets deletionTimestamp on a K8s resource to trigger finalizer
mark_for_deletion() {
    local resource="$1" ns="$2" name="$3"
    local file="${DEV_K8S_DIR}/${resource}/${ns}/${name}.json"
    [ -f "$file" ] || return 0
    local ts
    ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    jq --arg ts "$ts" '.metadata.deletionTimestamp = $ts' "$file" > "${file}.tmp" && mv "${file}.tmp" "$file"
}

# Kills tmux agent windows matching a prefix
kill_agent_windows() {
    local prefix="$1"
    for session in $(tmux list-sessions -F "#{session_name}" 2>/dev/null | grep "^nstance-.*-agents$"); do
        tmux list-windows -t "$session" -F "#{window_name}" 2>/dev/null | grep "^${prefix}" | while read -r w; do
            tmux kill-window -t "$session:$w" 2>/dev/null || true
        done
    done
}

# Checks if a K8s resource JSON file exists
resource_exists() {
    [ -f "${DEV_K8S_DIR}/$1/$2/$3.json" ]
}

# Checks if any nodes exist
nodes_exist() {
    [ -n "$(ls -A "${DEV_K8S_DIR}/nodes/" 2>/dev/null)" ]
}

# Checks that required commands are installed
check_deps() {
    for cmd in "$@"; do
        command -v "$cmd" >/dev/null 2>&1 || { echo "Error: $cmd is not installed"; exit 1; }
    done
    echo "✓ Dependencies installed: $*"
}

# Checks that dev environment is running with required services
require_dev_env() {
    local services="${1:-s3 server k8s operator}"
    
    [ -S "${ROOT_DIR}/.overmind.sock" ] || { echo "Error: Dev environment not running - run 'make dev' first"; exit 1; }
    
    OVERMIND_STATUS=$(overmind status -s "${ROOT_DIR}/.overmind.sock" 2>/dev/null)
    for svc in $services; do
        echo "${OVERMIND_STATUS}" | grep -qE "^${svc}(#[0-9]+)?[[:space:]]+[0-9]+[[:space:]]+running" \
            || { echo "Error: Required service '${svc}' is not running"; exit 1; }
    done
    
    SERVER_COUNT=$(echo "${OVERMIND_STATUS}" | grep -cE "^server#[0-9]+[[:space:]]+[0-9]+[[:space:]]+running" || true)
    [ "${SERVER_COUNT}" -ge 2 ] || { echo "Error: Need at least 2 server shards (found ${SERVER_COUNT})"; exit 1; }
    
    echo "✓ Dev environment is running (${SERVER_COUNT} server shards)"
}
