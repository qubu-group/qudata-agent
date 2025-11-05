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
else
  echo "No NVIDIA GPU detected"
fi

if [ "$HAS_NVIDIA" -eq 1 ]; then
  if nvidia-smi >/dev/null 2>&1; then
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
    
    apt-get autoremove --purge -y 2>/dev/null || true
    apt-get autoclean 2>/dev/null || true
    
    rm -f /etc/modprobe.d/nvidia*.conf 2>/dev/null || true
    rm -f /usr/share/X11/xorg.conf.d/*nvidia*.conf 2>/dev/null || true
    rm -rf /usr/lib/nvidia* 2>/dev/null || true
    rm -rf /usr/lib/x86_64-linux-gnu/libnvidia* 2>/dev/null || true
    
    echo "Cleanup complete"
  fi
fi

dpkg --configure -a 2>/dev/null || true
apt-get -f install -y 2>/dev/null || true

echo "==> Installing system dependencies"
apt-get update -qq

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a

apt-get install -y -qq \
  build-essential \
  git \
  curl \
  gnupg \
  lsb-release \
  ca-certificates \
  software-properties-common \
  wget \
  cryptsetup \
  pciutils \
  linux-headers-$(uname -r) 2>/dev/null || true

echo "==> Installing Docker"
if [ -f /usr/share/keyrings/docker-archive-keyring.gpg ]; then
rm /usr/share/keyrings/docker-archive-keyring.gpg
fi
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
echo "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" > /etc/apt/sources.list.d/docker.list
apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io

systemctl enable docker
systemctl start docker

echo "==> Checking NVIDIA drivers"
NEED_DRIVER_INSTALL=0
NEED_DRIVER_UPGRADE=0

if [ "$HAS_NVIDIA" -eq 1 ]; then
  if command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi >/dev/null 2>&1; then
    CURRENT_CUDA=$(nvidia-smi --query-gpu=cuda_version --format=csv,noheader 2>/dev/null | head -n1 | tr -d ' ')
    CURRENT_DRIVER=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -n1 | tr -d ' ')
    
    if [ -n "$CURRENT_CUDA" ]; then
      CUDA_MAJOR=$(echo $CURRENT_CUDA | cut -d. -f1)
      CUDA_MINOR=$(echo $CURRENT_CUDA | cut -d. -f2)
      
      echo "Current driver: $CURRENT_DRIVER (CUDA $CURRENT_CUDA)"
      
      if [ "$CUDA_MAJOR" -lt 12 ] || ([ "$CUDA_MAJOR" -eq 12 ] && [ "$CUDA_MINOR" -lt 6 ]); then
        echo "⚠ Driver is outdated (CUDA $CURRENT_CUDA < 12.6)"
        echo "Upgrading to nvidia-driver-560 for CUDA 12.6+ support"
        NEED_DRIVER_UPGRADE=1
        
        systemctl stop docker 2>/dev/null || true
        
        rmmod nvidia_drm 2>/dev/null || true
        rmmod nvidia_modeset 2>/dev/null || true
        rmmod nvidia_uvm 2>/dev/null || true
        rmmod nvidia 2>/dev/null || true
      else
        echo "✓ Driver supports CUDA 12.6+ (current: $CURRENT_CUDA)"
      fi
    fi
  else
    echo "NVIDIA driver not found or not working"
    NEED_DRIVER_INSTALL=1
  fi
fi

if [ "$NEED_DRIVER_INSTALL" -eq 1 ] || [ "$NEED_DRIVER_UPGRADE" -eq 1 ]; then
  echo "==> Installing NVIDIA driver 560 (CUDA 12.6+ support)"
  
  if DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    nvidia-driver-560 \
    nvidia-utils-560 \
    libnvidia-compute-560 \
    libnvidia-ml-dev 2>/dev/null; then
    echo "✓ nvidia-driver-560 installed successfully"
    modprobe nvidia 2>/dev/null || echo "Module load will require reboot"
  elif DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    nvidia-driver-550 \
    nvidia-utils-550 \
    libnvidia-compute-550 \
    libnvidia-ml-dev; then
    echo "⚠ nvidia-driver-550 installed (CUDA 12.5 max)"
    echo "WARNING: CUDA 12.6+ containers will NOT work"
    modprobe nvidia 2>/dev/null || echo "Module load will require reboot"
  else
    echo "ERROR: Failed to install NVIDIA driver"
    echo "Manual installation required: sudo apt-get install nvidia-driver-560"
  fi
elif [ "$HAS_NVIDIA" -eq 1 ]; then
  echo "Installing development headers"
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

systemctl disable nvidia-cdi-refresh.service 2>/dev/null || true
systemctl stop nvidia-cdi-refresh.service 2>/dev/null || true

echo "==> Configuring Docker daemon"
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
systemctl enable qudata-agent
systemctl enable qudata-security
systemctl start qudata-agent
systemctl start qudata-security

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
echo "=========================================="
echo "INSTALLATION COMPLETE"
echo "=========================================="
echo ""

if [ "$HAS_NVIDIA" -eq 1 ] && command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi >/dev/null 2>&1; then
  CUDA_VERSION=$(nvidia-smi --query-gpu=cuda_version --format=csv,noheader 2>/dev/null | head -n1 | tr -d ' ')
  DRIVER_VERSION=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -n1 | tr -d ' ')
  
  if [ -n "$CUDA_VERSION" ]; then
    echo "GPU: NVIDIA"
    echo "Driver: $DRIVER_VERSION"
    echo "CUDA Support: $CUDA_VERSION"
    echo "GPU Mode: --gpus=all (shared access)"
    echo ""
    
    CUDA_MAJOR=$(echo $CUDA_VERSION | cut -d. -f1)
    CUDA_MINOR=$(echo $CUDA_VERSION | cut -d. -f2)
    
    if [ "$CUDA_MAJOR" -lt 12 ] || ([ "$CUDA_MAJOR" -eq 12 ] && [ "$CUDA_MINOR" -lt 6 ]); then
      echo "⚠ WARNING: Your CUDA version is $CUDA_VERSION"
      echo "  Modern containers (nvidia/cuda:12.6.x+) may not work."
      echo ""
      echo "To upgrade driver for CUDA 12.6+ support:"
      echo "  1. Stop services:"
      echo "     sudo systemctl stop qudata-agent docker"
      echo ""
      echo "  2. Install new driver:"
      echo "     sudo apt-get install nvidia-driver-560"
      echo ""
      echo "  3. Reboot:"
      echo "     sudo reboot"
      echo ""
      echo "Compatibility table:"
      echo "  nvidia-driver-535 → CUDA 12.2"
      echo "  nvidia-driver-550 → CUDA 12.5"
      echo "  nvidia-driver-560 → CUDA 12.6+"
      echo ""
    else
      echo "✓ Driver supports modern CUDA containers"
    fi
  fi
else
  echo "GPU: Not available"
fi

if [ "$HAS_NVIDIA" -eq 1 ] && ! nvidia-smi >/dev/null 2>&1; then
  echo ""
  echo "=========================================="
  echo "REBOOT REQUIRED"
  echo "=========================================="
  echo "NVIDIA GPU detected but drivers not loaded."
  echo "Please reboot the system: sudo reboot"
  echo ""
  echo "After reboot, check driver version:"
  echo "  nvidia-smi"
  echo "=========================================="
fi

if [ "$ERRORS" -gt 0 ]; then
  echo ""
  echo "Warning: Installation completed with $ERRORS warnings"
  echo "Check the logs above for details."
  exit 0
fi

echo "Installation completed successfully!"
