#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
echo "=== Userdata Script Started at $(date) ==="

ARCH=$(dpkg --print-architecture)

# Provider-specific setup
%{ if provider == "aws" && enable_ssm ~}
# If enabled, ensure SSM Agent is installed and running
if command -v snap >/dev/null 2>&1 && snap list amazon-ssm-agent >/dev/null 2>&1; then
  snap start amazon-ssm-agent
elif dpkg -s amazon-ssm-agent >/dev/null 2>&1; then
  systemctl enable --now amazon-ssm-agent
else
  curl -fsSL -o /tmp/amazon-ssm-agent.deb \
    "https://s3.amazonaws.com/ec2-downloads-windows/SSMAgent/latest/debian_$${ARCH}/amazon-ssm-agent.deb"
  dpkg -i /tmp/amazon-ssm-agent.deb
  rm -f /tmp/amazon-ssm-agent.deb
  systemctl enable --now amazon-ssm-agent
fi
%{ endif ~}

# Install runtime dependencies
apt-get update -o Acquire::Retries=3
apt-get install -y -o Acquire::Retries=3 jq sqlite3

# Create data directory
mkdir -p /var/lib/nstance-server

# Get instance ID
%{ if provider == "gcp" ~}
INSTANCE_ID=$(curl -sf -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/name || true)
%{ else ~}
INSTANCE_ID=$(cat /var/lib/cloud/data/instance-id 2>/dev/null || true)
%{ endif ~}
if [ -z "$INSTANCE_ID" ]; then
  echo "ERROR: Failed to determine instance ID"
  exit 1
fi

# Determine download URL
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

  echo "Installing nstance-server $VERSION..."
  DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/$VERSION/nstance-server_$${VERSION#v}_linux_$ARCH.tar.gz"
fi

# Download and extract
echo "Downloading from: $DOWNLOAD_URL"
curl -sL "$DOWNLOAD_URL" | tar -xz -C /usr/local/bin nstance-server
chmod +x /usr/local/bin/nstance-server

# Create systemd service
cat > /etc/systemd/system/nstance-server.service <<SYSTEMD
[Unit]
Description=Nstance Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Environment=NSTANCE_PROVIDER=${provider}
Environment=AWS_REGION=${aws_region}
Environment=GCP_PROJECT=${gcp_project}
ExecStart=/usr/local/bin/nstance-server --storage ${storage} --bucket ${bucket} --shard ${shard} --id $INSTANCE_ID --cachedir /var/lib/nstance-server/cache
Restart=always
RestartSec=5
TimeoutStopSec=15
StandardOutput=journal
StandardError=journal

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/nstance-server

[Install]
WantedBy=multi-user.target
SYSTEMD

# Enable and start nstance-server service
systemctl daemon-reload
systemctl enable nstance-server
systemctl start nstance-server

echo "=== Userdata Script Completed at $(date) ==="
