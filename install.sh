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
BACKUP_DIR="/tmp/qudata-backup-$(date +%s)"

echo "==> Checking system requirements"
AVAILABLE_SPACE=$(df -BG / | awk 'NR==2 {print $4}' | sed 's/G//')
if [ "$AVAILABLE_SPACE" -lt 5 ]; then
    echo "Error: Not enough disk space (need 5GB, have ${AVAILABLE_SPACE}GB)"
    exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
    echo "Error: curl not found, installing..."
    apt-get update -qq && apt-get install -y curl
fi

echo "==> Creating directories"
mkdir -p $INSTALL_DIR $BIN_DIR $LOG_DIR /var/lib/qudata

echo "==> Backing up current configuration"
mkdir -p "$BACKUP_DIR"
[ -f /etc/docker/daemon.json ] && cp /etc/docker/daemon.json "$BACKUP_DIR/" 2>/dev/null || true
[ -f /etc/qudata.env ] && cp /etc/qudata.env "$BACKUP_DIR/" 2>/dev/null || true
echo "Backup created at: $BACKUP_DIR"

echo "==> Checking NVIDIA GPU"
HAS_NVIDIA=0
if lspci 2>/dev/null | grep -qi nvidia; then
  HAS_NVIDIA=1
  echo "NVIDIA GPU detected"
  
  if lsmod | grep -q nvidia && command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi >/dev/null 2>&1; then
    echo "NVIDIA drivers already working, skipping cleanup"
  else
    echo "Cleaning up broken NVIDIA installation"
    
    systemctl stop nvidia-persistenced 2>/dev/null || true
    
    rmmod nvidia_uvm 2>/dev/null || true
    rmmod nvidia_drm 2>/dev/null || true
    rmmod nvidia_modeset 2>/dev/null || true
    rmmod nvidia 2>/dev/null || true
    
    rm -rf /var/lib/dkms/nvidia* 2>/dev/null || true
    
    if [ -f /etc/kernel/postinst.d/dkms ]; then
      mv /etc/kernel/postinst.d/dkms /etc/kernel/postinst.d/dkms.disabled 2>/dev/null || true
    fi
    
    dpkg --remove --force-all nvidia-dkms-* 2>/dev/null || true
    
    NVIDIA_PKGS=$(dpkg -l | grep -E "^ii.*(nvidia|libnvidia)" | awk '{print $2}' | tr '\n' ' ')
    if [ -n "$NVIDIA_PKGS" ]; then
      for pkg in $NVIDIA_PKGS; do
        dpkg --purge --force-all "$pkg" 2>/dev/null || true
      done
    fi
    
    apt-get autoremove -y --purge 2>/dev/null || true
    
    echo "Cleanup complete"
  fi
else
  echo "No NVIDIA GPU detected"
fi

dpkg --configure -a 2>/dev/null || true
apt-get -f install -y 2>/dev/null || true

echo "==> Installing system dependencies"
apt-get update -qq

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a

apt-get install -y -qq \
    curl \
    wget \
    gnupg \
    software-properties-common \
    build-essential \
    cryptsetup \
    util-linux \
    apparmor-utils \
    systemd \
    git \
    ca-certificates \
    apt-transport-https \
    qemu-system-x86 \
    qemu-kvm \
    qemu-utils

echo "==> Installing Docker"
if [ -f "/usr/share/keyrings/docker-archive-keyring.gpg" ]; then
  rm /usr/share/keyrings/docker-archive-keyring.gpg
fi
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
echo "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" > /etc/apt/sources.list.d/docker.list
apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io

systemctl enable docker
systemctl start docker

echo "==> Detecting virtualization support (KVM/nested)"
HAS_KVM=0
if egrep -qw 'vmx|svm' /proc/cpuinfo; then
  modprobe kvm || true
  modprobe kvm_intel 2>/dev/null || modprobe kvm_amd 2>/dev/null || true
  if [ -e /dev/kvm ]; then
    HAS_KVM=1
  fi
fi
echo "HAS_KVM=$HAS_KVM"

