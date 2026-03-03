#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

# Set up nstance-server with keepalived on a Proxmox VE node.
# For use in an architecture where you run nstance-server on every node.
#
# This script installs nstance-server as a systemd service with keepalived
# for VIP-based leader failover. Run it on each Proxmox node in the cluster.
#
# The nstance-server instances coordinate leadership via S3-based leader
# election (S3lect). Great for use with Ceph RGW or SeaweedFS. Keepalived 
# monitors which node is the current leader (via port binding check), and 
# assigns the VIP to that node, giving agents a stable address.
#
# Usage:
#   ./server-with-keepalived.sh [options]
#
# Options:
#   --server-binary PATH       Path to nstance-server binary to install (required)
#   --vip ADDRESS              Virtual IP for leader services (required)
#   --vip-cidr N               VIP CIDR prefix length (default: 24)
#   --interface IFACE          Network interface for VRRP (auto-detected from VIP)
#   --router-id ID             VRRP virtual router ID (default: 51)
#   --shard NAME               Shard identifier (default: dev)
#   --bucket NAME              S3 bucket name (required)
#   --s3-endpoint URL          S3 endpoint for non-AWS backends (e.g. SeaweedFS)
#   --encryption-key FILE      Read 32-byte encryption key from this file
#   --config-dir DIR           Configuration directory (default: /etc/nstance)
#   --cache-dir DIR            Data/cache directory (default: /var/lib/nstance)
#   --install-dir DIR          Binary install directory (default: /usr/local/bin)
#   --enterprise               Keep Proxmox enterprise repos (requires subscription)
#   --uninstall                Stop services, remove binary, configs, and user
#   --dry-run                  Show what would be done without making changes
#   --help                     Show this help message
#
# Required environment variables (secrets, to avoid shell history exposure):
#   PROXMOX_API_URL            Proxmox API URL (default: https://localhost:8006/api2/json)
#   PROXMOX_TOKEN_ID           Proxmox API token ID (e.g. user@pam!token-name)
#   PROXMOX_TOKEN_SECRET       Proxmox API token secret
#   AWS_ACCESS_KEY_ID          S3 access key (for SeaweedFS/MinIO/AWS)
#   AWS_SECRET_ACCESS_KEY      S3 secret key
#
# Security:
#   The encryption key is stored at <config-dir>/encryption.key (root:nstance 0640).
#   The nstance-server process reads it via "provider": "file" in the shard config.
#   The S3 credentials are stored in <config-dir>/server.env (root:root 0600) and
#   loaded by systemd before dropping privileges to the nstance user.
#
# Example:
#   export AWS_ACCESS_KEY_ID=admin
#   export AWS_SECRET_ACCESS_KEY=admin
#
#   # First, generate the shard config and upload to S3:
#   ./create-shard-config.sh \
#       --vip 10.0.0.100 --shard dev --bucket nstance \
#       --s3-endpoint http://seaweedfs:8333 \
#       --output config.jsonc
#
#   # Generate a shared encryption key (same key on all nodes):
#   openssl rand 32 > /etc/nstance/encryption.key
#
#   # Then, run on each Proxmox node:
#   ./server-with-keepalived.sh \
#       --server-binary ./bin/nstance-server \
#       --vip 10.0.0.100 --shard dev --bucket nstance \
#       --s3-endpoint http://seaweedfs:8333

SERVER_BINARY=""
VIP=""
VIP_CIDR="24"
INTERFACE=""
ROUTER_ID="51"
SHARD="dev"
BUCKET=""
S3_ENDPOINT="${AWS_ENDPOINT_URL:-}"
ENCRYPTION_KEY=""
CONFIG_DIR="/etc/nstance"
CACHE_DIR="/var/lib/nstance"
INSTALL_DIR="/usr/local/bin"
ENTERPRISE=false
UNINSTALL=false
DRY_RUN=false

