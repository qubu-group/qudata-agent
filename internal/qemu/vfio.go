package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	sysBusPCI  = "/sys/bus/pci"
	devicesDir = sysBusPCI + "/devices"
	devVFIO    = "/dev/vfio"
)

// VFIO manages PCI device binding to the vfio-pci driver for GPU passthrough.
type VFIO struct {
	addr       string
	vendorID   string
	deviceID   string
	group      string
	origDriver string
	bound      bool
}

// NewVFIO creates a VFIO manager for the given PCI address (e.g. "0000:01:00.0").
func NewVFIO(addr string) *VFIO {
	return &VFIO{addr: addr}
}

// Bind detaches the GPU from its host driver and attaches it to vfio-pci.
//
// After a successful Bind the host loses access to the GPU: NVML will stop
// working and GPU metrics must be collected from inside the VM.
func (v *VFIO) Bind() error {
	deviceDir := filepath.Join(devicesDir, v.addr)

	if _, err := os.Stat(deviceDir); err != nil {
		return fmt.Errorf("pci device %s not found: %w", v.addr, err)
	}

	// Read vendor and device identifiers.
	vendor, err := readSysfsAttr(deviceDir, "vendor")
	if err != nil {
		return fmt.Errorf("read vendor: %w", err)
	}
	device, err := readSysfsAttr(deviceDir, "device")
	if err != nil {
		return fmt.Errorf("read device: %w", err)
	}
	v.vendorID = vendor
	v.deviceID = device

	// Determine IOMMU group from the symlink target.
	groupLink, err := os.Readlink(filepath.Join(deviceDir, "iommu_group"))
	if err != nil {
		return fmt.Errorf("read iommu_group: %w (is IOMMU enabled in BIOS and kernel?)", err)
	}
	v.group = filepath.Base(groupLink)

	// Record the current driver so we can restore it later.
	if link, err := os.Readlink(filepath.Join(deviceDir, "driver")); err == nil {
		v.origDriver = filepath.Base(link)
	}

	// Unbind from the current driver.
	if v.origDriver != "" {
		unbindPath := filepath.Join(deviceDir, "driver", "unbind")
		if err := os.WriteFile(unbindPath, []byte(v.addr), 0o200); err != nil {
			return fmt.Errorf("unbind from %s: %w", v.origDriver, err)
		}
	}

	// Set driver_override so only this device is claimed by vfio-pci.
	overridePath := filepath.Join(deviceDir, "driver_override")
	if err := os.WriteFile(overridePath, []byte("vfio-pci"), 0o200); err != nil {
		return fmt.Errorf("set driver_override: %w", err)
	}

	// Trigger a driver probe to bind the device.
	probePath := filepath.Join(sysBusPCI, "drivers_probe")
	if err := os.WriteFile(probePath, []byte(v.addr), 0o200); err != nil {
		return fmt.Errorf("drivers_probe: %w", err)
	}

	// Verify the VFIO group device appeared.
	vfioDevPath := filepath.Join(devVFIO, v.group)
	if _, err := os.Stat(vfioDevPath); err != nil {
		return fmt.Errorf("vfio device %s not found after bind: %w", vfioDevPath, err)
	}

	v.bound = true
	return nil
}

// Unbind detaches the device from vfio-pci and restores the original host driver.
func (v *VFIO) Unbind() error {
	if !v.bound {
		return nil
	}

	deviceDir := filepath.Join(devicesDir, v.addr)

	// Unbind from vfio-pci.
	vfioUnbind := filepath.Join(sysBusPCI, "drivers", "vfio-pci", "unbind")
	_ = os.WriteFile(vfioUnbind, []byte(v.addr), 0o200)

	// Clear driver_override so the kernel can match the original driver.
	overridePath := filepath.Join(deviceDir, "driver_override")
	_ = os.WriteFile(overridePath, []byte("\n"), 0o200)

	// Re-probe to let the kernel bind the original driver.
	probePath := filepath.Join(sysBusPCI, "drivers_probe")
	if err := os.WriteFile(probePath, []byte(v.addr), 0o200); err != nil {
		return fmt.Errorf("drivers_probe for restore: %w", err)
	}

	v.bound = false
	return nil
}

// IOMMUGroup returns the IOMMU group number for the PCI device.
func (v *VFIO) IOMMUGroup() string {
	return v.group
}

// Addr returns the PCI address managed by this VFIO instance.
func (v *VFIO) Addr() string {
	return v.addr
}

// Bound reports whether the device is currently bound to vfio-pci.
func (v *VFIO) Bound() bool {
	return v.bound
}

// RestoreBinding re-reads sysfs to determine if the device is already bound to vfio-pci.
// This is used when recovering manager state after an agent restart.
func (v *VFIO) RestoreBinding() {
	deviceDir := filepath.Join(devicesDir, v.addr)
	if link, err := os.Readlink(filepath.Join(deviceDir, "driver")); err == nil {
		if filepath.Base(link) == "vfio-pci" {
			v.bound = true
		}
	}
}

// readSysfsAttr reads and trims a single sysfs attribute file.
func readSysfsAttr(deviceDir, attr string) (string, error) {
	data, err := os.ReadFile(filepath.Join(deviceDir, attr))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
