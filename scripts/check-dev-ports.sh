#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

# Check that all required ports for dev services are available.
# Reads OVERMIND_FORMATION from .env to determine server count.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Load .env if it exists
if [ -f "${ROOT_DIR}/.env" ]; then
    export "$(grep -v '^#' "${ROOT_DIR}/.env" | xargs)"
fi

# Parse server count from OVERMIND_FORMATION (default to 1)
SERVER_COUNT=1
if [ -n "${OVERMIND_FORMATION}" ]; then
    COUNT=$(echo "${OVERMIND_FORMATION}" | grep -oE 'server=[0-9]+' | cut -d= -f2)
    if [ -n "${COUNT}" ]; then
        SERVER_COUNT="${COUNT}"
    fi
fi

# Build list of ports to check
PORTS=""

# s3: 8989
PORTS="8989"

# k8s: 6443
PORTS="${PORTS} 6443"

# operator: 8081 (health probe)
PORTS="${PORTS} 8081"

# server instances: each uses 5 ports with 10-port gap
# Instance 1: 8990-8994
# Instance 2: 9000-9004
# Instance N: base + (N-1)*10
for i in $(seq 1 "${SERVER_COUNT}"); do
    OFFSET=$(( (i - 1) * 10 ))
    for p in 8990 8991 8992 8993 8994; do
        PORT=$((p + OFFSET))
        PORTS="${PORTS} ${PORT}"
    done
done

# Check for existing dev processes
STALE_PROCS=""
for PROC in "dev-k8s" "dev-s3" "nstance-operator"; do
    if pgrep -f "${PROC}" >/dev/null 2>&1; then
        STALE_PROCS="${STALE_PROCS} ${PROC}"
    fi
done
if [ -n "${STALE_PROCS}" ]; then
    echo "Error: The following dev processes are already running:${STALE_PROCS}"
    echo ""
    echo "Run the following to stop them:"
    for PROC in ${STALE_PROCS}; do
        echo "  pkill -f ${PROC}"
    done
    exit 1
fi

# Check each port
BLOCKED=""
for PORT in ${PORTS}; do
    if lsof -i ":${PORT}" -sTCP:LISTEN >/dev/null 2>&1; then
        BLOCKED="${BLOCKED} ${PORT}"
    fi
done
if [ -n "${BLOCKED}" ]; then
    echo "Error: The following ports are already in use:${BLOCKED}"
    echo ""
    echo "Run the following to see what's using them:"
    for PORT in ${BLOCKED}; do
        echo "  lsof -i :${PORT}"
    done
    exit 1
fi

echo "All required ports are available (server count: ${SERVER_COUNT})"
