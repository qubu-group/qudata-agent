#!/bin/bash

echo "==> Checking NVIDIA Driver and CUDA Version"
echo ""

if ! command -v nvidia-smi >/dev/null 2>&1; then
    echo "ERROR: nvidia-smi not found"
    echo "Please install NVIDIA drivers first"
    exit 1
fi

DRIVER_VERSION=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -n1)
CUDA_VERSION=$(nvidia-smi --query-gpu=cuda_version --format=csv,noheader 2>/dev/null | head -n1)

if [ -z "$DRIVER_VERSION" ] || [ -z "$CUDA_VERSION" ]; then
    echo "ERROR: Could not detect driver or CUDA version"
    exit 1
fi

echo "NVIDIA Driver Version: $DRIVER_VERSION"
echo "Max CUDA Version: $CUDA_VERSION"
echo ""

CUDA_MAJOR=$(echo $CUDA_VERSION | cut -d. -f1)
CUDA_MINOR=$(echo $CUDA_VERSION | cut -d. -f2)

echo "Compatibility:"
echo ""

if [ "$CUDA_MAJOR" -ge 12 ] && [ "$CUDA_MINOR" -ge 6 ]; then
    echo "✓ CUDA 12.6+ supported - Compatible with latest containers"
    echo "  You can use: nvidia/cuda:12.6.x, nvidia/cuda:12.7.x, etc."
elif [ "$CUDA_MAJOR" -ge 12 ] && [ "$CUDA_MINOR" -ge 4 ]; then
    echo "✓ CUDA 12.4+ supported"
    echo "  You can use: nvidia/cuda:12.4.x, nvidia/cuda:12.5.x"
    echo "  ⚠ For CUDA 12.6+ containers, upgrade to nvidia-driver-550+"
elif [ "$CUDA_MAJOR" -ge 12 ] && [ "$CUDA_MINOR" -ge 2 ]; then
    echo "✓ CUDA 12.2+ supported"
    echo "  You can use: nvidia/cuda:12.2.x, nvidia/cuda:12.3.x"
    echo "  ⚠ For CUDA 12.4+ containers, upgrade to nvidia-driver-550+"
    echo "  ⚠ For CUDA 12.6+ containers, upgrade to nvidia-driver-550+"
elif [ "$CUDA_MAJOR" -ge 12 ]; then
    echo "✓ CUDA 12.0+ supported"
    echo "  You can use: nvidia/cuda:12.0.x, nvidia/cuda:12.1.x"
    echo "  ⚠ For newer containers, consider upgrading driver"
elif [ "$CUDA_MAJOR" -ge 11 ]; then
    echo "✓ CUDA 11.x supported"
    echo "  You can use: nvidia/cuda:11.x containers"
    echo "  ⚠ For CUDA 12.x containers, upgrade driver to nvidia-driver-525+"
else
    echo "⚠ CUDA version is very old"
    echo "  Please upgrade to nvidia-driver-525+ for CUDA 12.x support"
fi

echo ""
echo "Driver upgrade recommendations:"
echo "  nvidia-driver-525: CUDA 12.0-12.1"
echo "  nvidia-driver-535: CUDA 12.2-12.3"
echo "  nvidia-driver-550: CUDA 12.4-12.5"
echo "  nvidia-driver-560: CUDA 12.6+"
echo ""

echo "To upgrade driver:"
echo "  sudo apt-get install nvidia-driver-550"
echo "  sudo reboot"

