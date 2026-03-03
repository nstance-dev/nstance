#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0
#
# This is a Go text/template processed by nstance-server.
# Available template variables:
#   .Instance.ID             - Instance ID (puidv7)
#   .Instance.Kind           - Template kind identifier
#   .Instance.Arch           - Architecture (arm64, amd64)
#   .Instance.Type           - Instance type (e.g., t4g.nano)
#   .Server.Shard            - Shard ID
#   .Server.RegistrationAddr - Registration service address (host:port)
#   .Server.AgentAddr        - Agent service address (host:port)
#   .Server.OperatorAddr     - Operator service address (host:port)
#   .Cluster.ID              - Cluster ID
#   .Cluster.CACert          - CA certificate PEM
#   .Provider.Kind           - Provider (aws, gcp, proxmox)
#   .Provider.Region         - Provider region
#   .Provider.Zone           - Provider zone
#   .Vars.EXAMPLE            - Custom variables from config
#   .Nonce                   - Registration nonce JWT
#
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

ARCH="{{ .Instance.Arch }}"

# SSH access (optional) - inject authorized keys for the specified user
SSH_USERNAME="{{ .Vars.SSH_USERNAME }}"
SSH_AUTHORIZED_KEYS="{{ .Vars.SSH_AUTHORIZED_KEYS }}"
if [ -n "$SSH_USERNAME" ] && [ -n "$SSH_AUTHORIZED_KEYS" ]; then
  if ! id "$SSH_USERNAME" &>/dev/null; then
    useradd --create-home --shell /bin/bash "$SSH_USERNAME"
    echo "Created user $SSH_USERNAME"
  fi
  SSH_DIR="/home/$SSH_USERNAME/.ssh"
  mkdir -p "$SSH_DIR"
  printf '%s\n' "$SSH_AUTHORIZED_KEYS" >> "$SSH_DIR/authorized_keys"
  chmod 700 "$SSH_DIR"
  chmod 600 "$SSH_DIR/authorized_keys"
  chown -R "$SSH_USERNAME:$SSH_USERNAME" "$SSH_DIR"
  echo "SSH keys installed for $SSH_USERNAME"
fi

# Provider-specific setup
%{ if provider == "aws" && enable_ssm ~}
# If enabled, ensure SSM Agent is installed and running
if command -v snap >/dev/null 2>&1 && snap list amazon-ssm-agent >/dev/null 2>&1; then
  snap start amazon-ssm-agent
elif dpkg -s amazon-ssm-agent >/dev/null 2>&1; then
  systemctl enable --now amazon-ssm-agent
else
  curl -fsSL -o /tmp/amazon-ssm-agent.deb \
    "https://s3.amazonaws.com/ec2-downloads-windows/SSMAgent/latest/debian_$ARCH/amazon-ssm-agent.deb"
  dpkg -i /tmp/amazon-ssm-agent.deb
  rm -f /tmp/amazon-ssm-agent.deb
  systemctl enable --now amazon-ssm-agent
fi
%{ endif ~}

# Logging
exec > >(tee /var/log/nstance-agent-init.log) 2>&1
echo "=== Nstance Agent Init $(date) ==="

# Server addresses - each service has its own host:port
REGISTRATION_ADDR="{{ .Server.RegistrationAddr }}"
AGENT_ADDR="{{ .Server.AgentAddr }}"
REGISTRATION_HOST="$${REGISTRATION_ADDR%:*}"
REGISTRATION_PORT="$${REGISTRATION_ADDR##*:}"

# Verify connectivity to nstance-server before starting nstance-agent
echo "Checking connectivity to nstance-server at $REGISTRATION_ADDR..."
attempt=0
while true
do
  attempt=$((attempt + 1))
  if timeout 5 bash -c "echo > /dev/tcp/$REGISTRATION_HOST/$REGISTRATION_PORT" 2>/dev/null
  then
    echo "Connection successful!"
    break
  fi
  retry_in=15
  if [ $attempt -lt 3 ]
  then
    retry_in=3
  fi
  echo "Failed to connect to nstance-server at $REGISTRATION_ADDR (attempt $attempt), retrying in $retry_in seconds..."
  sleep $retry_in
