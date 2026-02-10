package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	sysBusPCI  = "/sys/bus/pci"
	devicesDir = sysBusPCI + "/devices"
	devVFIO    = "/dev/vfio"
)

// nvidiaModules lists the NVIDIA kernel modules in unload order.
var nvidiaModules = []string{
	"nvidia_uvm",
	"nvidia_drm",
	"nvidia_modeset",
	"nvidia",
}

// IOMMUGroupDevice represents a device in an IOMMU group.
type IOMMUGroupDevice struct {
	Addr     string // PCI address
	Vendor   string // Vendor ID
	Device   string // Device ID
	Class    string // Device class
	Driver   string // Current driver
	IsGPU    bool   // True if this is the main GPU device
	IsAudio  bool   // True if this is the NVIDIA audio device
	IsBridge bool   // True if this is a PCI bridge
}

// VFIO manages PCI device binding to the vfio-pci driver for GPU passthrough.
type VFIO struct {
	addr            string
	vendorID        string
	deviceID        string
	group           string
	origDriver      string
	bound           bool
	groupDevices    []IOMMUGroupDevice
	boundGroupAddrs []string // Other devices in group that we bound to vfio
}

// NewVFIO creates a VFIO manager for the given PCI address (e.g. "0000:01:00.0").
func NewVFIO(addr string) *VFIO {
	return &VFIO{addr: addr}
}

// Bind detaches the GPU from its host driver and attaches it to vfio-pci.
//
// After a successful Bind the host loses access to the GPU: NVML will stop
// working and GPU metrics must be collected from inside the VM.
//
// This method performs the following steps:
// 1. Validates the IOMMU group and identifies all devices in it
// 2. Unloads NVIDIA kernel modules if the GPU is currently using them
// 3. Binds all devices in the IOMMU group to vfio-pci
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

	// Validate IOMMU group and get all devices
	if err := v.validateIOMMUGroup(); err != nil {
		return err
	}

	// Record the current driver so we can restore it later.
	if link, err := os.Readlink(filepath.Join(deviceDir, "driver")); err == nil {
		v.origDriver = filepath.Base(link)
	}

	// Unload NVIDIA modules if GPU is using nvidia driver
	if v.origDriver == "nvidia" {
		if err := v.unloadNVIDIAModules(); err != nil {
			return err
		}
	}

	// Bind all devices in the IOMMU group to vfio-pci
	if err := v.bindAllGroupDevices(); err != nil {
		return err
	}

	// Verify the VFIO group device appeared.
	vfioDevPath := filepath.Join(devVFIO, v.group)
	if _, err := os.Stat(vfioDevPath); err != nil {
		return fmt.Errorf("vfio device %s not found after bind: %w", vfioDevPath, err)
	}

	v.bound = true
	return nil
}

// validateIOMMUGroup checks all devices in the IOMMU group and determines
// which ones need to be bound to vfio-pci.
func (v *VFIO) validateIOMMUGroup() error {
	groupDevicesPath := filepath.Join(devicesDir, v.addr, "iommu_group", "devices")
	entries, err := os.ReadDir(groupDevicesPath)
	if err != nil {
		return fmt.Errorf("read iommu group devices: %w", err)
	}

	v.groupDevices = nil
	var problemDevices []string

	for _, entry := range entries {
		addr := entry.Name()
		devPath := filepath.Join(devicesDir, addr)

		dev := IOMMUGroupDevice{Addr: addr}

		// Read device attributes
		dev.Vendor, _ = readSysfsAttr(devPath, "vendor")
		dev.Device, _ = readSysfsAttr(devPath, "device")
		dev.Class, _ = readSysfsAttr(devPath, "class")

		// Get current driver
		if link, err := os.Readlink(filepath.Join(devPath, "driver")); err == nil {
			dev.Driver = filepath.Base(link)
		}

		// Classify device by class code
		// 0x030000 = VGA controller (GPU)
		// 0x030200 = 3D controller
		// 0x040300 = Audio device
		// 0x060400 = PCI bridge
		classCode := strings.TrimPrefix(dev.Class, "0x")
		switch {
		case strings.HasPrefix(classCode, "0300") || strings.HasPrefix(classCode, "0302"):
			dev.IsGPU = true
		case strings.HasPrefix(classCode, "0403"):
			dev.IsAudio = true
		case strings.HasPrefix(classCode, "0604"):
			dev.IsBridge = true
		}

		v.groupDevices = append(v.groupDevices, dev)

		// Check for problematic devices
		// PCI bridges usually cannot be unbound from pcieport driver
		// Other non-GPU devices that aren't NVIDIA audio are problematic
		if dev.IsBridge {
			// PCI bridges are usually OK, they don't need to be passed through
			continue
		}

		// NVIDIA audio devices should be bound together with the GPU
		if dev.IsAudio && strings.HasPrefix(dev.Vendor, "0x10de") {
			continue
		}

		// Main GPU device
		if dev.IsGPU && addr == v.addr {
			continue
		}

		// Warn about other devices in the group
		if !dev.IsGPU && !dev.IsAudio && dev.Driver != "" && dev.Driver != "vfio-pci" {
			problemDevices = append(problemDevices, fmt.Sprintf("%s (class %s, driver %s)", addr, dev.Class, dev.Driver))
		}
	}

	if len(problemDevices) > 0 {
		return fmt.Errorf("IOMMU group %s contains devices that may prevent passthrough:\n  %s\n\nEither:\n1. Bind all devices to vfio-pci manually\n2. Use ACS override patch to isolate the GPU\n3. Use a different PCIe slot",
			v.group, strings.Join(problemDevices, "\n  "))
	}

	return nil
}

