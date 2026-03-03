#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# E2E test for the admin CLI commands.
# Tests group list, create, scale, and delete operations.
# This test verifies CLI commands work correctly without requiring the operator.

# shellcheck disable=SC1091
source "$(dirname "${BASH_SOURCE[0]}")/test-helpers.sh"

echo "=== Nstance E2E Test: Admin CLI ==="

# ============================================================================
# Helpers
# ============================================================================

ADMIN_CLI="${ROOT_DIR}/bin/nstance-admin"
ADMIN_BUCKET="dev"

admin() {
    AWS_ACCESS_KEY_ID=dev \
    AWS_SECRET_ACCESS_KEY=dev \
    AWS_ENDPOINT_URL=http://localhost:8989 \
    AWS_S3_USE_PATH_STYLE=true \
    NSTANCE_ENCRYPTION_KEY=thisisatest32bytekey123456789012 \
    "${ADMIN_CLI}" --bucket "${ADMIN_BUCKET}" "$@"
}

group_exists() {
    local group="$1"
    admin group list --all-shards 2>/dev/null | grep -qE "^[^ ]+[[:space:]]+${group}[[:space:]]"
}

get_group_total_size() {
    local group="$1"
    admin group list --all-shards 2>/dev/null | grep -E "^[^ ]+[[:space:]]+${group}[[:space:]]" | awk '{sum += $3} END {print sum+0}'
}

# ============================================================================
# Preflight Checks
# ============================================================================

check_deps jq curl overmind
require_dev_env "s3 server"

[ -x "${ADMIN_CLI}" ] || { echo "Error: Admin CLI not found at ${ADMIN_CLI} - run 'make build' first"; exit 1; }
echo "✓ Admin CLI found at ${ADMIN_CLI}"

# ============================================================================
# Test: List Groups (baseline)
# ============================================================================

echo "Testing group list..."
admin group list --all-shards >/dev/null || { echo "Error: group list failed"; exit 1; }
echo "✓ Group list works"

if group_exists test; then
    echo "✓ 'test' group exists"
else
    echo "Error: 'test' group not found in group list"
    exit 1
fi

# ============================================================================
# Test: Scale Existing Group
# ============================================================================

echo "Scaling 'test' group to 0..."
admin group scale test 0 --all-shards || { echo "Error: scale to 0 failed"; exit 1; }
[ "$(get_group_total_size test)" -eq 0 ] || { echo "Error: 'test' group size not 0 after scale"; exit 1; }
echo "✓ Scaled 'test' to 0"

echo "Scaling 'test' group to 2..."
admin group scale test 2 --all-shards || { echo "Error: scale to 2 failed"; exit 1; }
[ "$(get_group_total_size test)" -eq 4 ] || { echo "Error: 'test' group total size not 4 after scale"; exit 1; }
echo "✓ Scaled 'test' to 2 per shard (4 total)"

# ============================================================================
# Test: Create New Group
# ============================================================================

echo "Cleaning up 'example' group if it exists..."
if group_exists example; then
    admin group delete example --all-shards 2>/dev/null || true
    sleep 1
fi

echo "Creating 'example' group..."
admin group create example --template test --size 1 --all-shards || { echo "Error: group create failed"; exit 1; }

if group_exists example; then
    echo "✓ 'example' group created and visible in list"
else
    echo "Error: 'example' group not visible after create"
    exit 1
fi

[ "$(get_group_total_size example)" -eq 2 ] || { echo "Error: 'example' group total size not 2 after create"; exit 1; }
echo "✓ 'example' group has total size 2 (1 per shard)"

# ============================================================================
# Test: Scale New Group
# ============================================================================

echo "Scaling 'example' group to 3..."
admin group scale example 3 --all-shards || { echo "Error: scale example to 3 failed"; exit 1; }
[ "$(get_group_total_size example)" -eq 6 ] || { echo "Error: 'example' group total size not 6 after scale"; exit 1; }
echo "✓ Scaled 'example' to 3 per shard (6 total)"

# ============================================================================
# Test: Delete Group
# ============================================================================

echo "Deleting 'example' group..."
admin group delete example --all-shards || { echo "Error: group delete failed"; exit 1; }
sleep 1

if group_exists example; then
    echo "Error: 'example' group still exists after delete"
    exit 1
else
    echo "✓ 'example' group deleted successfully"
fi

# ============================================================================
# Cleanup
# ============================================================================

echo "Ensuring 'test' group is at 2 replicas..."
admin group scale test 2 --all-shards || true

echo "=== E2E Test Passed ==="