done

# Install dependencies
apt-get update -o Acquire::Retries=3
apt-get install -y -o Acquire::Retries=3 curl jq

# Provider-specific dependencies
%{ if provider == "proxmox" ~}
# Install QEMU Guest Agent for Proxmox VM management
apt-get install -y -o Acquire::Retries=3 qemu-guest-agent
systemctl start qemu-guest-agent
%{ endif ~}

# Create system user
useradd --system --no-create-home --shell /usr/sbin/nologin nstance

# Create directories
mkdir -p /opt/nstance-agent/identity
mkdir -p /opt/nstance-agent/keys
mkdir -p /opt/nstance-agent/recv
chown -R nstance:nstance /opt/nstance-agent

# Determine download URL for nstance-agent
BINARY_URL="${binary_url}"
if [ -n "$BINARY_URL" ]; then
  echo "Using fixed binary URL..."
  DOWNLOAD_URL="$BINARY_URL"
else
  VERSION="${nstance_version}"
  GITHUB_REPO="${github_repo}"

  if [ "$VERSION" = "latest" ]; then
    echo "Fetching latest release..."
    VERSION=$(curl -sL "https://api.github.com/repos/$GITHUB_REPO/releases/latest" | jq -r '.tag_name')
  fi

  echo "Installing nstance-agent $VERSION..."
  DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/$VERSION/nstance-agent_$${VERSION#v}_linux_$ARCH.tar.gz"
fi

# Download and extract nstance-agent binary
echo "Downloading from: $DOWNLOAD_URL"
curl -sL "$DOWNLOAD_URL" | tar -xz -C /usr/local/bin nstance-agent
chmod +x /usr/local/bin/nstance-agent

# Write registration nonce
cat > /opt/nstance-agent/identity/nonce.jwt <<'NONCE'
{{ .Nonce }}
NONCE
chmod 600 /opt/nstance-agent/identity/nonce.jwt

# Write CA certificate so nstance-agent trusts nstance-server
cat > /opt/nstance-agent/identity/ca.crt <<'CACERT'
{{ .Cluster.CACert }}
CACERT
chmod 600 /opt/nstance-agent/identity/ca.crt

# Create environment file
cat > /opt/nstance-agent/agent.env <<ENVFILE
NSTANCE_DEBUG=${agent_debug}
NSTANCE_ENVIRONMENT=${agent_environment}
NSTANCE_PROVIDER=${provider}
NSTANCE_SERVER_REGISTRATION_ADDR=$REGISTRATION_ADDR
NSTANCE_SERVER_AGENT_ADDR=$AGENT_ADDR
NSTANCE_IDENTITY_DIR=/opt/nstance-agent/identity
NSTANCE_KEYS_DIR=/opt/nstance-agent/keys
NSTANCE_RECV_DIR=/opt/nstance-agent/recv
NSTANCE_IDENTITY_MODE=${agent_identity_mode}
NSTANCE_KEYS_MODE=${agent_keys_mode}
NSTANCE_RECV_MODE=${agent_recv_mode}
NSTANCE_INSTANCE_KIND={{ .Instance.Kind }}
NSTANCE_INSTANCE_ID={{ .Instance.ID }}
NSTANCE_METRICS_INTERVAL=${agent_metrics_interval}
NSTANCE_SPOT_POLL_INTERVAL=${agent_spot_poll}
ENVFILE

# Fix ownership — all files above were written as root after the initial chown
chown -R nstance:nstance /opt/nstance-agent

# Create systemd service
cat > /etc/systemd/system/nstance-agent.service <<SYSTEMD
[Unit]
Description=Nstance Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nstance
EnvironmentFile=/opt/nstance-agent/agent.env
ExecStart=/usr/local/bin/nstance-agent
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/nstance-agent

[Install]
WantedBy=multi-user.target
SYSTEMD

# Enable and start service
systemctl daemon-reload
systemctl enable nstance-agent
systemctl start nstance-agent

echo "=== Nstance Agent Init Complete $(date) ==="
