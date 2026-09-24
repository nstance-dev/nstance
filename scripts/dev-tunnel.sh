#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

set -e

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCK_DIR="${DEV_RUN_DIR:-${ROOT_DIR}/temp}"
mkdir -p "${LOCK_DIR}" "${ROOT_DIR}/temp/logs"
PROC_NUM=1
while ! mkdir "${LOCK_DIR}/tunnel-${PROC_NUM}.lock" 2>/dev/null; do PROC_NUM=$((PROC_NUM + 1)); done
trap 'rmdir "${LOCK_DIR}/tunnel-${PROC_NUM}.lock"' EXIT

PROXY_PORT=$((${BASE_PROXY_PORT:-16443} + ((PROC_NUM - 1) * ${PORT_STEP:-10})))
READINESS_PORT=$((${BASE_TUNNEL_READINESS_PORT:-28080} + PROC_NUM))
MOCK="${LOCK_DIR}/dev-tunnel-${PROC_NUM}"
go build -o "${MOCK}" ./cmd/dev-tunnel
CONFIG="${LOCK_DIR}/nstance-tunnel-${PROC_NUM}.json"
cat > "${CONFIG}" <<EOF
{
  "socket": "${LOCK_DIR}/nstance-tunnel-${PROC_NUM}.sock",
  "tunnels": {
    "dev-api:${PROXY_PORT}": {
      "command": "${MOCK}",
      "args": ["--listen", "127.0.0.1:${READINESS_PORT}"],
      "readiness_url": "http://127.0.0.1:${READINESS_PORT}/",
      "readiness_timeout": "5s"
    }
  }
}
EOF

exec go run ./cmd/nstance-tunnel --config "${CONFIG}" 2>&1 | tee "${ROOT_DIR}/temp/logs/tunnel-${PROC_NUM}.log"