usage() {
    sed -n '/^# Usage:/,/^$/p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

log() {
    echo "[nstance-setup] $*"
}

fatal() {
    echo "[nstance-setup] ERROR: $*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --server-binary)       SERVER_BINARY="$2"; shift 2 ;;
        --vip)                 VIP="$2"; shift 2 ;;
        --vip-cidr)            VIP_CIDR="$2"; shift 2 ;;
        --interface)           INTERFACE="$2"; shift 2 ;;
        --router-id)           ROUTER_ID="$2"; shift 2 ;;
        --shard)               SHARD="$2"; shift 2 ;;
        --bucket)              BUCKET="$2"; shift 2 ;;
        --s3-endpoint)         S3_ENDPOINT="$2"; shift 2 ;;
        --encryption-key)      ENCRYPTION_KEY="$2"; shift 2 ;;
        --config-dir)          CONFIG_DIR="$2"; shift 2 ;;
        --cache-dir)           CACHE_DIR="$2"; shift 2 ;;
        --install-dir)         INSTALL_DIR="$2"; shift 2 ;;
        --enterprise)          ENTERPRISE=true; shift ;;
        --uninstall)           UNINSTALL=true; shift ;;
        --dry-run)             DRY_RUN=true; shift ;;
        --help|-h)             usage ;;
        *)                     fatal "Unknown option: $1" ;;
    esac
done

DEST_BINARY="${INSTALL_DIR}/nstance-server"
CHECK_SCRIPT="${INSTALL_DIR}/check-nstance-leader"
KEEPALIVED_CONF="/etc/keepalived/keepalived.conf"
UNIT_PATH="/etc/systemd/system/nstance-server.service"

# ---------------------------------------------------------------------------
# Uninstall
# ---------------------------------------------------------------------------

if [[ "${UNINSTALL}" == true ]]; then
    [[ "$(id -u)" -ne 0 ]] && fatal "This script must be run as root"

    log "Uninstalling nstance-server and keepalived config..."

    if systemctl is-active --quiet nstance-server 2>/dev/null; then
        log "Stopping nstance-server service..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would stop nstance-server"
        else
            systemctl stop nstance-server
            log "Service stopped"
        fi
    fi

    if systemctl is-enabled --quiet nstance-server 2>/dev/null; then
        log "Disabling nstance-server service..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would disable nstance-server"
        else
            systemctl disable nstance-server
            log "Service disabled"
        fi
    fi

    if [[ -f "${UNIT_PATH}" ]]; then
        log "Removing systemd unit ${UNIT_PATH}..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would remove ${UNIT_PATH}"
        else
            rm -f "${UNIT_PATH}"
            systemctl daemon-reload
            log "Unit file removed"
        fi
    fi

    if [[ -f "${DEST_BINARY}" ]]; then
        log "Removing binary ${DEST_BINARY}..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would remove ${DEST_BINARY}"
        else
            rm -f "${DEST_BINARY}"
            log "Binary removed"
        fi
    fi

    if [[ -f "${CHECK_SCRIPT}" ]]; then
        log "Removing check script ${CHECK_SCRIPT}..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would remove ${CHECK_SCRIPT}"
        else
            rm -f "${CHECK_SCRIPT}"
            log "Check script removed"
        fi
    fi

    if systemctl is-active --quiet keepalived 2>/dev/null; then
        log "Stopping keepalived..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would stop keepalived"
        else
            systemctl stop keepalived
            log "keepalived stopped"
        fi
    fi

    if systemctl is-enabled --quiet keepalived 2>/dev/null; then
        log "Disabling keepalived..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would disable keepalived"
        else
            systemctl disable keepalived
            log "keepalived disabled"
        fi
    fi

    if [[ -f "${KEEPALIVED_CONF}" ]]; then
        log "Removing keepalived config ${KEEPALIVED_CONF}..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would remove ${KEEPALIVED_CONF}"
        else
            rm -f "${KEEPALIVED_CONF}"
            log "keepalived config removed"
        fi
    fi

    if dpkg -s keepalived &>/dev/null 2>&1; then
        log "Removing keepalived package..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would purge keepalived"
        else
            apt-get purge -y -qq keepalived > /dev/null
            rm -rf /etc/keepalived
            log "keepalived purged"
        fi
    fi

    if [[ -d "${CONFIG_DIR}" ]]; then
        log "Removing config directory ${CONFIG_DIR} (preserving encryption.key)..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would remove contents of ${CONFIG_DIR} except encryption.key"
        else
            find "${CONFIG_DIR}" -mindepth 1 ! -name 'encryption.key' -delete
            log "Config directory cleaned (encryption.key preserved)"
        fi
    fi

    if [[ -d "${CACHE_DIR}" ]]; then
        log "Removing cache directory ${CACHE_DIR}..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would remove ${CACHE_DIR}"
        else
            rm -rf "${CACHE_DIR}"
            log "Cache directory removed"
        fi
    fi

    if id nstance &>/dev/null; then
        log "Removing system user 'nstance'..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would remove user 'nstance'"
        else
            userdel nstance
            log "User removed"
        fi
    fi

    log "Uninstall complete."
    exit 0
fi

