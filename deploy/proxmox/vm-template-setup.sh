#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

# Bootstrap a Proxmox VE node for use with Nstance.
#
# This script is idempotent and can be run on each node in a cluster.
# It downloads the image URL (if not already present), finds or 
# creates a VM template from it using template name, and converts 
# it to a template.
#
# Usage:
#   ./vm-template-setup.sh [options]
#
# Options:
#   --template-name NAME    Template VM name (default: debian-13-template)
#   --image-url URL         Cloud image URL (default: Debian 13 Trixie)
#   --image-hash HASH       Verify image integrity (see below; set to "" to skip verification)
#   --storage POOL          Storage pool for VM disk (default: local-lvm)
#   --bridge BRIDGE         Network bridge (default: vmbr0)
#   --min-vmid ID           Minimum VMID to search for available template ID (default: 9000)
#   --iso-dir DIR           Directory for cloud image downloads (default: /var/lib/vz/template/iso)
#   --ci-user USER          Cloud-init default user (default: debian, set "" to skip)
#   --ip-config CONFIG      Cloud-init IP config (default: ip=dhcp, set "" to skip)
#   --dry-run               Show what would be done without making changes
#   --help                  Show this help message
#
# Image hash verification (--image-hash):
#   The value determines how the image checksum is obtained:
#     sha256:<hex>            Direct SHA-256 checksum
#     sha512:<hex>            Direct SHA-512 checksum
#     https://...             Download checksums file from this URL
#     /path/to/file           Read checksums from a local file
#   If set to empty (i.e. ""), no verification is performed. If provided and verification
#   fails for any reason (download error, missing entry, mismatch), the
#   script exits with an error.

TEMPLATE_NAME="debian-13-template"
IMAGE_URL="https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2"
IMAGE_HASH="https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS"
STORAGE="local-lvm"
BRIDGE="vmbr0"
MIN_VMID=9000
ISO_DIR="/var/lib/vz/template/iso"
CI_USER="debian"
IP_CONFIG="ip=dhcp"
DRY_RUN=false
MIN_PVE_VERSION="8.0"

usage() {
    sed -n '/^# Usage:/,/^$/p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

log() {
    echo "[nstance-bootstrap] $*"
}

fatal() {
    echo "[nstance-bootstrap] ERROR: $*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --template-name) TEMPLATE_NAME="$2"; shift 2 ;;
        --image-url)     IMAGE_URL="$2"; shift 2 ;;
        --image-hash)    IMAGE_HASH="$2"; shift 2 ;;
        --storage)       STORAGE="$2"; shift 2 ;;
        --bridge)        BRIDGE="$2"; shift 2 ;;
        --min-vmid)      MIN_VMID="$2"; shift 2 ;;
        --iso-dir)       ISO_DIR="$2"; shift 2 ;;
        --ci-user)       CI_USER="$2"; shift 2 ;;
        --ip-config)     IP_CONFIG="$2"; shift 2 ;;
        --dry-run)       DRY_RUN=true; shift ;;
        --help|-h)       usage ;;
        *)               fatal "Unknown option: $1" ;;
    esac
done

IMAGE_FILENAME=$(basename "${IMAGE_URL}")
IMAGE_PATH="${ISO_DIR}/${IMAGE_FILENAME}"

# Validate --image-hash flag
if [[ -n "${IMAGE_HASH}" ]]; then
    case "${IMAGE_HASH}" in
        sha256:*|sha512:*|https://*|/*) ;;
        *) fatal "Invalid --image-hash: expected sha256:HEX, sha512:HEX, https://URL, or /path/to/file" ;;
    esac
fi

# Ensure we're running on a Proxmox node with a supported version.
if ! command -v qm &>/dev/null; then
    fatal "qm not found. This script must be run on a Proxmox VE node."
fi
if ! command -v pvesh &>/dev/null; then
    fatal "pvesh not found. This script must be run on a Proxmox VE node."
fi
if ! command -v pveversion &>/dev/null; then
    fatal "pveversion not found. This script must be run on a Proxmox VE node."
fi
PVE_VERSION=$(pveversion | cut -d/ -f2)
if [[ $(printf '%s\n%s' "${MIN_PVE_VERSION}" "${PVE_VERSION}" | sort -V | head -n1) != "${MIN_PVE_VERSION}" ]]; then
    fatal "Proxmox VE version ${PVE_VERSION} is not supported. Minimum version required for 'import-from' syntax is ${MIN_PVE_VERSION}."
fi

log "Bootstrapping Proxmox node: $(hostname)"

# Step 1: Check if a template with this name already exists on this node.
# Each node needs its own local copy of the template for cloning.
EXISTING_VMID=""
while read -r vmid; do
    if qm config "${vmid}" 2>/dev/null | grep -q '^template: 1'; then
        EXISTING_VMID="${vmid}"
        break
    fi
done < <(qm list 2>/dev/null | awk -v name="${TEMPLATE_NAME}" '$2 == name { print $1 }')
if [[ -n "${EXISTING_VMID}" ]]; then
    log "Template '${TEMPLATE_NAME}' already exists as VMID ${EXISTING_VMID} on this node. Nothing to do."
    log ""
    log "To use this template in Nstance config:"
    log "  \"TemplateName\": \"${TEMPLATE_NAME}\""
    exit 0
fi

log "No existing template '${TEMPLATE_NAME}' found on this node."

# Step 2: Download the cloud image if not already present.
if [[ -f "${IMAGE_PATH}" ]]; then
    log "Cloud image already downloaded: ${IMAGE_PATH}"
