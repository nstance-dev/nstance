#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# Development wrapper script for nstance-server.
# This script derives per-server ports, cache directories, and tmux session names
# based on OVERMIND_PROCESS_NUM, enabling horizontal scaling of servers in dev.
#
# Each server instance runs as a separate shard (dev-1, dev-2, etc.) with its own
# config file containing adjusted ports.
#
# Port scheme (10-port gap per instance):
#   Instance 1 (shard dev-1): health=8990, leader=8991, registration=8992, operator=8993, agent=8994
#   Instance 2 (shard dev-2): health=9000, leader=9001, registration=9002, operator=9003, agent=9004
#   Instance N (shard dev-N): base + (N-1)*10
#
# Environment variables:
#   NSTANCE_DEV_CONFIG - Path to config file (default: examples/config-tmux.jsonc)

set -e

# Dev S3 configuration (local fake S3 server)
export AWS_S3_USE_PATH_STYLE=true
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1
export AWS_ENDPOINT_URL=http://localhost:8989

# Nstance encryption key (must be 32 bytes)
export NSTANCE_ENCRYPTION_KEY=thisisatest32bytekey123456789012

if ! command -v jq &> /dev/null; then
    echo "Error: jq is required but not installed. Please install it first."
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Config file (can be overridden via NSTANCE_DEV_CONFIG)
BASE_CONFIG="${NSTANCE_DEV_CONFIG:-${ROOT_DIR}/examples/config-tmux.jsonc}"

# Determine instance number using lock file (Overmind doesn't set a per-instance env var)
LOCK_DIR="${ROOT_DIR}/temp"
mkdir -p "${LOCK_DIR}"
PROC_NUM=1
while [ -f "${LOCK_DIR}/server-${PROC_NUM}.lock" ]; do
    PROC_NUM=$((PROC_NUM + 1))
done
touch "${LOCK_DIR}/server-${PROC_NUM}.lock"
trap 'rm -f "${LOCK_DIR}/server-${PROC_NUM}.lock"' EXIT

# Calculate port offset: (N-1) * 10
OFFSET=$(( (PROC_NUM - 1) * 10 ))

# Derived ports for this instance
HEALTH_PORT=$((8990 + OFFSET))
LEADER_PORT=$((8991 + OFFSET))
REGISTRATION_PORT=$((8992 + OFFSET))
OPERATOR_PORT=$((8993 + OFFSET))
AGENT_PORT=$((8994 + OFFSET))

# Per-instance shard name
SHARD_NAME="dev-${PROC_NUM}"

# Per-instance cache directory (for SQLite DB and other cache files)
export NSTANCE_DEV_CACHE_DIR="${ROOT_DIR}/temp/server-cache-${PROC_NUM}"

# Per-instance log file
LOG_DIR="${ROOT_DIR}/temp/logs"
mkdir -p "${LOG_DIR}"
LOG_FILE="${LOG_DIR}/server-${PROC_NUM}.log"

# Per-instance shard (used by scripts/air/server.toml full_bin)
export NSTANCE_DEV_SHARD="${SHARD_NAME}"

# Ensure cache directory exists
mkdir -p "${NSTANCE_DEV_CACHE_DIR}"

# Generate per-instance config and groups files
# Server loads from shard-scoped storage:
# - shard/{shard}/config.jsonc
# - shard/{shard}/groups.jsonc
SHARD_DIR="${ROOT_DIR}/temp/dev-s3/shard/${SHARD_NAME}"
mkdir -p "${SHARD_DIR}"

# Copy groups file for this shard (all shards use same groups in dev)
BASE_GROUPS="${ROOT_DIR}/examples/groups.jsonc"
cp "$BASE_GROUPS" "${SHARD_DIR}/groups.jsonc"

INSTANCE_CONFIG="${SHARD_DIR}/config.jsonc"

