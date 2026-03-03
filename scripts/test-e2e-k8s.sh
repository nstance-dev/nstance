#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# E2E test for the dev environment.
# Tests NstanceMachinePool → MachinePool → Instance creation flow.

# shellcheck disable=SC2016,SC1091
# SC2016: Single-quoted expressions with $() are intentional; wait_for uses eval
source "$(dirname "${BASH_SOURCE[0]}")/test-helpers.sh"

echo "=== Nstance E2E Test: K8s Operator ==="

check_deps jq curl overmind tmux
require_dev_env "s3 server k8s operator"
curl -sf http://localhost:6443/healthz >/dev/null || { echo "Error: dev-k8s not responding on port 6443"; exit 1; }
wait_for "Operator ready" 30 "curl -sf http://localhost:8081/healthz >/dev/null"

# ============================================================================
# Test: Reset 'test' Pool
# ============================================================================

if resource_exists machinepools default test; then
    echo "Resetting 'test' machinepool to 2 replicas..."
    set_replicas test 2
    kill_agent_windows "nstance-agent-"
    wait_for "'test' machinepool has 2 instances" 30 '[ "$(count_instances test)" -eq 2 ]'
fi

# ============================================================================
# Test: Cleanup Existing Resources
# ============================================================================

echo "Cleaning up existing 'example' resources..."
if resource_exists machinepools default example; then
    set_replicas example 0
    wait_for "'example' instances deleted" 30 '[ "$(count_instances example)" -eq 0 ]'
    mark_for_deletion nstancemachinepools default example
fi

if resource_exists pods default my-on-demand-pod; then
    mark_for_deletion pods default my-on-demand-pod
    wait_for "on-demand instance deleted" 30 '[ "$(count_instances test true)" -eq 0 ]'
fi
sleep 1

# ============================================================================
# Test: Create NstanceMachinePool → MachinePool → Instance
# ============================================================================

echo "Creating NstanceMachinePool..."
mkdir -p "${DEV_K8S_DIR}/nstancemachinepools/default"
cp "${ROOT_DIR}/docs/nstancemachinepool.json" "${DEV_K8S_DIR}/nstancemachinepools/default/example.json"

wait_for "MachinePool created" 30 "resource_exists machinepools default example"

echo "Setting MachinePool replicas to 1..."
set_replicas example 1

wait_for "instance created" 30 "nodes_exist"

[ "$(count_instances test)" -eq 2 ] || { echo "Error: Expected 2 'test' instances, found $(count_instances test)"; exit 1; }
echo "✓ 2 instances from 'test' pool"

# ============================================================================
# Test: Scale 'test' Pool to Zero
# ============================================================================

echo "Scaling 'test' MachinePool to 0..."
set_replicas test 0
wait_for "'test' instances deleted" 30 '[ "$(count_instances test)" -eq 0 ]'

# ============================================================================
# Test: On-Demand Instance
# ============================================================================

echo "Creating on-demand pod..."
mkdir -p "${DEV_K8S_DIR}/pods/default"
cp "${ROOT_DIR}/docs/pod.json" "${DEV_K8S_DIR}/pods/default/my-on-demand-pod.json"

wait_for "on-demand instance created" 30 '[ "$(count_instances test true)" -ge 1 ]'

echo "Deleting on-demand pod..."
mark_for_deletion pods default my-on-demand-pod
sleep 1
mark_for_deletion nstancemachines default on-demand-my-on-demand-pod

wait_for "on-demand instance deleted" 60 '[ "$(count_instances test true)" -eq 0 ]'

# ============================================================================
# Cleanup
# ============================================================================

echo "Restoring 'test' MachinePool to 2 replicas..."
set_replicas test 2

echo "Cleaning up 'example' pool..."
if resource_exists machinepools default example; then
    set_replicas example 0
    kill_agent_windows "nstance-agent-exm"
    wait_for "'example' instances deleted" 30 '[ "$(count_instances example)" -eq 0 ]'
    mark_for_deletion nstancemachinepools default example
fi

echo "=== E2E Test Passed ==="