if [ "$HAS_KVM" -eq 1 ]; then
  echo "==> Installing Kata Containers (KVM detected)"
  KATA_VERSION="3.2.0"
  curl -fsSL https://github.com/kata-containers/kata-containers/releases/download/${KATA_VERSION}/kata-static-${KATA_VERSION}-amd64.tar.xz -o /tmp/kata.tar.xz
  tar -xJf /tmp/kata.tar.xz -C /
  rm /tmp/kata.tar.xz
  ln -sf /opt/kata/bin/containerd-shim-kata-v2 /usr/local/bin/containerd-shim-kata-v2
  ln -sf /opt/kata/bin/kata-runtime /usr/local/bin/kata-runtime
  ln -sf /opt/kata/bin/kata-collect-data.sh /usr/local/bin/kata-collect-data.sh

  mkdir -p /etc/kata-containers
  cp /opt/kata/share/defaults/kata-containers/configuration-qemu.toml /etc/kata-containers/configuration.toml || true
  # Ensure kernel/initrd/image paths are set
  sed -i 's|^#\?kernel = .*|kernel = "/opt/kata/share/kata-containers/vmlinuz.container"|' /etc/kata-containers/configuration.toml
  sed -i 's|^#\?image = .*|image = "/opt/kata/share/kata-containers/kata-containers.img"|' /etc/kata-containers/configuration.toml
  sed -i 's|^#\?initrd = .*|initrd = "/opt/kata/share/kata-containers/kata-containers-initrd.img"|' /etc/kata-containers/configuration.toml

  echo "==> Configuring containerd for Kata (shim v2)"
  if [ ! -f /etc/containerd/config.toml ]; then
    containerd config default | tee /etc/containerd/config.toml >/dev/null
  fi
  if ! grep -q '\[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata\]' /etc/containerd/config.toml; then
    cat >>/etc/containerd/config.toml <<'EOC'
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata]
  runtime_type = "io.containerd.kata.v2"
  privileged_without_host_devices = true
EOC
  fi

  echo "==> Installing nerdctl for Kata usage"
  NERDCTL_VER="1.7.7"
  curl -fsSL https://github.com/containerd/nerdctl/releases/download/v${NERDCTL_VER}/nerdctl-${NERDCTL_VER}-linux-amd64.tar.gz -o /tmp/nerdctl.tgz
  tar -xzf /tmp/nerdctl.tgz -C /usr/local/bin nerdctl
  rm /tmp/nerdctl.tgz

  echo "==> Installing CNI plugins for nerdctl"
  CNI_VERSION="1.3.0"
  mkdir -p /opt/cni/bin
  curl -fsSL https://github.com/containernetworking/plugins/releases/download/v${CNI_VERSION}/cni-plugins-linux-amd64-v${CNI_VERSION}.tgz -o /tmp/cni-plugins.tgz
  tar -xzf /tmp/cni-plugins.tgz -C /opt/cni/bin
  rm /tmp/cni-plugins.tgz

  echo "==> Configuring CNI network"
  mkdir -p /etc/cni/net.d
  cat >/etc/cni/net.d/10-bridge.conf <<'EOCNI'
{
  "cniVersion": "1.0.0",
  "name": "bridge",
  "type": "bridge",
  "bridge": "cni0",
  "isGateway": true,
  "ipMasq": true,
  "ipam": {
    "type": "host-local",
    "subnet": "10.88.0.0/16",
    "routes": [
      { "dst": "0.0.0.0/0" }
    ]
  }
}
EOCNI

  echo "==> Configuring Docker (keep runc default; add NVIDIA runtime if present)"
  mkdir -p /etc/docker
  cat >/etc/docker/daemon.json <<'EOF'
{
  "runtimes": {
    "nvidia": {
      "path": "nvidia-container-runtime",
      "runtimeArgs": []
    }
  },
  "default-runtime": "runc"
}
EOF
  systemctl restart containerd
  systemctl restart docker
else
  echo "==> No KVM detected — configuring gVisor (runsc) as micro-VM-like fallback"
  
  RUNSC_URL="https://storage.googleapis.com/gvisor/releases/release/latest/x86_64"
  curl -fsSL "${RUNSC_URL}/runsc" -o /tmp/runsc
  
  if [ -f /tmp/runsc ]; then
    install -m 755 /tmp/runsc /usr/local/bin/runsc
    rm -f /tmp/runsc
  else
    echo "Failed to download gVisor, skipping"
  fi
  
  curl -fsSL "${RUNSC_URL}/containerd-shim-runsc-v1" -o /tmp/containerd-shim-runsc-v1 2>/dev/null || true
  if [ -f /tmp/containerd-shim-runsc-v1 ]; then
    install -m 755 /tmp/containerd-shim-runsc-v1 /usr/local/bin/containerd-shim-runsc-v1
    rm -f /tmp/containerd-shim-runsc-v1
  fi
fi

