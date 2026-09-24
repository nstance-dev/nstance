#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

set -e

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCK_DIR="${DEV_RUN_DIR:-${ROOT_DIR}/temp}"
mkdir -p "${LOCK_DIR}" "${ROOT_DIR}/temp/logs"
PROC_NUM=1
while ! mkdir "${LOCK_DIR}/proxy-${PROC_NUM}.lock" 2>/dev/null; do PROC_NUM=$((PROC_NUM + 1)); done
trap 'rmdir "${LOCK_DIR}/proxy-${PROC_NUM}.lock"' EXIT

exec go run ./cmd/nstance-proxy \
    --socket "${LOCK_DIR}/nstance-server-${PROC_NUM}.sock" \
    --bind-host 127.0.0.1 \
    --debug 2>&1 | tee "${ROOT_DIR}/temp/logs/proxy-${PROC_NUM}.log"
