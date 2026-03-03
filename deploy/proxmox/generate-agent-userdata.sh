#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

# Load the agent-userdata template from the given URL or path and
# render it with Proxmox defaults using OpenTofu. The Go text/template
# variables ({{ .Server.RegistrationAddr }}, {{ .Nonce }}, etc.) are left intact for
# nstance-server to process at instance-creation time.
#
# Usage:
#   ./generate-agent-userdata.sh [SOURCE] [options]
#
# Arguments:
#   SOURCE             Template source: a local file path, a URL, or omit to
#                      fetch from the default GitHub repo (default: GitHub)
#
# Options:
#   --ref REF          Git ref when fetching from GitHub (default: main)
#   --output FILE      Output file (default: agent-userdata.sh)
#   --binary-url URL   Override the nstance-agent download URL
#   --dry-run          Print the rendered script to stdout instead of writing
#   --help             Show this help message

REPO="nstance-dev/terraform-development"
TEMPLATE_PATH="deploy/tf/common/shard/templates/agent-userdata.sh.tpl"
REF="main"
SOURCE=""
OUTPUT_FILE="agent-userdata.sh"
BINARY_URL=""
DRY_RUN=false

usage() {
    sed -n '/^# Usage:/,/^$/p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

log() { echo "[generate-userdata] $*"; }
fatal() { echo "[generate-userdata] ERROR: $*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --ref)        REF="$2"; shift 2 ;;
        --output)     OUTPUT_FILE="$2"; shift 2 ;;
        --binary-url) BINARY_URL="$2"; shift 2 ;;
        --dry-run)    DRY_RUN=true; shift ;;
        --help|-h)  usage ;;
        *)
            if [[ -z "${SOURCE}" && "$1" != -* ]]; then
                SOURCE="$1"; shift
            else
                fatal "Unknown option: $1"
            fi
            ;;
    esac
done

command -v tofu &>/dev/null || fatal "OpenTofu (tofu) is required but not installed"

# Resolve the template source.
if [[ -z "${SOURCE}" ]]; then
    # Default: fetch from GitHub.
    SOURCE="https://raw.githubusercontent.com/${REPO}/${REF}/${TEMPLATE_PATH}"
fi

if [[ "${SOURCE}" =~ ^https?:// ]]; then
    command -v curl &>/dev/null || fatal "curl is required to fetch remote templates"
    log "Fetching template from ${SOURCE}..."
    template=$(curl -fsSL "${SOURCE}") || fatal "Failed to fetch template from ${SOURCE}"
else
    [[ -f "${SOURCE}" ]] || fatal "Template file not found: ${SOURCE}"
    log "Reading template from ${SOURCE}..."
    template=$(cat "${SOURCE}")
fi

# Render with tofu console — write the template to a temp file so
# templatefile() can read it.
tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT
printf '%s' "${template}" > "${tmpdir}/agent-userdata.sh.tpl"

rendered=$(cd "${tmpdir}" && tofu console <<EOF
templatefile("agent-userdata.sh.tpl", {
  nstance_version        = "latest"
  github_repo            = "nstance-dev/nstance"
  binary_url             = "${BINARY_URL}"
  provider               = "proxmox"
  enable_ssm             = false
  agent_debug            = false
  agent_environment      = "production"
  agent_identity_mode    = "0600"
  agent_keys_mode        = "0640"
  agent_recv_mode        = "0640"
  agent_metrics_interval = "30s"
  agent_spot_poll        = "5s"
})
EOF
) || fatal "tofu console failed"

# tofu console wraps the output in <<EOT ... EOT — strip those lines.
rendered=$(printf '%s\n' "${rendered}" | sed '1{/^<<EOT$/d;}; ${/^EOT$/d;}')

# Prepend a source comment (insert after the shebang line).
rendered=$(printf '%s\n' "${rendered}" | sed "1a\\
# WARNING: Generated from https://github.com/${REPO} — do not edit directly.\\
# \\
")

if [[ "${DRY_RUN}" == true ]]; then
    printf '%s\n' "${rendered}"
    log "(dry-run) Would write to ${OUTPUT_FILE}"
else
    printf '%s\n' "${rendered}" > "${OUTPUT_FILE}"
    chmod +x "${OUTPUT_FILE}"
    log "Written to ${OUTPUT_FILE}"
fi
