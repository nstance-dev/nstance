#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

# Install dnsmasq as a lightweight DHCP server on a bridge interface.
#
# Intended for testing/development on a single node — not for production use.
#
# This script installs dnsmasq, creates a systemd service scoped to a
# single bridge (e.g. vmbr1), and hands out DHCP leases to VMs on that
# network.
#
# Usage:
#   ./dnsmasq-test-setup.sh [options]
#
# Options:
#   --interface IFACE  Bridge to serve DHCP on (default: vmbr1)
#   --range-start IP   First IP in DHCP range (default: auto from interface)
#   --range-end IP     Last IP in DHCP range (default: auto from interface)
#   --lease-time TIME  DHCP lease duration (default: 1h)
#   --dns SERVERS      Comma-separated DNS servers (default: auto from /etc/resolv.conf)
#   --domain DOMAIN    DNS search domain (default: nstance.local)
#   --uninstall        Stop service, remove config and package
#   --dry-run          Show what would be done without making changes
#   --help             Show this help message
#
# Examples:
#   # Install with defaults (auto-detect from vmbr1):
#   ./dnsmasq-test-setup.sh
#
#   # Install on a custom interface and range:
#   ./dnsmasq-test-setup.sh --interface vmbr2 --range-start 10.0.0.100 --range-end 10.0.0.200
#
#   # Uninstall:
#   ./dnsmasq-test-setup.sh --uninstall

INTERFACE="vmbr1"
RANGE_START=""
RANGE_END=""
LEASE_TIME="1h"
DNS=""
DOMAIN="nstance.local"
UNINSTALL=false
DRY_RUN=false

SERVICE_NAME="dnsmasq-nstance"
CONF_PATH="/etc/dnsmasq.d/${SERVICE_NAME}.conf"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

usage() {
    sed -n '/^# Usage:/,/^$/p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

log() {
    echo "[dnsmasq-setup] $*"
}

fatal() {
    echo "[dnsmasq-setup] ERROR: $*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --interface)    INTERFACE="$2"; shift 2 ;;
        --range-start)  RANGE_START="$2"; shift 2 ;;
        --range-end)    RANGE_END="$2"; shift 2 ;;
        --lease-time)   LEASE_TIME="$2"; shift 2 ;;
        --dns)          DNS="$2"; shift 2 ;;
        --domain)       DOMAIN="$2"; shift 2 ;;
        --uninstall)    UNINSTALL=true; shift ;;
        --dry-run)      DRY_RUN=true; shift ;;
        --help|-h)      usage ;;
        *)              fatal "Unknown option: $1" ;;
    esac
done

# ---------------------------------------------------------------------------
# Uninstall
# ---------------------------------------------------------------------------

if [[ "${UNINSTALL}" == true ]]; then
    [[ "$(id -u)" -ne 0 ]] && fatal "This script must be run as root"

    log "Uninstalling ${SERVICE_NAME}..."

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

    for f in "${UNIT_PATH}" "${CONF_PATH}"; do
        if [[ -f "${f}" ]]; then
            log "Removing ${f}..."
            if [[ "${DRY_RUN}" == true ]]; then
                log "(dry-run) Would remove ${f}"
            else
                rm -f "${f}"
                log "Removed ${f}"
            fi
        fi
    done

    systemctl daemon-reload 2>/dev/null || true

    log "Uninstall complete."
    log "dnsmasq package was left installed. Remove manually with: apt-get remove --purge dnsmasq-base"
    exit 0
fi

# ---------------------------------------------------------------------------
# Install
# ---------------------------------------------------------------------------

[[ "$(id -u)" -ne 0 ]] && fatal "This script must be run as root"

# Verify interface exists.
if ! ip link show "${INTERFACE}" &>/dev/null; then
    fatal "Interface ${INTERFACE} does not exist. Create the bridge first."
fi

# Auto-detect network from the interface address.
IFACE_ADDR=$(ip -4 addr show "${INTERFACE}" | grep -oP 'inet \K[0-9.]+(?=/)' | head -1)

if [[ -z "${IFACE_ADDR}" ]]; then
    fatal "No IPv4 address found on ${INTERFACE}. Assign one first."
fi

log "Detected ${INTERFACE} address: ${IFACE_ADDR}"

