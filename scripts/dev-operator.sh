#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

# Development script for the nstance-operator.
# This script:
# 1. Waits for the nstance-server(s) to be healthy
# 2. Sets up the dev-k8s with required secrets (CA, nonce)
# 3. Generates operator config with all available server endpoints
# 4. Starts the operator with air for live reload
#
# Supports multi-server dev mode via OVERMIND_FORMATION scaling.
# Port scheme: server instance N uses base port + (N-1)*10
#   Instance 1: registration=8992, operator=8993
#   Instance 2: registration=9002, operator=9003

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

DEV_K8S_DIR="${ROOT_DIR}/temp/dev-k8s"
DEV_S3_DIR="${ROOT_DIR}/temp/dev-s3"
DEV_K8S_URL="${DEV_K8S_URL:-http://localhost:6443}"
NAMESPACE="${NAMESPACE:-default}"

# Port configuration
BASE_HEALTH_PORT="${BASE_HEALTH_PORT:-8990}"
BASE_REGISTRATION_PORT="${BASE_REGISTRATION_PORT:-8992}"
BASE_OPERATOR_PORT="${BASE_OPERATOR_PORT:-8993}"
PORT_STEP="${PORT_STEP:-10}"

# Discover number of server instances from OVERMIND_FORMATION or default to 1
discover_server_count() {
    local formation="${OVERMIND_FORMATION:-server=1}"
    local count=1
    
    # Parse server=N from formation string
    if [[ "$formation" =~ server=([0-9]+) ]]; then
        count="${BASH_REMATCH[1]}"
    fi
    
    echo "$count"
}

# Calculate port for server instance N (1-indexed)
calc_port() {
    local base_port=$1
    local instance=$2
    echo $(( base_port + (instance - 1) * PORT_STEP ))
}

# Wait for a server instance to be healthy and gRPC ready
wait_for_server() {
    local instance=$1
    local health_port
    health_port=$(calc_port "$BASE_HEALTH_PORT" "$instance")
    local reg_port
    reg_port=$(calc_port "$BASE_REGISTRATION_PORT" "$instance")
    local health_url="http://localhost:${health_port}/healthz"
    
    echo "==> Waiting for nstance-server instance ${instance} at ${health_url}..."
    until curl -sf "$health_url" > /dev/null 2>&1; do
        sleep 1
    done
    echo "==> nstance-server instance ${instance} health OK, waiting for gRPC (port ${reg_port})..."
    until nc -z localhost "${reg_port}" 2>/dev/null; do
        sleep 1
    done
    echo "==> nstance-server instance ${instance} is ready"
}

# Ensure nstance-admin is built fresh (needed for nonce generation)
echo "==> Building nstance-admin..."
make -B -C "$ROOT_DIR" bin/nstance-admin >/dev/null 2>&1 || {
    echo "==> Warning: Failed to build nstance-admin, using existing binary"
}

# Wait for dev-s3 to be ready
echo "==> Waiting for dev-s3 to be healthy..."
until curl -sf "http://localhost:8989/" > /dev/null 2>&1; do
    sleep 1
done
echo "==> dev-s3 is healthy"

SERVER_COUNT=$(discover_server_count)
echo "==> Discovered ${SERVER_COUNT} server instance(s) from OVERMIND_FORMATION"

# Wait for all server instances to be healthy
for i in $(seq 1 "$SERVER_COUNT"); do
    wait_for_server "$i"
done

echo "==> Waiting for dev-k8s to be healthy..."
until curl -sf "$DEV_K8S_URL/healthz" > /dev/null 2>&1; do
    sleep 1
done
echo "==> dev-k8s is healthy"

# Ensure directories exist
mkdir -p "${DEV_K8S_DIR}/secrets/${NAMESPACE}"
mkdir -p "${DEV_K8S_DIR}/configmaps/${NAMESPACE}"

# Create cluster CA ConfigMap from the dev-s3 CA cert if it exists
CA_CERT_PATH="${DEV_S3_DIR}/cluster/ca.crt"
if [ -f "$CA_CERT_PATH" ]; then
    echo "==> Creating cluster CA ConfigMap..."
    CA_CERT=$(cat "$CA_CERT_PATH")
    cat > "${DEV_K8S_DIR}/configmaps/${NAMESPACE}/nstance-cluster-ca.json" << EOF
{
  "apiVersion": "v1",
  "kind": "ConfigMap",
  "metadata": {
    "name": "nstance-cluster-ca",
    "namespace": "${NAMESPACE}",
    "uid": "dev-cluster-ca-$(date +%s)",
    "creationTimestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
    "resourceVersion": "1"
  },
  "data": {
    "ca.crt": $(echo "$CA_CERT" | jq -Rs .)
  }
}
EOF
else
    echo "==> Warning: CA cert not found at $CA_CERT_PATH, skipping ConfigMap creation"
