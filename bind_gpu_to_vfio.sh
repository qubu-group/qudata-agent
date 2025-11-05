#!/bin/bash

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "Error: Must run as root"
    exit 1
fi

echo "==> NVIDIA GPU VFIO Binding Script"
echo "WARNING: This will unbind the GPU from NVIDIA driver and bind it to VFIO."
echo "After this, the GPU will NOT be usable by the host system!"
echo ""

if [ ! -d /sys/kernel/iommu_groups ] || [ -z "$(ls -A /sys/kernel/iommu_groups 2>/dev/null)" ]; then
    echo "ERROR: IOMMU is not enabled!"
    echo "Please enable IOMMU in GRUB and reboot first."
    exit 1
fi

GPU_PCI=$(lspci -D -nn | grep -i "nvidia" | grep -iE "vga|3d" | head -n1 | awk '{print $1}')
if [ -z "$GPU_PCI" ]; then
    echo "ERROR: No NVIDIA GPU found"
    exit 1
fi

echo "Found GPU at PCI address: $GPU_PCI"

GPU_INFO=$(lspci -nn -s $GPU_PCI | grep -oP '\[\K[0-9a-f]{4}:[0-9a-f]{4}')
VENDOR_ID=$(echo $GPU_INFO | cut -d: -f1)
DEVICE_ID=$(echo $GPU_INFO | cut -d: -f2)

echo "Vendor ID: $VENDOR_ID"
echo "Device ID: $DEVICE_ID"

IOMMU_GROUP=$(basename $(readlink /sys/bus/pci/devices/$GPU_PCI/iommu_group 2>/dev/null) 2>/dev/null)
if [ -z "$IOMMU_GROUP" ]; then
    echo "ERROR: GPU is not in an IOMMU group"
    exit 1
fi

echo "IOMMU Group: $IOMMU_GROUP"
echo ""
echo "Press Ctrl+C to cancel, or Enter to continue..."
read

echo "==> Stopping services that might use GPU"
systemctl stop qudata-agent 2>/dev/null || true
systemctl stop docker 2>/dev/null || true

echo "==> Unloading NVIDIA kernel modules"
rmmod nvidia_drm 2>/dev/null || true
rmmod nvidia_modeset 2>/dev/null || true
rmmod nvidia_uvm 2>/dev/null || true
rmmod nvidia 2>/dev/null || true

echo "==> Loading VFIO modules"
modprobe vfio
modprobe vfio-pci
modprobe vfio_iommu_type1

echo "==> Unbinding GPU from current driver"
DRIVER_PATH="/sys/bus/pci/devices/$GPU_PCI/driver"
if [ -L "$DRIVER_PATH" ]; then
    echo "$GPU_PCI" > $DRIVER_PATH/unbind
    echo "GPU unbound from $(basename $(readlink $DRIVER_PATH))"
fi

echo "==> Binding GPU to vfio-pci"
echo "$VENDOR_ID $DEVICE_ID" > /sys/bus/pci/drivers/vfio-pci/new_id
sleep 1

if [ -L "/sys/bus/pci/devices/$GPU_PCI/driver" ] && [ "$(basename $(readlink /sys/bus/pci/devices/$GPU_PCI/driver))" = "vfio-pci" ]; then
    echo "SUCCESS: GPU is now bound to vfio-pci"
    echo "VFIO device: /dev/vfio/$IOMMU_GROUP"
    
    echo ""
    echo "==> Making changes persistent"
    cat > /etc/modprobe.d/vfio-gpu.conf <<EOF
softdep nvidia pre: vfio-pci
options vfio-pci ids=$VENDOR_ID:$DEVICE_ID
EOF
    
    cat > /etc/modules-load.d/vfio.conf <<EOF
vfio
vfio-pci
vfio_iommu_type1
EOF
    
    echo "Configuration saved to:"
    echo "  - /etc/modprobe.d/vfio-gpu.conf"
    echo "  - /etc/modules-load.d/vfio.conf"
    echo ""
    echo "GPU will be bound to vfio-pci on next boot."
    
    echo "==> Starting services"
    systemctl start docker 2>/dev/null || true
    systemctl start qudata-agent 2>/dev/null || true
    
    echo ""
    echo "GPU passthrough is ready for Kata Containers!"
else
    echo "ERROR: Failed to bind GPU to vfio-pci"
    exit 1
fi

