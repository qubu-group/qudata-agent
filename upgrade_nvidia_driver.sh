#!/bin/bash

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "Error: Must run as root"
    exit 1
fi

echo "==> NVIDIA Driver Upgrade Script"
echo ""

if ! command -v nvidia-smi >/dev/null 2>&1; then
    echo "ERROR: nvidia-smi not found"
    echo "NVIDIA drivers not installed. Run install.sh first."
    exit 1
fi

CURRENT_DRIVER=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -n1 | tr -d ' ')
CURRENT_CUDA=$(nvidia-smi --query-gpu=cuda_version --format=csv,noheader 2>/dev/null | head -n1 | tr -d ' ')

echo "Current Configuration:"
echo "  Driver: $CURRENT_DRIVER"
echo "  CUDA Support: $CURRENT_CUDA"
echo ""

CUDA_MAJOR=$(echo $CURRENT_CUDA | cut -d. -f1)
CUDA_MINOR=$(echo $CURRENT_CUDA | cut -d. -f2)

if [ "$CUDA_MAJOR" -ge 12 ] && [ "$CUDA_MINOR" -ge 6 ]; then
    echo "✓ Your driver already supports CUDA 12.6+"
    echo "No upgrade needed."
    exit 0
fi

echo "⚠ Your CUDA version ($CURRENT_CUDA) is below 12.6"
echo ""
echo "Recommended: nvidia-driver-560 (CUDA 12.6+)"
echo ""
echo "This will:"
echo "  1. Stop Docker and Qudata services"
echo "  2. Unload current NVIDIA modules"
echo "  3. Install nvidia-driver-560"
echo "  4. Restart services"
echo "  5. Require system reboot"
echo ""
echo -n "Continue? (y/N): "
read RESPONSE

if [ "$RESPONSE" != "y" ] && [ "$RESPONSE" != "Y" ]; then
    echo "Upgrade cancelled."
    exit 0
fi

echo ""
echo "==> Stopping services"
systemctl stop qudata-agent 2>/dev/null || true
systemctl stop docker 2>/dev/null || true

echo "==> Unloading NVIDIA modules"
rmmod nvidia_drm 2>/dev/null || true
rmmod nvidia_modeset 2>/dev/null || true
rmmod nvidia_uvm 2>/dev/null || true
rmmod nvidia 2>/dev/null || true

echo "==> Installing nvidia-driver-560"
apt-get update -qq
if DEBIAN_FRONTEND=noninteractive apt-get install -y nvidia-driver-560 nvidia-utils-560 libnvidia-compute-560; then
    echo "✓ nvidia-driver-560 installed successfully"
else
    echo "ERROR: Failed to install nvidia-driver-560"
    echo "Starting services..."
    systemctl start docker 2>/dev/null || true
    systemctl start qudata-agent 2>/dev/null || true
    exit 1
fi

echo ""
echo "=========================================="
echo "DRIVER UPGRADE COMPLETE"
echo "=========================================="
echo ""
echo "⚠ REBOOT REQUIRED"
echo ""
echo "The new driver will be loaded after reboot."
echo "To reboot now: sudo reboot"
echo ""
echo "After reboot, verify:"
echo "  nvidia-smi"
echo "  # Should show CUDA Version: 12.6 or higher"
echo "=========================================="

