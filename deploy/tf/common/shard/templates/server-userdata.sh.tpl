#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
echo "=== Userdata Script Started at $(date) ==="

ARCH=$(dpkg --print-architecture)

for command in curl python3; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "ERROR: Debian 13 image is missing required command: $command" >&2
    exit 1
  fi
done

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
apt-get install -y -o Acquire::Retries=3 sqlite3

# Create data directory
mkdir -p /var/lib/nstance-server

# Get instance ID
%{ if provider == "google" ~}
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
    VERSION=$(curl -fsSL "https://api.github.com/repos/$GITHUB_REPO/releases/latest" | python3 -c 'import json, sys; print(json.load(sys.stdin)["tag_name"])')
  fi

  echo "Installing nstance-server $VERSION..."
  DOWNLOAD_URL="https://github.com/$GITHUB_REPO/releases/download/$VERSION/nstance-server_$${VERSION#v}_linux_$ARCH.tar.gz"
fi

# Download and extract
echo "Downloading from: $DOWNLOAD_URL"
curl -fsSL "$DOWNLOAD_URL" | tar -xz -C /usr/local/bin nstance-server
chmod +x /usr/local/bin/nstance-server

# Create the shared socket group and unprivileged proxy user.
getent group nstance >/dev/null || groupadd --system nstance
id -u nstance-proxy >/dev/null 2>&1 ||
  useradd --system --gid nstance --no-create-home --shell /usr/sbin/nologin nstance-proxy

# Create systemd service
cat > /etc/systemd/system/nstance-server.service <<SYSTEMD
[Unit]
Description=Nstance Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=nstance
Environment=NSTANCE_PROVIDER=${provider}
Environment=AWS_REGION=${aws_region}
Environment=GOOGLE_PROJECT=${google_project}
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
RuntimeDirectory=nstance
RuntimeDirectoryMode=0750
ReadWritePaths=/var/lib/nstance-server

[Install]
WantedBy=multi-user.target
SYSTEMD

# The proxy reads listener configuration only through the server's Unix socket.
cat > /etc/systemd/system/nstance-proxy.service <<'SYSTEMD'
[Unit]
Description=Nstance Wake Proxy
Requires=nstance-server.service
After=nstance-server.service
PartOf=nstance-server.service

[Service]
Type=simple
User=nstance-proxy
Group=nstance
ExecStart=/usr/local/bin/nstance-server proxy
Restart=always
RestartSec=5
TimeoutStopSec=35
NoNewPrivileges=true
CapabilityBoundingSet=
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
InaccessiblePaths=/var/lib/nstance-server -/var/lib/cloud
# Block AWS and Google metadata endpoints, including their IPv6 addresses.
IPAddressDeny=169.254.169.254/32 fd00:ec2::254/128 fd20:ce::254/128

[Install]
WantedBy=multi-user.target
SYSTEMD

# Enable and start both services.
systemctl daemon-reload
systemctl enable --now nstance-server nstance-proxy

echo "=== Userdata Script Completed at $(date) ==="
