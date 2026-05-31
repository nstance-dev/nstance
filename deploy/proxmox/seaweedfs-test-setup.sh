#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

# Install SeaweedFS as a single-node S3-compatible object storage service.
#
# This script downloads the SeaweedFS binary, creates a systemd service,
# and optionally pre-creates an S3 bucket. Intended for testing/development
# on a single Proxmox node — not for production use.
#
# Data is stored on disk and survives restarts. The S3 gateway requires no
# authentication by default (any credentials work).
#
# Usage:
#   ./seaweedfs-test-setup.sh [options]
#
# Options:
#   --port PORT        S3 gateway port (default: 8333)
#   --dir DIR          Data directory (default: /var/lib/seaweedfs)
#   --bucket NAME      Pre-create this S3 bucket after starting (optional)
#   --version VERSION  SeaweedFS version to install (default: latest)
#   --bind ADDRESS     Address to bind to (default: auto; e.g. 0.0.0.0)
#   --install-dir DIR  Binary install directory (default: /usr/local/bin)
#   --uninstall        Stop service, remove binary, unit file, and data
#   --dry-run          Show what would be done without making changes
#   --help             Show this help message
#
# Examples:
#   # Install and pre-create the "nstance" bucket:
#   ./seaweedfs-test-setup.sh --bucket nstance
#
#   # Install a specific version:
#   ./seaweedfs-test-setup.sh --version 3.78 --bucket nstance
#
#   # Uninstall and remove all data:
#   ./seaweedfs-test-setup.sh --uninstall

PORT="8333"
DATA_DIR="/var/lib/seaweedfs"
BUCKET=""
VERSION=""
BIND=""
INSTALL_DIR="/usr/local/bin"
UNINSTALL=false
DRY_RUN=false

SERVICE_NAME="seaweedfs"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

usage() {
    sed -n '/^# Usage:/,/^$/p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

log() {
    echo "[seaweedfs-setup] $*"
}

fatal() {
    echo "[seaweedfs-setup] ERROR: $*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --port)        PORT="$2"; shift 2 ;;
        --dir)         DATA_DIR="$2"; shift 2 ;;
        --bucket)      BUCKET="$2"; shift 2 ;;
        --version)     VERSION="$2"; shift 2 ;;
        --bind)        BIND="$2"; shift 2 ;;
        --install-dir) INSTALL_DIR="$2"; shift 2 ;;
        --uninstall)   UNINSTALL=true; shift ;;
        --dry-run)     DRY_RUN=true; shift ;;
        --help|-h)     usage ;;
        *)             fatal "Unknown option: $1" ;;
    esac
done

BINARY_PATH="${INSTALL_DIR}/weed"

# ---------------------------------------------------------------------------
# Uninstall
# ---------------------------------------------------------------------------

if [[ "${UNINSTALL}" == true ]]; then
    [[ "$(id -u)" -ne 0 ]] && fatal "This script must be run as root"

    log "Uninstalling SeaweedFS..."

    if systemctl is-active --quiet "${SERVICE_NAME}" 2>/dev/null; then
        log "Stopping ${SERVICE_NAME} service..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would stop ${SERVICE_NAME}"
        else
            systemctl stop "${SERVICE_NAME}"
            log "Service stopped"
        fi
    fi

    if systemctl is-enabled --quiet "${SERVICE_NAME}" 2>/dev/null; then
        log "Disabling ${SERVICE_NAME} service..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would disable ${SERVICE_NAME}"
        else
            systemctl disable "${SERVICE_NAME}"
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

    if [[ -f "${BINARY_PATH}" ]]; then
        log "Removing binary ${BINARY_PATH}..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would remove ${BINARY_PATH}"
        else
            rm -f "${BINARY_PATH}"
            log "Binary removed"
        fi
    fi

    if [[ -d "${DATA_DIR}" ]]; then
        log "Removing data directory ${DATA_DIR}..."
        if [[ "${DRY_RUN}" == true ]]; then
            log "(dry-run) Would remove ${DATA_DIR}"
        else
            rm -rf "${DATA_DIR}"
            log "Data directory removed"
        fi
    fi

    log "Uninstall complete."
    exit 0
fi

# ---------------------------------------------------------------------------
# Install
# ---------------------------------------------------------------------------

