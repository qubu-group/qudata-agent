#!/bin/bash

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "Error: Must run as root"
    exit 1
fi

if [ -z "${1:-}" ]; then
    echo "Usage: ./install.sh <QUDATA_API_KEY>"
    exit 1
fi

QUDATA_API_KEY=$1

if [ ${#QUDATA_API_KEY} -lt 20 ]; then
    echo "Error: Invalid API key (too short)"
    exit 1
fi

INSTALL_DIR="/opt/qudata"
BIN_DIR="$INSTALL_DIR/bin"
LOG_DIR="/var/log/qudata"

echo "==> Creating directories"
mkdir -p $INSTALL_DIR $BIN_DIR $LOG_DIR /var/lib/qudata

echo "==> Installing system dependencies"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq build-essential git curl gnupg ca-certificates wget

echo "==> Installing Docker"
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
echo "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" > /etc/apt/sources.list.d/docker.list
apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io

systemctl enable docker
systemctl start docker

echo "==> Installing NVIDIA Container Toolkit"
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  tee /etc/apt/sources.list.d/nvidia-container-toolkit.list >/dev/null

apt-get update -qq
apt-get install -y -qq nvidia-container-toolkit
nvidia-ctk runtime configure --runtime=docker
systemctl restart docker

echo "==> Installing Go"
GO_VERSION="1.23.0"
wget -q https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz -O /tmp/go.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf /tmp/go.tar.gz
rm /tmp/go.tar.gz

export PATH=$PATH:/usr/local/go/bin
export CGO_ENABLED=1

echo "==> Cloning repository"
cd /tmp
rm -rf qudata-agent-alpha
git clone https://github.com/magicaleks/qudata-agent-alpha.git
cd qudata-agent-alpha

echo "==> Building agent"
/usr/local/go/bin/go build -tags linux -o $BIN_DIR/qudata-agent ./cmd/app
/usr/local/go/bin/go build -tags linux -o $BIN_DIR/qudata-security ./cmd/security
chmod +x $BIN_DIR/qudata-agent
chmod +x $BIN_DIR/qudata-security

echo "==> Setup environment"
cat > /etc/qudata.env <<EOF
QUDATA_API_KEY=$QUDATA_API_KEY
CGO_ENABLED=1
EOF
chmod 600 /etc/qudata.env

echo "==> Installing systemd services"
cat > /etc/systemd/system/qudata-agent.service <<EOF
[Unit]
Description=Qudata Agent
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
User=root
EnvironmentFile=/etc/qudata.env
ExecStart=$BIN_DIR/qudata-agent
Restart=always
RestartSec=10
StandardOutput=append:/var/log/qudata/agent.log
StandardError=append:/var/log/qudata/agent.error.log
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/qudata-security.service <<EOF
[Unit]
Description=Qudata Security Monitor
After=network.target

[Service]
Type=simple
User=root
EnvironmentFile=/etc/qudata.env
ExecStart=$BIN_DIR/qudata-security
Restart=always
RestartSec=10
StandardOutput=append:/var/log/qudata/security.log
StandardError=append:/var/log/qudata/security.error.log

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable qudata-agent qudata-security
systemctl start qudata-agent qudata-security

cd /
rm -rf /tmp/qudata-agent-alpha

echo ""
echo "=========================================="
echo "INSTALLATION COMPLETE"
echo "=========================================="
echo ""
echo "Services started:"
echo "  - qudata-agent"
echo "  - qudata-security"
echo ""
echo "Logs:"
echo "  /var/log/qudata/agent.log"
echo "  /var/log/qudata/security.log"
echo ""
echo "Check status:"
echo "  systemctl status qudata-agent"
echo "  systemctl status qudata-security"
echo ""