// unloadNVIDIAModules attempts to unload the NVIDIA kernel modules.
// Returns an error if the GPU is in use and modules cannot be unloaded.
func (v *VFIO) unloadNVIDIAModules() error {
	// Check if any NVIDIA modules are loaded
	modsLoaded := false
	for _, mod := range nvidiaModules {
		if isModuleLoaded(mod) {
			modsLoaded = true
			break
		}
	}

	if !modsLoaded {
		return nil
	}

	// Try to unload modules in order
	for _, mod := range nvidiaModules {
		if !isModuleLoaded(mod) {
			continue
		}

		cmd := exec.Command("rmmod", mod)
		out, err := cmd.CombinedOutput()
		if err != nil {
			// Check if module is in use
			if strings.Contains(string(out), "in use") || strings.Contains(string(out), "Module") {
				return fmt.Errorf("GPU is in use, cannot bind to VFIO: failed to unload %s: %s",
					mod, strings.TrimSpace(string(out)))
			}
			return fmt.Errorf("failed to unload NVIDIA module %s: %w: %s", mod, err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}

// bindAllGroupDevices binds the main GPU and related devices (like NVIDIA audio)
// in the IOMMU group to vfio-pci.
func (v *VFIO) bindAllGroupDevices() error {
	for _, dev := range v.groupDevices {
		// Skip PCI bridges - they don't need to be passed through
		if dev.IsBridge {
			continue
		}

		// Only bind GPU and related NVIDIA devices (audio)
		if !dev.IsGPU && !(dev.IsAudio && strings.HasPrefix(dev.Vendor, "0x10de")) {
			continue
		}

		// Already bound to vfio-pci
		if dev.Driver == "vfio-pci" {
			continue
		}

		if err := v.bindSingleDevice(dev.Addr); err != nil {
			return fmt.Errorf("bind device %s: %w", dev.Addr, err)
		}

		if dev.Addr != v.addr {
			v.boundGroupAddrs = append(v.boundGroupAddrs, dev.Addr)
		}
	}

	return nil
}

// bindSingleDevice binds a single PCI device to vfio-pci.
func (v *VFIO) bindSingleDevice(addr string) error {
	deviceDir := filepath.Join(devicesDir, addr)

	// Unbind from current driver if any
	if link, err := os.Readlink(filepath.Join(deviceDir, "driver")); err == nil {
		driver := filepath.Base(link)
		if driver != "vfio-pci" {
			unbindPath := filepath.Join(deviceDir, "driver", "unbind")
			if err := os.WriteFile(unbindPath, []byte(addr), 0o200); err != nil {
				return fmt.Errorf("unbind from %s: %w", driver, err)
			}
		}
	}

	// Set driver_override
	overridePath := filepath.Join(deviceDir, "driver_override")
	if err := os.WriteFile(overridePath, []byte("vfio-pci"), 0o200); err != nil {
		return fmt.Errorf("set driver_override: %w", err)
	}

	// Trigger driver probe
	probePath := filepath.Join(sysBusPCI, "drivers_probe")
	if err := os.WriteFile(probePath, []byte(addr), 0o200); err != nil {
		return fmt.Errorf("drivers_probe: %w", err)
	}

	return nil
}

// isModuleLoaded checks if a kernel module is currently loaded.
func isModuleLoaded(module string) bool {
	data, err := os.ReadFile("/proc/modules")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == module {
			return true
		}
	}
	return false
}

// Unbind detaches the device from vfio-pci and restores the original host driver.
// It also unbinds any related devices (like NVIDIA audio) that were bound to vfio.
func (v *VFIO) Unbind() error {
	if !v.bound {
		return nil
	}

	// Unbind all devices that we bound (in reverse order)
	allAddrs := append([]string{v.addr}, v.boundGroupAddrs...)
	for i := len(allAddrs) - 1; i >= 0; i-- {
		addr := allAddrs[i]
		v.unbindSingleDevice(addr)
	}

	v.bound = false
	v.boundGroupAddrs = nil
	return nil
}

// unbindSingleDevice unbinds a single device from vfio-pci and restores its original driver.
func (v *VFIO) unbindSingleDevice(addr string) {
	deviceDir := filepath.Join(devicesDir, addr)

	// Unbind from vfio-pci
	vfioUnbind := filepath.Join(sysBusPCI, "drivers", "vfio-pci", "unbind")
	_ = os.WriteFile(vfioUnbind, []byte(addr), 0o200)

	// Clear driver_override
	overridePath := filepath.Join(deviceDir, "driver_override")
	_ = os.WriteFile(overridePath, []byte("\n"), 0o200)

	// Re-probe to let kernel bind original driver
	probePath := filepath.Join(sysBusPCI, "drivers_probe")
	_ = os.WriteFile(probePath, []byte(addr), 0o200)
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