[[ "$(id -u)" -ne 0 ]] && fatal "This script must be run as root"

# Detect architecture.
ARCH=$(uname -m)
case "${ARCH}" in
    x86_64)  ARCH_SUFFIX="amd64" ;;
    aarch64) ARCH_SUFFIX="arm64" ;;
    arm64)   ARCH_SUFFIX="arm64" ;;
    *)       fatal "Unsupported architecture: ${ARCH}" ;;
esac

# Resolve download URL.
if [[ -n "${VERSION}" ]]; then
    DOWNLOAD_URL="https://github.com/seaweedfs/seaweedfs/releases/download/${VERSION}/linux_${ARCH_SUFFIX}_full.tar.gz"
else
    DOWNLOAD_URL="https://github.com/seaweedfs/seaweedfs/releases/latest/download/linux_${ARCH_SUFFIX}_full.tar.gz"
fi

# Step 1: Download and install binary.
if [[ -f "${BINARY_PATH}" ]]; then
    log "SeaweedFS binary already exists at ${BINARY_PATH}"
    log "  To reinstall, remove it first or use --uninstall"
else
    log "Downloading SeaweedFS from ${DOWNLOAD_URL}..."
    if [[ "${DRY_RUN}" == true ]]; then
        log "(dry-run) Would download and install to ${BINARY_PATH}"
    else
        TMP_DIR=$(mktemp -d)
        trap 'rm -rf "${TMP_DIR}"' EXIT
        curl -fSL "${DOWNLOAD_URL}" | tar xz -C "${TMP_DIR}"
        mv "${TMP_DIR}/weed" "${BINARY_PATH}"
        chmod 0755 "${BINARY_PATH}"
        rm -rf "${TMP_DIR}"
        trap - EXIT
        log "Installed: ${BINARY_PATH}"
    fi
fi

# Step 2: Create data directory.
log "Creating data directory ${DATA_DIR}..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would create ${DATA_DIR}"
else
    mkdir -p "${DATA_DIR}"
    log "Data directory ready"
fi

# Step 3: Write systemd unit.
log "Writing systemd unit to ${UNIT_PATH}..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would write ${UNIT_PATH}"
else
    cat > "${UNIT_PATH}" <<UNIT
[Unit]
Description=SeaweedFS Server (single-node S3)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${BINARY_PATH} server${BIND:+ -ip.bind ${BIND}} -s3 -dir ${DATA_DIR} -s3.port=${PORT}
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    log "Systemd unit written"
fi

# Step 4: Enable and start service.
log "Enabling and starting ${SERVICE_NAME} service..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would enable and start ${SERVICE_NAME}"
else
    systemctl enable --now "${SERVICE_NAME}"
    log "Service started"
fi

# Step 5: Pre-create bucket if requested.
if [[ -n "${BUCKET}" ]]; then
    log "Waiting for S3 gateway to be ready..."
    if [[ "${DRY_RUN}" == true ]]; then
        log "(dry-run) Would create bucket '${BUCKET}'"
    else
        # Wait for the S3 port to accept connections.
        for i in $(seq 1 30); do
            if curl -sf -o /dev/null "http://localhost:${PORT}" 2>/dev/null; then
                break
            fi
            if [[ "${i}" -eq 30 ]]; then
                fatal "S3 gateway did not become ready within 30 seconds"
            fi
            sleep 1
        done
        if curl -sf -o /dev/null -I "http://localhost:${PORT}/${BUCKET}" 2>/dev/null; then
            log "Bucket '${BUCKET}' already exists"
        else
            curl -sf -X PUT "http://localhost:${PORT}/${BUCKET}" > /dev/null
            log "Bucket '${BUCKET}' created"
        fi
    fi
fi

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------

log ""
log "SeaweedFS is running on $(hostname)."
log ""
log "  Service:   systemctl status ${SERVICE_NAME}"
log "  S3 endpoint: http://localhost:${PORT}"
log "  Data dir:  ${DATA_DIR}"
if [[ -n "${BUCKET}" ]]; then
log "  Bucket:    ${BUCKET}"
fi
log ""
log "Any S3 credentials will work (no auth enforced)."
log "To remove: $0 --uninstall"
