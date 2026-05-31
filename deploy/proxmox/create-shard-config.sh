#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

# Generate a shard configuration file (JSONC) for nstance-server on Proxmox VE.
#
# This is a standalone config-generation script - it only produces a config 
# file and optionally (if rclone is installed) uploads it to S3 via rclone.
#
# Usage:
#   ./create-shard-config.sh [options]
#
# Options:
#   --vip ADDRESS              Virtual IP for leader services (required)
#   --shard NAME               Shard identifier (required, default: dev)
#   --bucket NAME              S3 bucket name (required)
#   --s3-endpoint URL          S3 endpoint for non-AWS backends (default: $AWS_ENDPOINT_URL)
#   --userdata PATH_OR_URL     Userdata script: a URL (http/https) or a local file path (default: inline echo stub)
#   --config-dir DIR           Configuration directory referenced in config (default: /etc/nstance)
#   --output FILE              Write generated config to FILE (default: config.jsonc)
#   --rclone                   Require rclone (script exits if rclone is not detected when this flag is set)
#   --ssh-username USER        SSH username to create and inject SSH_AUTHORIZED_KEYS for (default: admin)
#   --ssh-authorized-keys FILE Public key(s) file to inject as SSH_AUTHORIZED_KEYS var (a single .pub or an authorized_keys file)
#   --dry-run                  Show what would be done without making changes
#   --help                     Show this help message
#
# Required environment variables:
#   AWS_ACCESS_KEY_ID          S3 access key (for SeaweedFS/MinIO/AWS) — used for rclone upload
#   AWS_SECRET_ACCESS_KEY      S3 secret key — used for rclone upload
#
# Optional environment variables:
#   NSTANCE_CLUSTER_ID         Cluster ID (default: cls0000000001r010000000000000)
#   NSTANCE_REGION             Region name (default: same as shard)
#   NSTANCE_ZONE               Zone name (default: same as shard)
#
# Example:
#   export AWS_ACCESS_KEY_ID=admin
#   export AWS_SECRET_ACCESS_KEY=admin
#
#   ./create-shard-config.sh \
#       --vip 10.0.0.100 --shard dev --bucket nstance \
#       --s3-endpoint http://seaweedfs:8333 \
#       --userdata https://example.com/userdata.sh \
#       --output config.jsonc
#
#   # If rclone is installed, the script will upload config.jsonc automatically.
#   # Otherwise, upload config.jsonc to s3://nstance/shard/dev/config.jsonc

VIP=""
SHARD="dev"
BUCKET=""
S3_ENDPOINT="${AWS_ENDPOINT_URL:-}"
USERDATA=""
CONFIG_DIR="/etc/nstance"
DRY_RUN=false
REQUIRE_RCLONE=false
OUTPUT_FILE="config.jsonc"
SSH_USERNAME="admin"
SSH_AUTHORIZED_KEYS_FILE=""

usage() {
    sed -n '/^# Usage:/,/^$/p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

log() {
    echo "[nstance-config] $*"
}

fatal() {
    echo "[nstance-config] ERROR: $*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --vip)            VIP="$2"; shift 2 ;;
        --shard)          SHARD="$2"; shift 2 ;;
        --bucket)         BUCKET="$2"; shift 2 ;;
        --s3-endpoint)    S3_ENDPOINT="$2"; shift 2 ;;
        --userdata)       USERDATA="$2"; shift 2 ;;
        --config-dir)     CONFIG_DIR="$2"; shift 2 ;;
        --output)         OUTPUT_FILE="$2"; shift 2 ;;
        --rclone)         REQUIRE_RCLONE=true; shift ;;
        --ssh-username)    SSH_USERNAME="$2"; shift 2 ;;
        --ssh-authorized-keys) SSH_AUTHORIZED_KEYS_FILE="$2"; shift 2 ;;
        --dry-run)        DRY_RUN=true; shift ;;
        --help|-h)        usage ;;
        *)                fatal "Unknown option: $1" ;;
    esac
done

# Read optional env vars with defaults.
CLUSTER_ID="${NSTANCE_CLUSTER_ID:-cls0000000001r010000000000000}"
REGION="${NSTANCE_REGION:-${SHARD}}"
ZONE="${NSTANCE_ZONE:-${SHARD}}"

# ---------------------------------------------------------------------------
# Validate required arguments.
# ---------------------------------------------------------------------------

[[ -z "${VIP}" ]]    && fatal "--vip is required"
[[ -z "${SHARD}" ]]  && fatal "--shard is required"
[[ -z "${BUCKET}" ]] && fatal "--bucket is required"