# Default Proxmox API URL to local node.
: "${PROXMOX_API_URL:=https://localhost:8006/api2/json}"

# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------

[[ -z "${SERVER_BINARY}" ]] && fatal "--server-binary is required"
[[ -f "${SERVER_BINARY}" ]] || fatal "Server binary not found: ${SERVER_BINARY}"
[[ -x "${SERVER_BINARY}" ]] || fatal "Server binary not executable: ${SERVER_BINARY}"
[[ -z "${VIP}" ]]           && fatal "--vip is required"
[[ -z "${BUCKET}" ]]        && fatal "--bucket is required"

# Auto-detect the network interface for the VIP if not explicitly set.
# Finds the interface whose subnet contains the VIP address.
if [[ -z "${INTERFACE}" ]]; then
    IFS=. read -r v1 v2 v3 v4 <<< "${VIP}"
    vip_int=$(( (v1<<24) + (v2<<16) + (v3<<8) + v4 ))
    while read -r _ iface _ cidr _; do
        IFS=/ read -r addr prefix <<< "${cidr}"
        IFS=. read -r a1 a2 a3 a4 <<< "${addr}"
        addr_int=$(( (a1<<24) + (a2<<16) + (a3<<8) + a4 ))
        mask=$(( 0xFFFFFFFF << (32 - prefix) ))
        if (( (addr_int & mask) == (vip_int & mask) )); then
            INTERFACE="${iface}"
            break
        fi
    done < <(ip -4 -o addr show)
    [[ -z "${INTERFACE}" ]] && fatal "Could not auto-detect interface for VIP ${VIP}. Use --interface to specify it."
    log "Auto-detected interface for VIP: ${INTERFACE}"
fi

[[ -z "${AWS_ACCESS_KEY_ID:-}" ]]     && fatal "AWS_ACCESS_KEY_ID is required (for S3-compatible storage)"
[[ -z "${AWS_SECRET_ACCESS_KEY:-}" ]] && fatal "AWS_SECRET_ACCESS_KEY is required (for S3-compatible storage)"
[[ -z "${PROXMOX_TOKEN_ID:-}" ]]      && fatal "PROXMOX_TOKEN_ID is required (Proxmox API token ID, e.g. user@pam!token-name)"
[[ -z "${PROXMOX_TOKEN_SECRET:-}" ]]  && fatal "PROXMOX_TOKEN_SECRET is required (Proxmox API token secret)"

# Resolve encryption key. The server expects exactly 32 raw bytes on disk.
ENC_KEY_PATH="${CONFIG_DIR}/encryption.key"
if [[ -n "${ENCRYPTION_KEY}" ]]; then
    [[ -f "${ENCRYPTION_KEY}" ]] || fatal "Encryption key file not found: ${ENCRYPTION_KEY}"
    KEY_SIZE=$(wc -c < "${ENCRYPTION_KEY}")
    [[ "${KEY_SIZE}" -eq 32 ]] || fatal "Encryption key must be exactly 32 bytes, got ${KEY_SIZE}
  Generate one with: openssl rand 32 > ${ENC_KEY_PATH}"
elif [[ ! -f "${ENC_KEY_PATH}" ]]; then
    fatal "No encryption key found at ${ENC_KEY_PATH}
  Generate one with: openssl rand 32 > ${ENC_KEY_PATH}
  The same key must be used on all nodes"
else
    KEY_SIZE=$(wc -c < "${ENC_KEY_PATH}")
    [[ "${KEY_SIZE}" -eq 32 ]] || fatal "Encryption key at ${ENC_KEY_PATH} must be exactly 32 bytes, got ${KEY_SIZE}
  Regenerate with: openssl rand 32 > ${ENC_KEY_PATH}"
fi

if [[ "$(id -u)" -ne 0 ]]; then
    fatal "This script must be run as root"
fi

log "Setting up nstance-server on: $(hostname)"

# ---------------------------------------------------------------------------
# Validate shard config in object storage
# ---------------------------------------------------------------------------

log "Validating shard config in s3://${BUCKET}/shard/${SHARD}/config.jsonc..."
# AWS SDK requires a region even with custom endpoints.
: "${AWS_REGION:=${SHARD}}"
export AWS_REGION
if [[ -n "${S3_ENDPOINT}" ]]; then
    export AWS_ENDPOINT_URL="${S3_ENDPOINT}"
    export AWS_S3_USE_PATH_STYLE=true
