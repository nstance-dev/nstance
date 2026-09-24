#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
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
ADMIN_IDENTITY_DIR="${DEV_RUN_DIR:-${ROOT_DIR}/temp}/admin-identity"
ADMIN_SERVERS=""
ADMIN_TEST_GROUP="admin-e2e"

# Runs nstance-admin against the current development servers.
admin() {
    "${ADMIN_CLI}" --servers "${ADMIN_SERVERS}" --identity-dir "${ADMIN_IDENTITY_DIR}" "$@"
}

# Reports whether the named group exists on any shard.
group_exists() {
    local group="$1"
    admin group list --all-shards 2>/dev/null | grep -qE "^[^ ]+[[:space:]]+${group}[[:space:]]"
}

# Sums the configured size of the named group across all shards.
get_group_total_size() {
    local group="$1"
    admin group list --all-shards 2>/dev/null | grep -E "^[^ ]+[[:space:]]+${group}[[:space:]]" | awk '{sum += $3} END {print sum+0}'
}

# ============================================================================
# Preflight Checks
# ============================================================================

check_deps overmind
require_dev_env "s3 server"

[ -x "${ADMIN_CLI}" ] || { echo "Error: Admin CLI not found at ${ADMIN_CLI} - run 'make build' first"; exit 1; }
echo "✓ Admin CLI found at ${ADMIN_CLI}"

for i in $(seq 1 "${SERVER_COUNT}"); do
    shard="dev-${i}"
    port=$(( BASE_OPERATOR_PORT + (i - 1) * PORT_STEP ))
    [ -z "${ADMIN_SERVERS}" ] || ADMIN_SERVERS+=","
    ADMIN_SERVERS+="${shard}=127.0.0.1:${port}"
done

NSTANCE_ENCRYPTION_KEY=thisisatest32bytekey123456789012 \
AWS_ACCESS_KEY_ID=dev \
AWS_SECRET_ACCESS_KEY=dev \
AWS_ENDPOINT_URL="${AWS_ENDPOINT_URL}" \
AWS_S3_USE_PATH_STYLE=true \
"${ADMIN_CLI}" cluster register-operator \
    --storage-bucket dev \
    --secrets-provider object-storage \
    --key-provider env \
    --output-dir "${ADMIN_IDENTITY_DIR}"

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

echo "Cleaning up '${ADMIN_TEST_GROUP}' group if it exists..."
if group_exists "${ADMIN_TEST_GROUP}"; then
    admin group delete "${ADMIN_TEST_GROUP}" --all-shards 2>/dev/null || true
    sleep 1
fi

echo "Creating '${ADMIN_TEST_GROUP}' group..."
admin group create "${ADMIN_TEST_GROUP}" --template test --size 1 --all-shards || { echo "Error: group create failed"; exit 1; }

if group_exists "${ADMIN_TEST_GROUP}"; then
    echo "✓ '${ADMIN_TEST_GROUP}' group created and visible in list"
else
    echo "Error: '${ADMIN_TEST_GROUP}' group not visible after create"
    exit 1
fi

[ "$(get_group_total_size "${ADMIN_TEST_GROUP}")" -eq 2 ] || { echo "Error: '${ADMIN_TEST_GROUP}' group total size not 2 after create"; exit 1; }
echo "✓ '${ADMIN_TEST_GROUP}' group has total size 2 (1 per shard)"

# ============================================================================
# Test: Scale New Group
# ============================================================================

echo "Scaling '${ADMIN_TEST_GROUP}' group to 3..."
admin group scale "${ADMIN_TEST_GROUP}" 3 --all-shards || { echo "Error: scale ${ADMIN_TEST_GROUP} to 3 failed"; exit 1; }
[ "$(get_group_total_size "${ADMIN_TEST_GROUP}")" -eq 6 ] || { echo "Error: '${ADMIN_TEST_GROUP}' group total size not 6 after scale"; exit 1; }
echo "✓ Scaled '${ADMIN_TEST_GROUP}' to 3 per shard (6 total)"

# ============================================================================
# Test: Delete Group
# ============================================================================

echo "Deleting '${ADMIN_TEST_GROUP}' group..."
admin group delete "${ADMIN_TEST_GROUP}" --all-shards || { echo "Error: group delete failed"; exit 1; }
sleep 1

if group_exists "${ADMIN_TEST_GROUP}"; then
    echo "Error: '${ADMIN_TEST_GROUP}' group still exists after delete"
    exit 1
else
    echo "✓ '${ADMIN_TEST_GROUP}' group deleted successfully"
fi

# ============================================================================
# Cleanup
# ============================================================================

echo "Ensuring 'test' group is at 2 replicas..."
admin group scale test 2 --all-shards || true

echo "=== E2E Test Passed ==="
