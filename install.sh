#!/bin/bash
set -euo pipefail

API_KEY="${1:-}"
REPO_URL="${REPO_URL:-https://github.com/magicaleks/qudata-agent-alpha.git}"
INSTALL_DIR="${INSTALL_DIR:-/opt/qudata-agent}"
LOG_FILE="/var/log/qudata-install.log"

rm -f "$LOG_FILE"

log() {
    echo "$@" | tee -a "$LOG_FILE"
}

log_cmd() {
    "$@" 2>&1 | tee -a "$LOG_FILE"
    return ${PIPESTATUS[0]}
}

cleanup_on_error() {
    local exit_code=$?
    if [ $exit_code -ne 0 ]; then
        echo ""
        echo "Installation failed with error code: $exit_code"
        echo "Full log saved to: $LOG_FILE"
        echo "Please contact qudata.ai support to solve the problem!"
        echo ""
        if [ -f "$LOG_FILE" ]; then
            echo "Log contents:"
            cat "$LOG_FILE"
        fi
    fi
}

trap cleanup_on_error EXIT

if [ "$EUID" -ne 0 ]; then
    echo "Error: root required"
    exit 1
fi

if [ -z "$API_KEY" ]; then
    echo "Error: API key required"
    echo "Usage: bash install.sh <api-key>"
    exit 1
fi

if [[ ! "$API_KEY" =~ ^ak- ]]; then
    echo "Error: invalid API key format (must start with 'ak-')"
    exit 1
fi

rm -f /etc/apt/sources.list.d/nvidia-container-toolkit.list 2>/dev/null || true

log "Installing system dependencies"
log_cmd apt-get update
DEBIAN_FRONTEND=noninteractive log_cmd apt-get install -y \
    build-essential \
    git \
    curl \
    wget \
    ca-certificates \
    gnupg \
    lsb-release \
    libnvidia-ml-dev \
    cryptsetup \
    cryptsetup-bin \
    dmsetup \
    apparmor \
    apparmor-utils

if ! command -v docker >/dev/null 2>&1; then
    log "Installing Docker"
    apt-get remove -y docker docker-engine docker.io containerd runc 2>/dev/null || true
    install -m 0755 -d /etc/apt/keyrings
    log_cmd bash -c "curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg && chmod a+r /etc/apt/keyrings/docker.gpg"
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
    log_cmd apt-get update
    DEBIAN_FRONTEND=noninteractive log_cmd apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    systemctl enable docker >/dev/null 2>&1
    systemctl start docker
fi

if ! command -v nvidia-ctk >/dev/null 2>&1; then
    log "Installing NVIDIA Container Toolkit"

  log_cmd curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey -o /tmp/nvidia.gpg
  gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg /tmp/nvidia.gpg

  curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
    | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
    | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list >/dev/null

  log_cmd apt-get update
  DEBIAN_FRONTEND=noninteractive log_cmd apt-get install -y nvidia-container-toolkit

  nvidia-ctk runtime configure --runtime=docker >/dev/null 2>&1
  systemctl restart docker
fi

GO_VERSION="1.23.4"
if ! command -v go >/dev/null 2>&1; then
    log "Installing Go $GO_VERSION"
    log_cmd wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
    rm -rf /usr/local/go
    log_cmd tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
    rm go${GO_VERSION}.linux-amd64.tar.gz
    if ! grep -q "/usr/local/go/bin" /etc/profile; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
    fi
fi
export PATH=$PATH:/usr/local/go/bin

log "Building QuData Agent"
if [ -d "$INSTALL_DIR" ]; then
    cd "$INSTALL_DIR"
    log_cmd git pull
else
    log_cmd git clone "$REPO_URL" "$INSTALL_DIR"
    cd "$INSTALL_DIR"
fi

rm -f /usr/local/bin/qudata-agent
log_cmd bash -c "CGO_ENABLED=1 go build -o /usr/local/bin/qudata-agent ./cmd/app"
chmod +x /usr/local/bin/qudata-agent

mkdir -p /var/lib/qudata/data
mkdir -p /etc/qudata

cat > /etc/systemd/system/qudata-agent.service <<EOF
[Unit]
Description=QuData Agent
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/qudata-agent
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=qudata-agent
Environment="QUDATA_API_KEY=$API_KEY"
Environment="QUDATA_SERVICE_URL=https://api.qudata.com"
Environment="QUDATA_AGENT_PORT=7070"

[Install]
WantedBy=multi-user.target
EOF

log "Starting QuData Agent"
systemctl daemon-reload
systemctl enable qudata-agent >/dev/null 2>&1
systemctl restart qudata-agent
sleep 3

if ! systemctl is-active --quiet qudata-agent; then
    log "Error: agent failed to start"
    journalctl -u qudata-agent -n 20 --no-pager | tee -a "$LOG_FILE"
    exit 1
fi

trap - EXIT

echo ""
echo "Installation successful"
echo ""

rm -f "$LOG_FILE"

if command -v nvidia-smi >/dev/null 2>&1; then
    GPU_NAME=$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -n1 || echo "")
    GPU_MEMORY=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -n1 || echo "")
    GPU_DRIVER=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -n1 || echo "")
    
    if [ -n "$GPU_NAME" ] && [ -n "$GPU_MEMORY" ]; then
        echo "GPU: $GPU_NAME"
        echo "VRAM: ${GPU_MEMORY} MB"
        if [ -n "$GPU_DRIVER" ]; then
            echo "Driver: $GPU_DRIVER"
        fi
    fi
fi
