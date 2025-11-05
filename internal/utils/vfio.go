//go:build linux

package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type GPUDevice struct {
	PCIAddress string
	VendorID   string
	DeviceID   string
	IOMMUGroup string
	VFIODevice string
}

func GetGPUPCIAddress() (string, error) {
	cmd := exec.Command("lspci", "-D", "-nn")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "nvidia") && 
		   (strings.Contains(strings.ToLower(line), "vga") || 
		    strings.Contains(strings.ToLower(line), "3d controller")) {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				return parts[0], nil
			}
		}
	}
	return "", fmt.Errorf("no NVIDIA GPU found")
}

func GetGPUDeviceIDs() (string, string, error) {
	cmd := exec.Command("lspci", "-D", "-nn")
	output, err := cmd.Output()
	if err != nil {
		return "", "", err
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "nvidia") {
			start := strings.Index(line, "[")
			end := strings.LastIndex(line, "]")
			if start != -1 && end != -1 {
				ids := line[start+1 : end]
				parts := strings.Split(ids, ":")
				if len(parts) == 2 {
					return parts[0], parts[1], nil
				}
			}
		}
	}
	return "", "", fmt.Errorf("could not parse GPU device IDs")
}

func GetIOMMUGroup(pciAddress string) (string, error) {
	path := fmt.Sprintf("/sys/bus/pci/devices/%s/iommu_group", pciAddress)
	link, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	return filepath.Base(link), nil
}

func IsGPUBoundToVFIO(pciAddress string) bool {
	driverPath := fmt.Sprintf("/sys/bus/pci/devices/%s/driver", pciAddress)
	link, err := os.Readlink(driverPath)
	if err != nil {
		return false
	}
	return strings.Contains(link, "vfio-pci")
}

func GetGPUVFIODevice() (*GPUDevice, error) {
	pciAddr, err := GetGPUPCIAddress()
	if err != nil {
		return nil, err
	}

	vendorID, deviceID, err := GetGPUDeviceIDs()
	if err != nil {
		return nil, err
	}

	iommuGroup, err := GetIOMMUGroup(pciAddr)
	if err != nil {
		return nil, fmt.Errorf("IOMMU not enabled or GPU not in IOMMU group: %v", err)
	}

	vfioDevice := fmt.Sprintf("/dev/vfio/%s", iommuGroup)

	return &GPUDevice{
		PCIAddress: pciAddr,
		VendorID:   vendorID,
		DeviceID:   deviceID,
		IOMMUGroup: iommuGroup,
		VFIODevice: vfioDevice,
	}, nil
}

func IsIOMMUEnabled() bool {
	if _, err := os.Stat("/sys/kernel/iommu_groups"); err != nil {
		return false
	}
	entries, err := os.ReadDir("/sys/kernel/iommu_groups")
	if err != nil || len(entries) == 0 {
		return false
	}
	return true
}