else
    log "Downloading cloud image: ${IMAGE_URL}"
    if [[ "${DRY_RUN}" == true ]]; then
        log "(dry-run) Would download to ${IMAGE_PATH}"
    else
        mkdir -p "${ISO_DIR}"
        TEMP_PATH="${IMAGE_PATH}.part"
        trap 'rm -f "${TEMP_PATH}"' EXIT
        curl -fSL -o "${TEMP_PATH}" -- "${IMAGE_URL}"
        mv "${TEMP_PATH}" "${IMAGE_PATH}"
        trap - EXIT
        log "Download complete: ${IMAGE_PATH}"
    fi
fi

# Step 3: Verify image integrity.
if [[ -n "${IMAGE_HASH}" && -f "${IMAGE_PATH}" ]]; then
    HASH_ALGO=""
    EXPECTED=""
    SUMS=""
    case "${IMAGE_HASH}" in
        sha256:*) HASH_ALGO="sha256"; EXPECTED="${IMAGE_HASH#sha256:}" ;;
        sha512:*) HASH_ALGO="sha512"; EXPECTED="${IMAGE_HASH#sha512:}" ;;
        https://*) SUMS=$(curl -fsSL -- "${IMAGE_HASH}") || fatal "Failed to download: ${IMAGE_HASH}" ;;
        /*)        SUMS=$(cat "${IMAGE_HASH}" 2>/dev/null) || fatal "Failed to read: ${IMAGE_HASH}" ;;
    esac
    if [[ -n "${SUMS}" ]]; then
        EXPECTED=$(echo "${SUMS}" | tr -d '\r' \
            | awk -v f="${IMAGE_FILENAME}" '$2==f || $2=="*"f || $2=="./"f { print $1; exit }')
        [[ -n "${EXPECTED}" ]] || fatal "No checksum entry for '${IMAGE_FILENAME}' in checksums file"
        case "${#EXPECTED}" in
            64) HASH_ALGO="sha256" ;; 128) HASH_ALGO="sha512" ;;
            *) fatal "Unrecognised hash length (${#EXPECTED}) in checksums file" ;;
        esac
    fi
    [[ "${EXPECTED}" =~ ^[0-9a-fA-F]+$ ]] || fatal "Invalid checksum: ${EXPECTED}"
    ACTUAL=$("${HASH_ALGO}sum" "${IMAGE_PATH}" | awk '{print $1}')
    [[ "${ACTUAL}" == "${EXPECTED}" ]] || fatal "Checksum mismatch for ${IMAGE_PATH}
  expected: ${EXPECTED}
  actual:   ${ACTUAL}"
    log "Checksum verified (${HASH_ALGO})"
fi

# Step 4: Find the next available VMID starting from MIN_VMID.
# VMIDs are cluster-wide in Proxmox, so we must check across all nodes.
# pvesh returns all VMs/containers/templates in the cluster.
# If running this script on multiple nodes simultaneously, two nodes may select
# the same VMID. The second node's qm create will fail — simply re-run.
CLUSTER_VMIDS=$(pvesh get /cluster/resources --type vm --output-format json 2>/dev/null \
    | grep -oP '"vmid"\s*:\s*\K[0-9]+' | sort -n)
VMID="${MIN_VMID}"
while echo "${CLUSTER_VMIDS}" | grep -qw "${VMID}"; do
    VMID=$((VMID + 1))
done
log "Selected VMID: ${VMID}"
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would create template '${TEMPLATE_NAME}' with VMID ${VMID}"
    log "(dry-run) Storage: ${STORAGE}, Bridge: ${BRIDGE}"
    exit 0
fi

# If any step fails after initial VM creation but before completion, destroy the partial VM.
trap 'log "Cleaning up failed VM ${VMID}..."; qm destroy "${VMID}" --purge 2>/dev/null' ERR

# Step 5: Create the VM.
log "Creating VM ${VMID} (${TEMPLATE_NAME})..."
qm create "${VMID}" \
    --name "${TEMPLATE_NAME}" \
    --memory 2048 \
    --cores 2 \
    --net0 "virtio,bridge=${BRIDGE}" \
    --scsihw virtio-scsi-pci

# Step 6: Import the cloud image as the primary disk.
log "Importing disk from cloud image..."
qm set "${VMID}" --scsi0 "${STORAGE}:0,import-from=${IMAGE_PATH}"

# Step 7: Add cloud-init drive.
log "Adding cloud-init drive..."
qm set "${VMID}" --ide2 "${STORAGE}:cloudinit"

# Step 8: Configure boot order and serial console.
log "Configuring boot order and serial console..."
qm set "${VMID}" --boot order=scsi0
qm set "${VMID}" --serial0 socket --vga serial0

# Step 9: Enable QEMU guest agent.
log "Enabling QEMU guest agent..."
qm set "${VMID}" --agent enabled=1

# Step 10: Set cloud-init defaults (if configured).
if [[ -n "${CI_USER}" ]]; then
    log "Setting cloud-init user: ${CI_USER}"
    qm set "${VMID}" --ciuser "${CI_USER}"
fi
if [[ -n "${IP_CONFIG}" ]]; then
    log "Setting cloud-init IP config: ${IP_CONFIG}"
    qm set "${VMID}" --ipconfig0 "${IP_CONFIG}"
fi

# Step 11: Convert to template.
log "Converting VM to template..."
qm template "${VMID}"

# Reset trap and print success
trap - ERR
log ""
log "Template '${TEMPLATE_NAME}' created successfully (VMID ${VMID})."
log ""
log "To use this template in Nstance config:"
log "  \"TemplateName\": \"${TEMPLATE_NAME}\""