if [[ "${REQUIRE_RCLONE}" == true ]] && ! command -v rclone &>/dev/null; then
    fatal "--rclone was specified but rclone is not installed"
fi

SSH_AUTHORIZED_KEYS=""
if [[ -n "${SSH_AUTHORIZED_KEYS_FILE}" ]]; then
    [[ -f "${SSH_AUTHORIZED_KEYS_FILE}" ]] || fatal "SSH authorized keys file not found: ${SSH_AUTHORIZED_KEYS_FILE}"
    SSH_AUTHORIZED_KEYS=$(awk '{printf "%s\\n", $0}' "${SSH_AUTHORIZED_KEYS_FILE}" | sed 's/\\n$//')
    log "Loaded SSH authorized keys from ${SSH_AUTHORIZED_KEYS_FILE}"
fi

# Build the userdata JSON object.
if [[ -z "${USERDATA}" ]]; then
    USERDATA_JSON='{"content": "#!/bin/bash\necho \"Instance started\""}'
elif [[ "${USERDATA}" =~ ^https?:// ]]; then
    USERDATA_JSON=$(printf '{"source": "url", "content": "%s"}' "${USERDATA}")
else
    [[ -f "${USERDATA}" ]] || fatal "Userdata file not found: ${USERDATA}"
    userdata_content=$(awk '
        BEGIN { ORS="" }
        { gsub(/\\/, "\\\\"); gsub(/"/, "\\\""); gsub(/\t/, "\\t")
          if (NR>1) printf "\\n"; print }
    ' "${USERDATA}")
    USERDATA_JSON=$(printf '{"content": "%s"}' "${userdata_content}")
fi

# ---------------------------------------------------------------------------
# Generate shard JSONC config.
# ---------------------------------------------------------------------------

# Use sed to substitute __PLACEHOLDERS__ while preserving ${VAR} literals.
# The heredoc is piped directly to sed (not inside $(...)) for bash 3.2 compat.
_tmpconfig=$(mktemp)
trap 'rm -f "${_tmpconfig}"' EXIT
sed \
    -e "s|__CLUSTER_ID__|${CLUSTER_ID}|g" \
    -e "s|__CONFIG_DIR__|${CONFIG_DIR}|g" \
    -e "s|__SHARD__|${SHARD}|g" \
    -e "s|__REGION__|${REGION}|g" \
    -e "s|__ZONE__|${ZONE}|g" \
    -e "s|__VIP__|${VIP}|g" \
    <<'JSONC' > "${_tmpconfig}"
{
  // Nstance Server shard configuration for Proxmox VE with keepalived.
  "cluster": {
    "id": "__CLUSTER_ID__",
    "secrets": {
      "provider": "object-storage",
      "prefix": "secret/",
      "encryption_key": {
        "provider": "file",
        "source": "__CONFIG_DIR__/encryption.key"
      }
    }
  },
  "shard": {
    "id": "__SHARD__",
    "infra": {
      "provider": "proxmox",
      "region": "__REGION__",
      "zone": "__ZONE__",
      "options": {
        "cloud_init_iso_storage": "local",
        "insecure_tls": true
      }
    },
    "bind": {
      "health_addr": "0.0.0.0:8990",
      "election_addr": "0.0.0.0:8991",
      "registration_addr": "0.0.0.0:8992",
      "operator_addr": "0.0.0.0:8993",
      "agent_addr": "0.0.0.0:8994"
    },
    "advertise": {
      // health/election addrs are auto-detected (each node's real IP for peer health checks)
      "health_addr": ":8990",
      "election_addr": ":8991",
      // keepalived VIP — agents connect here
      "registration_addr": "__VIP__:8992",
      "operator_addr": "__VIP__:8993",
      "agent_addr": "__VIP__:8994"
    },
    "subnet_pools": {
      "default": ["vmbr1"]
    },
    "default_drain_timeout": "5m",
    "garbage_collection": {
      "interval": "2m",
      "registration_timeout": "5m"
    }
  },
  "defaults": {
    "args": {
      "StoragePool": "local-lvm",
      "TemplateName": "debian-13-template",
      "Bridge": "vmbr1",
      "Cores": 2,
      "Memory": 2048,
      "DiskSize": "20G",
      "StartOnBoot": true
    },
    "vars": {
      "Environment": "production",
      "ClusterSlug": "__SHARD__",
      "ClusterFQDN": "__SHARD__.nstance.local"__SSH_VARS__
    }
  },
  "templates": {
    "default": {
      "kind": "tst",
      "arch": "amd64",
      "userdata": __USERDATA__,
      "subnet_pool": "default"
    }
  },
  "groups": {
    "default": {
      "workers": {
        "template": "default",
        "size": 1,
        "subnet_pool": "default"
      }
    }
  }
}
JSONC

# Replace remaining placeholders with bash parameter substitution.
config=$(cat "${_tmpconfig}")
config="${config/__USERDATA__/${USERDATA_JSON}}"

# Conditionally inject SSH vars.
if [[ -n "${SSH_AUTHORIZED_KEYS}" ]]; then
    config="${config/__SSH_VARS__/,
      \"SSH_USERNAME\": \"${SSH_USERNAME}\",
      \"SSH_AUTHORIZED_KEYS\": \"${SSH_AUTHORIZED_KEYS}\"}"
else
    config="${config/__SSH_VARS__/}"
fi

s3_dest="shard/${SHARD}/config.jsonc"

# Output the config: to a file or stdout.
if [[ -n "${OUTPUT_FILE}" ]]; then
    if [[ "${DRY_RUN}" == true ]]; then
        log "(dry-run) Would write config to ${OUTPUT_FILE}"
    else
        printf '%s\n' "${config}" > "${OUTPUT_FILE}"
        log "Config written to ${OUTPUT_FILE}"
    fi

    # Attempt S3 upload via rclone if endpoint and bucket are available.
    if [[ -n "${S3_ENDPOINT}" && -n "${BUCKET}" ]]; then
        local_rclone_dest="nstance:${BUCKET}/${s3_dest}"
        if command -v rclone &>/dev/null; then
            log "Uploading config to s3://${BUCKET}/${s3_dest} via rclone..."
            if [[ "${DRY_RUN}" == true ]]; then
                log "(dry-run) Would run: rclone copyto ${OUTPUT_FILE} ${local_rclone_dest}"
            else
                RCLONE_CONFIG_NSTANCE_TYPE=s3 \
                RCLONE_CONFIG_NSTANCE_PROVIDER=Other \
                RCLONE_CONFIG_NSTANCE_ENDPOINT="${S3_ENDPOINT}" \
                RCLONE_CONFIG_NSTANCE_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-}" \
                RCLONE_CONFIG_NSTANCE_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-}" \
                RCLONE_CONFIG_NSTANCE_FORCE_PATH_STYLE=true \
                    rclone copyto "${OUTPUT_FILE}" "${local_rclone_dest}"
                log "Config uploaded to s3://${BUCKET}/${s3_dest}"
            fi
        else
            log ""
            log "rclone is not installed — upload the config manually:"
            log ""
            log "  # Using rclone:"
            log "  RCLONE_CONFIG_NSTANCE_TYPE=s3 \\"
            log "  RCLONE_CONFIG_NSTANCE_PROVIDER=Other \\"
            log "  RCLONE_CONFIG_NSTANCE_ENDPOINT=${S3_ENDPOINT} \\"
            log "  RCLONE_CONFIG_NSTANCE_ACCESS_KEY_ID=\${AWS_ACCESS_KEY_ID} \\"
            log "  RCLONE_CONFIG_NSTANCE_SECRET_ACCESS_KEY=\${AWS_SECRET_ACCESS_KEY} \\"
            log "  RCLONE_CONFIG_NSTANCE_FORCE_PATH_STYLE=true \\"
            log "    rclone copyto ${OUTPUT_FILE} ${local_rclone_dest}"
            log ""
            log "  # Using aws cli:"
            log "  aws s3 cp ${OUTPUT_FILE} s3://${BUCKET}/${s3_dest} --endpoint-url ${S3_ENDPOINT}"
            log ""
            log "  # Using mc (MinIO client):"
            log "  mc alias set nstance ${S3_ENDPOINT} \${AWS_ACCESS_KEY_ID} \${AWS_SECRET_ACCESS_KEY}"
            log "  mc cp ${OUTPUT_FILE} nstance/${BUCKET}/${s3_dest}"
        fi
    fi
else
    # Stdout mode — just print the config.
    printf '%s\n' "${config}"
    echo >&2 ""
    echo >&2 "[nstance-config] Config printed to stdout."
    echo >&2 "[nstance-config] Upload it to: s3://${BUCKET}/${s3_dest}"
    echo >&2 "[nstance-config] Tip: use --output FILE to write to a file and optionally upload via rclone."
fi