# Generate config with adjusted ports (strip JSONC comments first)
# Bind addrs are per-instance so we override them here.
# Advertise health/election addrs use port-only format and are resolved via --advertise-host.
# Advertise leader-service addrs (registration, operator, agent) are set explicitly.
grep -v '^\s*//' "$BASE_CONFIG" | jq \
    --arg shard "$SHARD_NAME" \
    --arg bind_health_addr "0.0.0.0:${HEALTH_PORT}" \
    --arg bind_election_addr "0.0.0.0:${LEADER_PORT}" \
    --arg bind_registration_addr "0.0.0.0:${REGISTRATION_PORT}" \
    --arg bind_operator_addr "0.0.0.0:${OPERATOR_PORT}" \
    --arg bind_agent_addr "0.0.0.0:${AGENT_PORT}" \
    --arg advertise_health_addr ":${HEALTH_PORT}" \
    --arg advertise_election_addr ":${LEADER_PORT}" \
    --arg advertise_registration_addr "127.0.0.1:${REGISTRATION_PORT}" \
    --arg advertise_operator_addr "127.0.0.1:${OPERATOR_PORT}" \
    --arg advertise_agent_addr "127.0.0.1:${AGENT_PORT}" \
    '.shard.id = $shard |
     .shard.bind.health_addr = $bind_health_addr |
     .shard.bind.election_addr = $bind_election_addr |
     .shard.bind.registration_addr = $bind_registration_addr |
     .shard.bind.operator_addr = $bind_operator_addr |
     .shard.bind.agent_addr = $bind_agent_addr |
     .shard.advertise.health_addr = $advertise_health_addr |
     .shard.advertise.election_addr = $advertise_election_addr |
     .shard.advertise.registration_addr = $advertise_registration_addr |
     .shard.advertise.operator_addr = $advertise_operator_addr |
     .shard.advertise.agent_addr = $advertise_agent_addr' \
    > "$INSTANCE_CONFIG"

echo "==> Starting nstance-server instance ${PROC_NUM}"
echo "    Config: ${BASE_CONFIG}"
echo "    Shard: ${SHARD_NAME}"
echo "    Ports: health=${HEALTH_PORT}, leader=${LEADER_PORT}, registration=${REGISTRATION_PORT}, operator=${OPERATOR_PORT}, agent=${AGENT_PORT}"
echo "    Cache: ${NSTANCE_DEV_CACHE_DIR}"
echo "    Log:   ${LOG_FILE}"
echo "    Tmux:  nstance-${SHARD_NAME}-agents"

cd "${ROOT_DIR}"

BINARY="${ROOT_DIR}/bin/nstance-server"
BUILD_DONE="${LOCK_DIR}/server-build.done"

# Parse OVERMIND_FORMATION to get server count (e.g., "s3=1,server=2,k8s=1,operator=1")
SERVER_COUNT=1
if [ -n "${OVERMIND_FORMATION}" ]; then
    SERVER_COUNT=$(echo "${OVERMIND_FORMATION}" | grep -oE 'server=[0-9]+' | cut -d= -f2)
    if [ -z "${SERVER_COUNT}" ]; then
        SERVER_COUNT=1
    fi
fi

# Only use Air when running a single server
if [ "${SERVER_COUNT}" -eq 1 ]; then
    echo "    Mode: air (hot reload)"
    exec air -c scripts/air/server.toml 2>&1 | tee "${LOG_FILE}"
else
    # Multiple servers: first instance builds, all run directly
    if [ "${PROC_NUM}" -eq 1 ]; then
        echo "    Mode: direct (building binary...)"
        rm -f "${BUILD_DONE}"
        make nstance-server
        touch "${BUILD_DONE}"
        echo "    Build complete, starting server"
    else
        echo "    Mode: direct (waiting for build...)"
        while [ ! -f "${BUILD_DONE}" ]; do
            sleep 1
        done
        echo "    Build complete, starting server"
    fi
    # Restart loop (like air does for hot reload)
    # Disable set -e for the loop since we expect the server to exit and restart
    set +e
    SHOULD_RESTART=true
    trap 'SHOULD_RESTART=false' SIGINT SIGTERM
    while $SHOULD_RESTART; do
        "${BINARY}" --id dev --storage s3 --bucket dev --shard "${NSTANCE_DEV_SHARD}" --cachedir "${NSTANCE_DEV_CACHE_DIR}" --advertise-host 127.0.0.1 --debug 2>&1 | tee "${LOG_FILE}"
        EXIT_CODE=$?
        if $SHOULD_RESTART; then
            echo "    Server exited with code ${EXIT_CODE}, restarting in 2s..."
            sleep 2
        fi
    done
    echo "    Server shutdown complete"
fi