echo "==> Installing NVIDIA drivers"
if [ "$HAS_NVIDIA" -eq 1 ] && ! command -v nvidia-smi >/dev/null 2>&1; then
  echo "Installing NVIDIA driver 535"
  
  if DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    nvidia-driver-535 \
    nvidia-utils-535 \
    libnvidia-compute-535 \
    libnvidia-ml-dev; then
    echo "NVIDIA driver installed successfully"
    modprobe nvidia 2>/dev/null || echo "Module load will require reboot"
  else
    echo "Warning: NVIDIA driver installation failed, continuing..."
  fi
else
  echo "Installing development headers only"
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq libnvidia-ml-dev 2>/dev/null || true
fi

echo "==> Installing NVIDIA Container Toolkit"
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey 2>/dev/null | \
  gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg 2>/dev/null || true

curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list 2>/dev/null | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  tee /etc/apt/sources.list.d/nvidia-container-toolkit.list >/dev/null 2>&1 || true

apt-get update -qq 2>/dev/null || true
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nvidia-container-toolkit 2>/dev/null || true
nvidia-ctk runtime configure --runtime=docker 2>/dev/null || true

echo "==> Configuring Docker daemon"

HAS_GPU=0
if [ -e /dev/nvidia0 ]; then
  HAS_GPU=1
fi

if [ "$HAS_KVM" -eq 1 ]; then
  cat >/etc/docker/daemon.json <<'EOF'
{
  "runtimes": {
    "nvidia": {
      "path": "nvidia-container-runtime",
      "runtimeArgs": []
    }
  },
  "default-runtime": "runc"
}
EOF
else
  if [ "$HAS_GPU" -eq 1 ]; then
    echo "==> Configuring gVisor nvproxy for GPU support"
    /usr/local/bin/runsc nvproxy list-supported-drivers 2>/dev/null || echo "nvproxy check skipped"
    
    cat >/etc/docker/daemon.json <<'EOF'
{
  "runtimes": {
    "runsc": {
      "path": "/usr/local/bin/runsc",
      "runtimeArgs": [
        "--nvproxy"
      ]
    },
    "nvidia": {
      "path": "nvidia-container-runtime",
      "runtimeArgs": []
    }
  },
  "default-runtime": "runc"
}
EOF
  else
    cat >/etc/docker/daemon.json <<'EOF'
{
  "runtimes": {
    "runsc": {
      "path": "/usr/local/bin/runsc",
      "runtimeArgs": []
    }
  },
  "default-runtime": "runc"
}
EOF
  fi
fi

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
if ! /usr/local/go/bin/go build -tags linux -o $BIN_DIR/qudata-agent ./cmd/app; then
  echo "Error: Failed to build qudata-agent"
  exit 1
fi

if ! /usr/local/go/bin/go build -tags linux -o $BIN_DIR/qudata-security ./cmd/security; then
  echo "Error: Failed to build qudata-security"
  exit 1
fi

chmod +x $BIN_DIR/qudata-agent
chmod +x $BIN_DIR/qudata-security

echo "Build completed successfully"

echo "==> Setup environment variables"

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
ExecStart=$BIN_DIR/qudata-security
Restart=always
RestartSec=10
StandardOutput=append:/var/log/qudata/security.log
StandardError=append:/var/log/qudata/security.error.log

[Install]
WantedBy=multi-user.target
EOF

echo "==> Enabling and starting services"
systemctl daemon-reload
systemctl enable qudata-agent.service
systemctl enable qudata-security.service
systemctl start qudata-agent.service
systemctl start qudata-security.service

echo "==> Cleaning up"
cd /
rm -rf /tmp/qudata-agent-alpha

echo "==> Verifying installation"
ERRORS=0

if ! systemctl is-active --quiet qudata-agent; then
  echo "Warning: qudata-agent service is not running"
  ERRORS=$((ERRORS + 1))
fi

if ! systemctl is-active --quiet qudata-security; then
  echo "Warning: qudata-security service is not running"
  ERRORS=$((ERRORS + 1))
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "Warning: Docker is not installed"
  ERRORS=$((ERRORS + 1))
fi

echo ""
echo "==> Installation complete"
echo ""

if [ "$HAS_NVIDIA" -eq 1 ] && ! nvidia-smi >/dev/null 2>&1; then
  echo "NOTE: NVIDIA GPU detected but drivers not loaded"
  echo "      Reboot required: sudo reboot"
  echo ""
fi

if [ "$ERRORS" -gt 0 ]; then
  echo "Warning: Installation completed with $ERRORS warnings"
  exit 0
fi

echo "Installation completed successfully!"