fi
validate_output=$("${SERVER_BINARY}" --validate --storage s3 --bucket "${BUCKET}" --shard "${SHARD}" 2>&1) || \
    fatal "Shard config validation failed. Generate and upload it first with:
    ./create-shard-config.sh --vip ${VIP} --shard ${SHARD} --bucket ${BUCKET}${S3_ENDPOINT:+ --s3-endpoint ${S3_ENDPOINT}} --output config.jsonc"
while IFS= read -r line; do
    [[ -n "${line}" ]] && log "${line}"
done <<< "${validate_output}"

# ---------------------------------------------------------------------------
# Step 1: Configure apt repos
# ---------------------------------------------------------------------------

if [[ "${ENTERPRISE}" != true ]]; then
    log "Switching Proxmox to no-subscription repos..."
    if [[ "${DRY_RUN}" == true ]]; then
        log "(dry-run) Would disable enterprise repos and add pve-no-subscription"
    else
        for f in /etc/apt/sources.list.d/pve-enterprise.{list,sources} /etc/apt/sources.list.d/ceph.{list,sources}; do
            if [[ -f "$f" ]]; then
                mv "$f" "${f}.disabled"
                log "Disabled enterprise repo: ${f}"
            fi
        done
        if [[ ! -f /etc/apt/sources.list.d/pve-no-subscription.list ]]; then
            echo "deb http://download.proxmox.com/debian/pve $(grep VERSION_CODENAME /etc/os-release | cut -d= -f2) pve-no-subscription" \
                > /etc/apt/sources.list.d/pve-no-subscription.list
            log "Added pve-no-subscription repo"
        fi
    fi
fi

# ---------------------------------------------------------------------------
# Step 2: Create system user
# ---------------------------------------------------------------------------

if id nstance &>/dev/null; then
    log "User 'nstance' already exists"
else
    log "Creating system user 'nstance'..."
    if [[ "${DRY_RUN}" == true ]]; then
        log "(dry-run) Would create user 'nstance'"
    else
        useradd --system --no-create-home --shell /usr/sbin/nologin nstance
        log "User 'nstance' created"
    fi
fi

# ---------------------------------------------------------------------------
# Step 3: Create directories
# ---------------------------------------------------------------------------

log "Creating directories..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would create ${CONFIG_DIR}, ${CACHE_DIR}"
else
    mkdir -p "${CONFIG_DIR}"
    mkdir -p "${CACHE_DIR}"
    chown nstance:nstance "${CACHE_DIR}"
    chmod 0750 "${CACHE_DIR}"
fi

# ---------------------------------------------------------------------------
# Step 4: Install binary
# ---------------------------------------------------------------------------

log "Installing nstance-server to ${DEST_BINARY}..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would copy ${SERVER_BINARY} to ${DEST_BINARY}"
else
    cp "${SERVER_BINARY}" "${DEST_BINARY}"
    chmod 0755 "${DEST_BINARY}"
    log "Installed: ${DEST_BINARY}"
fi

# ---------------------------------------------------------------------------
# Step 5: Write encryption key file
# ---------------------------------------------------------------------------

if [[ -n "${ENCRYPTION_KEY}" ]]; then
    log "Copying encryption key to ${ENC_KEY_PATH}..."
    if [[ "${DRY_RUN}" == true ]]; then
        log "(dry-run) Would copy ${ENCRYPTION_KEY} to ${ENC_KEY_PATH}"
    else
        cp "${ENCRYPTION_KEY}" "${ENC_KEY_PATH}"
        chown root:nstance "${ENC_KEY_PATH}"
        chmod 0640 "${ENC_KEY_PATH}"
        log "Encryption key written (root:nstance 0640)"
    fi
else
    log "Using existing encryption key at ${ENC_KEY_PATH}"
    chown root:nstance "${ENC_KEY_PATH}"
    chmod 0640 "${ENC_KEY_PATH}"
fi

# ---------------------------------------------------------------------------
# Step 6: Write environment file (S3 + Proxmox credentials)
# ---------------------------------------------------------------------------

ENV_PATH="${CONFIG_DIR}/server.env"
log "Writing environment file to ${ENV_PATH}..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would write ${ENV_PATH}"
else
    cat > "${ENV_PATH}" <<ENV
# nstance-server environment — S3 and Proxmox credentials.
# These are loaded by systemd EnvironmentFile before the server starts.
AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID}
AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY}
AWS_REGION=${AWS_REGION}
PROXMOX_API_URL=${PROXMOX_API_URL}
PROXMOX_TOKEN_ID=${PROXMOX_TOKEN_ID}
PROXMOX_TOKEN_SECRET=${PROXMOX_TOKEN_SECRET}
ENV
    if [[ -n "${S3_ENDPOINT}" ]]; then
        cat >> "${ENV_PATH}" <<ENV