fi

# Generate operator nonce using nstance-admin
echo "==> Generating operator registration nonce..."
NONCE_JWT=$(NSTANCE_ENCRYPTION_KEY=thisisatest32bytekey123456789012 AWS_ACCESS_KEY_ID=dev AWS_SECRET_ACCESS_KEY=dev AWS_ENDPOINT_URL=http://localhost:8989 AWS_S3_USE_PATH_STYLE=true ./bin/nstance-admin cluster nonce --cluster-id example-cluster --storage-bucket dev --key-provider env --output - 2>/dev/null || echo "")

if [ -z "$NONCE_JWT" ]; then
    echo "==> Warning: Could not generate nonce, operator registration may fail"
else
    echo "==> Creating nonce Secret..."
    cat > "${DEV_K8S_DIR}/secrets/${NAMESPACE}/nstance-operator-nonce.json" << EOF
{
  "apiVersion": "v1",
  "kind": "Secret",
  "metadata": {
    "name": "nstance-operator-nonce",
    "namespace": "${NAMESPACE}",
    "uid": "dev-nonce-$(date +%s)",
    "creationTimestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
    "resourceVersion": "1"
  },
  "type": "Opaque",
  "data": {
    "nonce.jwt": "$(echo -n "$NONCE_JWT" | base64)"
  }
}
EOF
fi

# Create operator config file with all server endpoints
echo "==> Creating operator config with ${SERVER_COUNT} server endpoint(s)..."
mkdir -p "${ROOT_DIR}/temp/operator"

# Build the shards configuration dynamically - each server instance is a separate shard
{
    echo "# Generated by dev-operator.sh"
    echo "# Server instances: ${SERVER_COUNT}"
    echo "# Port scheme: base + (instance-1) * ${PORT_STEP}"
    echo "cluster_id: example-cluster"
    echo "tenant: default"
    echo "shards:"
    for i in $(seq 1 "$SERVER_COUNT"); do
        reg_port=$(calc_port "$BASE_REGISTRATION_PORT" "$i")
        op_port=$(calc_port "$BASE_OPERATOR_PORT" "$i")
        # Name shards as dev-1, dev-2, dev-3, etc. to match server naming
        shard_name="dev-${i}"
        echo "  ${shard_name}:"
        echo "    registration_addr: \"127.0.0.1:${reg_port}\""
        echo "    operator_addr: \"127.0.0.1:${op_port}\""
    done
} > "${ROOT_DIR}/temp/operator/config.yaml"

# Log all configured endpoints
echo "==> Configured shards:"
for i in $(seq 1 "$SERVER_COUNT"); do
    reg_port=$(calc_port "$BASE_REGISTRATION_PORT" "$i")
    op_port=$(calc_port "$BASE_OPERATOR_PORT" "$i")
    shard_name="dev-${i}"
    echo "    ${shard_name}: registration=127.0.0.1:${reg_port}, operator=127.0.0.1:${op_port}"
done

# Set up kubeconfig for the fake k8s server
echo "==> Creating kubeconfig for dev-k8s..."
cat > "${ROOT_DIR}/temp/operator/kubeconfig" << EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: ${DEV_K8S_URL}
    insecure-skip-tls-verify: true
  name: dev-k8s
contexts:
- context:
    cluster: dev-k8s
    namespace: ${NAMESPACE}
  name: dev-k8s
current-context: dev-k8s
users: []
EOF

export KUBECONFIG="${ROOT_DIR}/temp/operator/kubeconfig"
export NSTANCE_NAMESPACE="${NAMESPACE}"
export NSTANCE_CA_CONFIGMAP="nstance-cluster-ca"
export NSTANCE_NONCE_SECRET="nstance-operator-nonce"
export NSTANCE_KEY_SECRET="nstance-operator-key"
export NSTANCE_CERT_SECRET="nstance-operator-cert"
export NSTANCE_K8S_JSON="true"

echo "==> Starting operator with air..."
exec air -c scripts/air/operator.toml
