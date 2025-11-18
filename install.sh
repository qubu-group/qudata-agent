#!/bin/bash
set -euo pipefail

API_KEY="${1:-}"
REPO_URL="${REPO_URL:-https://github.com/magicaleks/qudata-agent-alpha.git}"
INSTALL_DIR="${INSTALL_DIR:-/opt/qudata-agent}"
LOG_FILE="/var/log/qudata-install.log"

rm -f "$LOG_FILE"
exec 3>&1

log() {
    echo "$@" | tee -a "$LOG_FILE" >&3
}

log_cmd() {
    local msg="$1"
    shift
    echo -n "$msg..." >&3
    if "$@" >>"$LOG_FILE" 2>&1; then
        echo " done" >&3
        return 0
    else
        local status=$?
        echo " failed" >&3
        return $status
    fi
}

cleanup_on_error() {
    local exit_code=$?
    if [ $exit_code -ne 0 ]; then
        echo "" >&3
        echo "Installation failed with error code: $exit_code" >&3
        echo "Full log saved to: $LOG_FILE" >&3
        echo "Please contact qudata.ai support to solve the problem!" >&3
        echo "" >&3
        if [ -f "$LOG_FILE" ]; then
            echo "Last 50 lines of log:" >&3
            tail -n 50 "$LOG_FILE" >&3
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
log_cmd "Updating package lists" apt-get update

if [ "${QUDATA_AGENT_DEBUG:-false}" = "true" ]; then
    log "DEBUG MODE: Installing base packages without NVIDIA dependencies"
    log_cmd "Installing packages" env DEBIAN_FRONTEND=noninteractive apt-get install -y \
        build-essential \
        git \
        curl \
        wget \
        ca-certificates \
        gnupg \
        lsb-release \
        cryptsetup \
        cryptsetup-bin \
        dmsetup \
        apparmor \
        apparmor-utils
else
    log_cmd "Installing packages" env DEBIAN_FRONTEND=noninteractive apt-get install -y \
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
fi

if ! command -v docker >/dev/null 2>&1; then
    log "Installing Docker"
    apt-get remove -y docker docker-engine docker.io containerd runc >>"$LOG_FILE" 2>&1 || true
    install -m 0755 -d /etc/apt/keyrings
    log_cmd "Adding Docker repository" bash -c "curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg && chmod a+r /etc/apt/keyrings/docker.gpg"
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list >> "$LOG_FILE"
    log_cmd "Updating package lists" apt-get update
    log_cmd "Installing Docker packages" env DEBIAN_FRONTEND=noninteractive apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    systemctl enable docker >>"$LOG_FILE" 2>&1
    systemctl start docker
fi

if [ "${QUDATA_AGENT_DEBUG:-false}" != "true" ]; then
    if ! command -v nvidia-ctk >/dev/null 2>&1; then
        log "Installing NVIDIA Container Toolkit"
        log_cmd "Downloading GPG key" curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey -o /tmp/nvidia.gpg
        gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg /tmp/nvidia.gpg 2>>"$LOG_FILE"
        curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
          | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
          | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list >> "$LOG_FILE"
        log_cmd "Updating package lists" apt-get update
        log_cmd "Installing toolkit" env DEBIAN_FRONTEND=noninteractive apt-get install -y nvidia-container-toolkit
        nvidia-ctk runtime configure --runtime=docker >>"$LOG_FILE" 2>&1
        systemctl restart docker
    fi
else
    log "DEBUG MODE: Skipping NVIDIA Container Toolkit installation"
fi

echo -n "Installing agent..." >&3

{
    GO_VERSION="1.23.4"
    if ! command -v go >/dev/null 2>&1; then
        echo "Downloading Go $GO_VERSION" >> "$LOG_FILE"
        wget -q https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz >> "$LOG_FILE" 2>&1 || exit 1
        rm -rf /usr/local/go
        echo "Extracting Go" >> "$LOG_FILE"
        tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz >> "$LOG_FILE" 2>&1 || exit 1
        rm go${GO_VERSION}.linux-amd64.tar.gz
        if ! grep -q "/usr/local/go/bin" /etc/profile; then
            echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
        fi
    fi
    export PATH=$PATH:/usr/local/go/bin

    NVML_PATH=""
    if [ "${QUDATA_AGENT_DEBUG:-false}" != "true" ]; then
        if nvidia-smi >/dev/null 2>&1; then
            for search_dir in /usr/lib/x86_64-linux-gnu /lib/x86_64-linux-gnu /usr/lib /usr/local/lib; do
                if [ -f "$search_dir/libnvidia-ml.so.1" ]; then
                    NVML_PATH="$search_dir"
                    if [ ! -f "$search_dir/libnvidia-ml.so" ]; then
                        ln -sf "$search_dir/libnvidia-ml.so.1" "$search_dir/libnvidia-ml.so"
                        echo "Created symlink: $search_dir/libnvidia-ml.so -> libnvidia-ml.so.1" >> "$LOG_FILE"
                    fi
                    break
                fi
            done
        fi
    else
        echo "DEBUG MODE: Skipping NVML search" >> "$LOG_FILE"
    fi

    if [ -d "$INSTALL_DIR" ]; then
        cd "$INSTALL_DIR"
        echo "Updating repository" >> "$LOG_FILE"
        git pull -q >> "$LOG_FILE" 2>&1 || exit 1
    else
        echo "Cloning repository" >> "$LOG_FILE"
        git clone -q "$REPO_URL" "$INSTALL_DIR" >> "$LOG_FILE" 2>&1 || exit 1
        cd "$INSTALL_DIR"
    fi

    rm -f /usr/local/bin/qudata-agent
    echo "Compiling agent" >> "$LOG_FILE"
    
    if [ "${QUDATA_AGENT_DEBUG:-false}" = "true" ]; then
        echo "DEBUG MODE: Compiling without GPU support (using mocks)" >> "$LOG_FILE"
        CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu"
    elif [ -n "$NVML_PATH" ]; then
        echo "Using NVML from: $NVML_PATH" >> "$LOG_FILE"
        CGO_LDFLAGS="-L$NVML_PATH -L/usr/lib/x86_64-linux-gnu"
    else
        echo "NVML not found, compiling without GPU support" >> "$LOG_FILE"
        CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu"
    fi
    
    CGO_ENABLED=1 CGO_LDFLAGS="$CGO_LDFLAGS" go build -o /usr/local/bin/qudata-agent ./cmd/app >> "$LOG_FILE" 2>&1 || exit 1
    chmod +x /usr/local/bin/qudata-agent
    
    echo " done" >&3
} || {
    echo " failed" >&3
    exit 1
}

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
EOF

if [ "${QUDATA_AGENT_DEBUG:-false}" = "true" ]; then
    echo "Environment=\"QUDATA_AGENT_DEBUG=true\"" >> /etc/systemd/system/qudata-agent.service
    log "DEBUG MODE enabled for agent runtime"
fi

if [ -n "${QUDATA_PORTS:-}" ]; then
    echo "Environment=\"QUDATA_PORTS=$QUDATA_PORTS\"" >> /etc/systemd/system/qudata-agent.service
    log "Port configuration: QUDATA_PORTS=$QUDATA_PORTS"
else
    log "Port configuration: dynamic allocation (QUDATA_PORTS not set)"
fi

cat >> /etc/systemd/system/qudata-agent.service <<EOF

[Install]
WantedBy=multi-user.target
EOF

log "Starting QuData Agent"
systemctl daemon-reload >>"$LOG_FILE" 2>&1
systemctl enable qudata-agent >>"$LOG_FILE" 2>&1
systemctl restart qudata-agent >>"$LOG_FILE" 2>&1
sleep 3

if ! systemctl is-active --quiet qudata-agent; then
    log "Error: agent failed to start"
    journalctl -u qudata-agent -n 20 --no-pager >> "$LOG_FILE"
    exit 1
fi

trap - EXIT

echo "" >&3
echo "Installation successful" >&3
echo "" >&3

rm -f "$LOG_FILE"

if [ "${QUDATA_AGENT_DEBUG:-false}" = "true" ]; then
    echo "DEBUG MODE: Agent will use mock GPU (H100, 70 VRAM)" >&3
elif command -v nvidia-smi >/dev/null 2>&1; then
    GPU_NAME=$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -n1 || echo "")
    GPU_MEMORY=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -n1 || echo "")
    GPU_DRIVER=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -n1 || echo "")
    
    if [ -n "$GPU_NAME" ] && [ -n "$GPU_MEMORY" ]; then
        echo "GPU: $GPU_NAME" >&3
        echo "VRAM: ${GPU_MEMORY} MB" >&3
        if [ -n "$GPU_DRIVER" ]; then
            echo "Driver: $GPU_DRIVER" >&3
        fi
    fi
fi
