#!/bin/bash
set -euo pipefail

API_KEY="${1:-}"
REPO_URL="${REPO_URL:-https://github.com/magicaleks/qudata-agent-alpha.git}"
INSTALL_DIR="${INSTALL_DIR:-/opt/qudata-agent}"

progress() {
    echo -n "$1"
    shift
    "$@" >/dev/null 2>&1 &
    local pid=$!
    while kill -0 $pid 2>/dev/null; do
        echo -n "."
        sleep 0.5
    done
    wait $pid
    local status=$?
    if [ $status -eq 0 ]; then
        echo " done"
    else
        echo " failed"
        return $status
    fi
}

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

echo "Installing system dependencies"
progress "Updating package lists" apt-get update -qq
progress "Installing packages" apt-get install -y -qq \
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
    echo "Installing Docker"
    apt-get remove -y docker docker-engine docker.io containerd runc 2>/dev/null || true
    install -m 0755 -d /etc/apt/keyrings
    progress "Adding Docker repository" bash -c "curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg && chmod a+r /etc/apt/keyrings/docker.gpg"
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
    progress "Installing Docker packages" bash -c "apt-get update -qq && apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin"
    systemctl enable docker >/dev/null 2>&1
    systemctl start docker
fi

if ! command -v nvidia-ctk >/dev/null 2>&1; then
    echo "Installing NVIDIA Container Toolkit"
    curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
    curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
        sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
        tee /etc/apt/sources.list.d/nvidia-container-toolkit.list > /dev/null
    progress "Installing toolkit" bash -c "apt-get update -qq && apt-get install -y -qq nvidia-container-toolkit"
    nvidia-ctk runtime configure --runtime=docker >/dev/null 2>&1
    systemctl restart docker
fi

GO_VERSION="1.23.4"
if ! command -v go >/dev/null 2>&1; then
    echo "Installing Go $GO_VERSION"
    progress "Downloading Go" wget -q https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
    rm -rf /usr/local/go
    progress "Extracting Go" tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
    rm go${GO_VERSION}.linux-amd64.tar.gz
    if ! grep -q "/usr/local/go/bin" /etc/profile; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
    fi
fi
export PATH=$PATH:/usr/local/go/bin

echo "Building QuData Agent"
if [ -d "$INSTALL_DIR" ]; then
    cd "$INSTALL_DIR"
    progress "Updating repository" git pull -q
else
    progress "Cloning repository" git clone -q "$REPO_URL" "$INSTALL_DIR"
    cd "$INSTALL_DIR"
fi

rm -f /usr/local/bin/qudata-agent
progress "Compiling agent" bash -c "CGO_ENABLED=1 go build -o /usr/local/bin/qudata-agent ./cmd/app"
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

echo "Starting QuData Agent"
systemctl daemon-reload
systemctl enable qudata-agent >/dev/null 2>&1
systemctl restart qudata-agent
progress "Waiting for agent" sleep 3

if ! systemctl is-active --quiet qudata-agent; then
    echo "Error: agent failed to start"
    journalctl -u qudata-agent -n 20 --no-pager
    exit 1
fi

echo ""
echo "Installation successful"
echo ""
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
