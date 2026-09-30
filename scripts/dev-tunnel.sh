#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCK_DIR="${DEV_RUN_DIR:-${ROOT_DIR}/temp}"
mkdir -p "${LOCK_DIR}" "${ROOT_DIR}/temp/logs"
PROC_NUM=1
while ! mkdir "${LOCK_DIR}/tunnel-${PROC_NUM}.lock" 2>/dev/null; do PROC_NUM=$((PROC_NUM + 1)); done

READINESS_PORT=$((${BASE_TUNNEL_READINESS_PORT:-28080} + PROC_NUM))
MOCK="${LOCK_DIR}/dev-tunnel-${PROC_NUM}"
go build -o "${MOCK}" ./cmd/dev-tunnel
"${MOCK}" --listen "127.0.0.1:${READINESS_PORT}" &
MOCK_PID=$!
trap 'kill "${MOCK_PID}" 2>/dev/null || true; wait "${MOCK_PID}" 2>/dev/null || true; rmdir "${LOCK_DIR}/tunnel-${PROC_NUM}.lock"' EXIT

SHARD="dev-${PROC_NUM}"
CONFIG="${LOCK_DIR}/dev-s3/shard/${SHARD}/config.jsonc"
while [ ! -f "${CONFIG}" ]; do sleep 0.1; done

export AWS_S3_USE_PATH_STYLE=true
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1
export AWS_ENDPOINT_URL="${AWS_ENDPOINT_URL:-http://127.0.0.1:${DEV_S3_PORT:-8989}}"
export NSTANCE_ENCRYPTION_KEY=thisisatest32bytekey123456789012

MANIFEST_DIR="${LOCK_DIR}/tunnel-manifests-${PROC_NUM}"
FILES_DIR="${LOCK_DIR}/tunnel-files-${PROC_NUM}"
mkdir -p "${MANIFEST_DIR}" "${FILES_DIR}"

go run ./cmd/nstance-server tunnel \
  --storage s3 \
  --bucket dev \
  --shard "${SHARD}" \
  --server-socket "${LOCK_DIR}/nstance-tunnel-${PROC_NUM}.sock" \
  --manifest-dir "${MANIFEST_DIR}" \
  --files-dir "${FILES_DIR}" \
  2>&1 | tee "${ROOT_DIR}/temp/logs/tunnel-${PROC_NUM}.log"