# Compute network base for default range (e.g. 10.185.182.0 -> range .100-.250).
IFS='.' read -r a b c _ <<< "${IFACE_ADDR}"
NETWORK_BASE="${a}.${b}.${c}"

if [[ -z "${RANGE_START}" ]]; then
    RANGE_START="${NETWORK_BASE}.100"
fi
if [[ -z "${RANGE_END}" ]]; then
    RANGE_END="${NETWORK_BASE}.250"
fi

# Auto-detect DNS from resolv.conf if not specified.
if [[ -z "${DNS}" ]]; then
    DNS=$(grep -m1 '^nameserver' /etc/resolv.conf | awk '{print $2}')
    if [[ -z "${DNS}" ]]; then
        DNS="${IFACE_ADDR}"
    fi
fi

log "DHCP range: ${RANGE_START} - ${RANGE_END} (lease ${LEASE_TIME})"
log "Gateway/router: ${IFACE_ADDR}"
log "DNS: ${DNS}"
log "Domain: ${DOMAIN}"

# Step 1: Install dnsmasq if needed.
if ! command -v dnsmasq &>/dev/null; then
    log "Installing dnsmasq..."
    if [[ "${DRY_RUN}" == true ]]; then
        log "(dry-run) Would install dnsmasq"
    else
        apt-get update -qq
        apt-get install -y -qq dnsmasq-base > /dev/null
        log "dnsmasq installed"
    fi
else
    log "dnsmasq already installed"
fi

# Disable the default dnsmasq service if it exists (we run our own instance).
if systemctl is-enabled --quiet dnsmasq 2>/dev/null; then
    log "Disabling default dnsmasq service..."
    if [[ "${DRY_RUN}" != true ]]; then
        systemctl disable --now dnsmasq 2>/dev/null || true
    fi
fi

# Step 2: Write dnsmasq config.
log "Writing config to ${CONF_PATH}..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would write ${CONF_PATH}"
else
    mkdir -p "$(dirname "${CONF_PATH}")"
    cat > "${CONF_PATH}" <<CONF
# dnsmasq DHCP config for nstance dev/test.
# Scoped to ${INTERFACE} only — does not affect other networks.

# Only listen on the bridge interface.
interface=${INTERFACE}
bind-interfaces

# DHCP range.
dhcp-range=${RANGE_START},${RANGE_END},${LEASE_TIME}

# Gateway — point VMs at this node.
dhcp-option=option:router,${IFACE_ADDR}

# DNS servers.
dhcp-option=option:dns-server,${DNS}

# Search domain.
dhcp-option=option:domain-search,${DOMAIN}

# No DNS server (DHCP only). Avoids conflicting with system DNS.
port=0

# Lease file.
dhcp-leasefile=/var/lib/misc/${SERVICE_NAME}.leases

# Log DHCP transactions to syslog.
log-dhcp
CONF
    log "Config written"
fi

# Step 3: Write systemd unit.
log "Writing systemd unit to ${UNIT_PATH}..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would write ${UNIT_PATH}"
else
    cat > "${UNIT_PATH}" <<UNIT
[Unit]
Description=dnsmasq DHCP for nstance (${INTERFACE})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/sbin/dnsmasq --keep-in-foreground --conf-file=${CONF_PATH} --log-facility=-
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    log "Systemd unit written"
fi

# Step 4: Create lease file directory and start.
log "Enabling and starting ${SERVICE_NAME} service..."
if [[ "${DRY_RUN}" == true ]]; then
    log "(dry-run) Would enable and start ${SERVICE_NAME}"
else
    mkdir -p /var/lib/misc
    systemctl enable --now "${SERVICE_NAME}"
    log "Service started"
fi

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------

log ""
log "dnsmasq DHCP is running on $(hostname)."
log ""
log "  Service:    systemctl status ${SERVICE_NAME}"
log "  Interface:  ${INTERFACE}"
log "  DHCP range: ${RANGE_START} - ${RANGE_END}"
log "  Gateway:    ${IFACE_ADDR}"
log "  DNS:        ${DNS}"
log "  Config:     ${CONF_PATH}"
log ""
log "Only run this on ONE node per subnet."
log "To remove: $0 --uninstall"