AWS_ENDPOINT_URL=${S3_ENDPOINT}
AWS_S3_USE_PATH_STYLE=true
ENV
    fi
    chown root:root "${ENV_PATH}"
    chmod 0600 "${ENV_PATH}"
    log "Environment file written (root:root 0600)"
fi

# ---------------------------------------------------------------------------
# Step 7: Write systemd unit
# ---------------------------------------------------------------------------

log "Writing systemd unit to ${UNIT_PATH}..."

# Build ExecStart command line.
# Note: S3 endpoint and path-style config are handled via AWS SDK environment
# variables (AWS_ENDPOINT_URL, AWS_S3_USE_PATH_STYLE) in the env file.
EXEC_START="${DEST_BINARY} --id %H --shard ${SHARD} --storage s3 --bucket ${BUCKET} --cachedir ${CACHE_DIR}"

if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would write ${UNIT_PATH}"
else
    cat > "${UNIT_PATH}" <<UNIT
[Unit]
Description=Nstance Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nstance
Group=nstance
EnvironmentFile=${ENV_PATH}
ExecStart=${EXEC_START}
Restart=on-failure
RestartSec=10
TimeoutStopSec=15

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${CACHE_DIR}

[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    log "Systemd unit written"
fi

# ---------------------------------------------------------------------------
# Step 8: Install keepalived
# ---------------------------------------------------------------------------

if dpkg -s keepalived &>/dev/null 2>&1; then
    log "keepalived already installed"
else
    log "Installing keepalived..."
    if [[ "${DRY_RUN}" == true ]]; then
        log "(dry-run) Would install keepalived"
    else
        apt-get update -qq
        apt-get install -y -qq keepalived > /dev/null
        log "keepalived installed"
    fi
fi

# ---------------------------------------------------------------------------
# Step 9: Write leader check script
# ---------------------------------------------------------------------------

log "Writing leader check script to ${CHECK_SCRIPT}..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would write ${CHECK_SCRIPT}"
else
    cat > "${CHECK_SCRIPT}" <<'CHECK'
#!/bin/bash
# Returns 0 (success) when the local nstance-server is the shard leader.
# The registration port (8992) only binds when this server wins S3lect
# leader election, so checking if it's listening is a reliable indicator.
ss -tlnH sport = :8992 | grep -q 8992
CHECK
    chmod 0755 "${CHECK_SCRIPT}"
    log "Check script written"
fi

# ---------------------------------------------------------------------------
# Step 10: Write keepalived config
# ---------------------------------------------------------------------------

log "Writing keepalived config to ${KEEPALIVED_CONF}..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would write ${KEEPALIVED_CONF}"
    log "(dry-run) VIP: ${VIP}/${VIP_CIDR} on ${INTERFACE}, router ID: ${ROUTER_ID}"
else
    mkdir -p /etc/keepalived
    cat > "${KEEPALIVED_CONF}" <<KEEPALIVED
# Managed by nstance — see server-with-keepalived.sh
#
# All nodes start as BACKUP with equal priority. The check script adds +50
# to whichever node is the current S3lect shard leader, causing keepalived
# to assign the VIP to that node.

global_defs {
    enable_script_security
    script_user nstance
}

vrrp_script check_nstance {
    script "${CHECK_SCRIPT}"
    interval 2
    fall 2
    rise 2
    weight 50
}

vrrp_instance NSTANCE {
    state BACKUP
    interface ${INTERFACE}
    virtual_router_id ${ROUTER_ID}
    priority 100
    advert_int 1

    virtual_ipaddress {
        ${VIP}/${VIP_CIDR}
    }

    track_script {
        check_nstance
    }
}
KEEPALIVED
    log "keepalived config written"
fi

# ---------------------------------------------------------------------------
# Step 11: Enable and start services
# ---------------------------------------------------------------------------

if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would enable and start nstance-server and keepalived"
else
    systemctl enable --now nstance-server
    systemctl enable --now keepalived
    log "Services enabled and started"
fi

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------

log ""
log "Setup complete on $(hostname)."
log ""
log "  nstance-server: systemctl status nstance-server"
log "  keepalived:     systemctl status keepalived"
log "  VIP:            ${VIP}/${VIP_CIDR} on ${INTERFACE}"
log "  Shard:          ${SHARD}"
log "  S3 bucket:      ${BUCKET}"
