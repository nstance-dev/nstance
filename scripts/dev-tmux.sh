#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

set -e

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:-full}"
SERVER_COUNT=2
PORT_STEP=10

case "${MODE}" in
    full) FORMATION="s3=1,server=${SERVER_COUNT},proxy=${SERVER_COUNT},tunnel=${SERVER_COUNT},k8s=1,operator=1" ;;
    server) FORMATION="s3=1,server=${SERVER_COUNT},proxy=${SERVER_COUNT},tunnel=${SERVER_COUNT},k8s=0,operator=0" ;;
    *) echo "Usage: $0 full|server" >&2; exit 1 ;;
esac

for _ in $(seq 1 100); do
    base=$((20000 + RANDOM % 12000))
    ports="$base $((base + 1)) $((base + 2))"
    for i in $(seq 0 $((SERVER_COUNT - 1))); do
        offset=$((i * PORT_STEP))
        for service_offset in 0 1 2 3 4; do
            ports="${ports} $((base + 10 + offset + service_offset))"
        done
        ports="${ports} $((base + 1000 + offset)) $((base + 1100 + i + 1))"
    done
    available=true
    for port in ${ports}; do
        if lsof -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; then available=false; break; fi
    done
    ${available} && break
done
${available} || { echo "Unable to allocate a free development port range" >&2; exit 1; }

export DEV_S3_PORT="${base}"
export DEV_K8S_PORT="$((base + 1))"
export DEV_OPERATOR_HEALTH_PORT="$((base + 2))"
export BASE_HEALTH_PORT="$((base + 10))"
export BASE_LEADER_PORT="$((base + 11))"
export BASE_REGISTRATION_PORT="$((base + 12))"
export BASE_OPERATOR_PORT="$((base + 13))"
export BASE_AGENT_PORT="$((base + 14))"
export BASE_PROXY_PORT="$((base + 1000))"
export BASE_TUNNEL_READINESS_PORT="$((base + 1100))"
export PORT_STEP
export DEV_K8S_URL="http://127.0.0.1:${DEV_K8S_PORT}"
export AWS_ENDPOINT_URL="http://127.0.0.1:${DEV_S3_PORT}"
export OVERMIND_FORMATION="${FORMATION}"
export DEV_RUN_DIR="${ROOT_DIR}/temp/run-${base}"

mkdir -p "${DEV_RUN_DIR}"
{
    printf 'export DEV_S3_PORT="%s"\n' "${DEV_S3_PORT}"
    printf 'export DEV_K8S_PORT="%s"\n' "${DEV_K8S_PORT}"
    printf 'export DEV_OPERATOR_HEALTH_PORT="%s"\n' "${DEV_OPERATOR_HEALTH_PORT}"
    printf 'export BASE_HEALTH_PORT="%s"\n' "${BASE_HEALTH_PORT}"
    printf 'export BASE_LEADER_PORT="%s"\n' "${BASE_LEADER_PORT}"
    printf 'export BASE_REGISTRATION_PORT="%s"\n' "${BASE_REGISTRATION_PORT}"
    printf 'export BASE_OPERATOR_PORT="%s"\n' "${BASE_OPERATOR_PORT}"
    printf 'export BASE_AGENT_PORT="%s"\n' "${BASE_AGENT_PORT}"
    printf 'export BASE_PROXY_PORT="%s"\n' "${BASE_PROXY_PORT}"
    printf 'export BASE_TUNNEL_READINESS_PORT="%s"\n' "${BASE_TUNNEL_READINESS_PORT}"
    printf 'export PORT_STEP="%s"\n' "${PORT_STEP}"
    printf 'export DEV_K8S_URL="%s"\n' "${DEV_K8S_URL}"
    printf 'export AWS_ENDPOINT_URL="%s"\n' "${AWS_ENDPOINT_URL}"
    printf 'export OVERMIND_FORMATION="%s"\n' "${OVERMIND_FORMATION}"
    printf 'export DEV_RUN_DIR="%s"\n' "${DEV_RUN_DIR}"
} > "${ROOT_DIR}/temp/dev-ports.env"
echo "Development port base: ${base} (saved to temp/dev-ports.env)"

"${ROOT_DIR}/scripts/check-dev-ports.sh"
trap 'tmux list-sessions -F "#{session_name}" 2>/dev/null | grep "^nstance-.*-agents$" | xargs -I{} tmux kill-session -t {} 2>/dev/null || true' EXIT
cd "${ROOT_DIR}"
overmind start
