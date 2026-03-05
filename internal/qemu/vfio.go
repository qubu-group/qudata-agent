package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	sysBusPCI  = "/sys/bus/pci"
	devicesDir = sysBusPCI + "/devices"
	devVFIO    = "/dev/vfio"

	rmmodTimeout = 30 * time.Second
)

var nvidiaModules = []string{
	"nvidia_uvm",
	"nvidia_drm",
	"nvidia_modeset",
	"nvidia",
}

var nvidiaServices = []string{
	"nvidia-persistenced",
	"nvidia-fabricmanager",
	"nvidia-powerd",
	"dcgm",
}

// IOMMUGroupDevice represents a device in an IOMMU group.
type IOMMUGroupDevice struct {
	Addr     string
	Vendor   string
	Device   string
	Class    string
	Driver   string
	IsGPU    bool
	IsAudio  bool
	IsBridge bool
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
	boundGroupAddrs []string
}

// NewVFIO creates a VFIO manager for the given PCI address (e.g. "0000:01:00.0").
func NewVFIO(addr string) *VFIO {
	return &VFIO{addr: addr}
}

// Bind detaches the GPU from its host driver and attaches it to vfio-pci.
//
// Safety: refuses to hot-unbind nouveau (kernel crash risk).
// The install script ensures nouveau is blacklisted before the agent runs.
func (v *VFIO) Bind() error {
	deviceDir := filepath.Join(devicesDir, v.addr)

	if _, err := os.Stat(deviceDir); err != nil {
		return fmt.Errorf("pci device %s not found: %w", v.addr, err)
	}

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

	groupLink, err := os.Readlink(filepath.Join(deviceDir, "iommu_group"))
	if err != nil {
		return fmt.Errorf("read iommu_group: %w (is IOMMU enabled in BIOS and kernel?)", err)
	}
	v.group = filepath.Base(groupLink)

	if err := v.validateIOMMUGroup(); err != nil {
		return err
	}

	if link, err := os.Readlink(filepath.Join(deviceDir, "driver")); err == nil {
		v.origDriver = filepath.Base(link)
	}

	if v.origDriver == "nouveau" {
		return fmt.Errorf(
			"GPU %s is bound to nouveau — cannot hot-detach safely (kernel crash risk). "+
				"Blacklist nouveau and reboot first", v.addr)
	}

	if err := v.unloadGPUModules(); err != nil {
		return err
	}

	if err := v.bindAllGroupDevices(); err != nil {
		return err
	}

	vfioDevPath := filepath.Join(devVFIO, v.group)
	if _, err := os.Stat(vfioDevPath); err != nil {
		return fmt.Errorf("vfio device %s not found after bind: %w", vfioDevPath, err)
	}

	v.bound = true
	return nil
}

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
		dev.Vendor, _ = readSysfsAttr(devPath, "vendor")
		dev.Device, _ = readSysfsAttr(devPath, "device")
		dev.Class, _ = readSysfsAttr(devPath, "class")

		if link, err := os.Readlink(filepath.Join(devPath, "driver")); err == nil {
			dev.Driver = filepath.Base(link)
		}

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

		if dev.IsBridge {
			continue
		}
		if dev.IsAudio && strings.HasPrefix(dev.Vendor, "0x10de") {
			continue
		}
		if dev.IsGPU && addr == v.addr {
			continue
		}
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

func (v *VFIO) unloadGPUModules() error {
	if v.origDriver != "nvidia" {
		return nil
	}

	for _, svc := range nvidiaServices {
		_ = exec.Command("systemctl", "stop", svc).Run()
	}

	unbindVTConsoles()

	for _, mod := range nvidiaModules {
		if !isModuleLoaded(mod) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), rmmodTimeout)
		cmd := exec.CommandContext(ctx, "rmmod", mod)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("rmmod %s timed out after %v — GPU may be in use by another process", mod, rmmodTimeout)
			}
			if strings.Contains(msg, "in use") {
				return fmt.Errorf("GPU is in use, cannot bind to VFIO: failed to unload %s: %s", mod, msg)
			}
			return fmt.Errorf("failed to unload module %s: %w: %s", mod, err, msg)
		}
	}
	return nil
}

// unbindVTConsoles detaches VT consoles from the framebuffer so GPU
// kernel modules (nvidia_drm) can be unloaded.
func unbindVTConsoles() {
	entries, err := os.ReadDir("/sys/class/vtconsole")
	if err != nil {
		return
	}
	for _, entry := range entries {
		bindPath := filepath.Join("/sys/class/vtconsole", entry.Name(), "bind")
		data, err := os.ReadFile(bindPath)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "1" {
			_ = os.WriteFile(bindPath, []byte("0"), 0o200)
		}
	}
}

func (v *VFIO) bindAllGroupDevices() error {
	for _, dev := range v.groupDevices {
		if dev.IsBridge {
			continue
		}
		if !dev.IsGPU && !(dev.IsAudio && strings.HasPrefix(dev.Vendor, "0x10de")) {
			continue
		}

		currentDriver := readPCIDriver(dev.Addr)
		if currentDriver == "vfio-pci" {
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

func (v *VFIO) bindSingleDevice(addr string) error {
	deviceDir := filepath.Join(devicesDir, addr)

	if link, err := os.Readlink(filepath.Join(deviceDir, "driver")); err == nil {
		driver := filepath.Base(link)
		if driver != "vfio-pci" {
			unbindPath := filepath.Join(deviceDir, "driver", "unbind")
			if err := os.WriteFile(unbindPath, []byte(addr), 0o200); err != nil {
				return fmt.Errorf("unbind from %s: %w", driver, err)
			}
		}
	}

	overridePath := filepath.Join(deviceDir, "driver_override")
	if err := os.WriteFile(overridePath, []byte("vfio-pci"), 0o200); err != nil {
		return fmt.Errorf("set driver_override: %w", err)
	}

	probePath := filepath.Join(sysBusPCI, "drivers_probe")
	if err := os.WriteFile(probePath, []byte(addr), 0o200); err != nil {
		return fmt.Errorf("drivers_probe: %w", err)
	}

	return nil
}

func readPCIDriver(addr string) string {
	link, err := os.Readlink(filepath.Join(devicesDir, addr, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(link)
}

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
func (v *VFIO) Unbind() error {
	if !v.bound {
		return nil
	}

	allAddrs := append([]string{v.addr}, v.boundGroupAddrs...)
	for i := len(allAddrs) - 1; i >= 0; i-- {
		v.unbindSingleDevice(allAddrs[i])
	}

	v.bound = false
	v.boundGroupAddrs = nil
	return nil
}

func (v *VFIO) unbindSingleDevice(addr string) {
	deviceDir := filepath.Join(devicesDir, addr)

	vfioUnbind := filepath.Join(sysBusPCI, "drivers", "vfio-pci", "unbind")
	_ = os.WriteFile(vfioUnbind, []byte(addr), 0o200)

	overridePath := filepath.Join(deviceDir, "driver_override")
	_ = os.WriteFile(overridePath, []byte("\n"), 0o200)

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
func (v *VFIO) RestoreBinding() {
	deviceDir := filepath.Join(devicesDir, v.addr)
	if link, err := os.Readlink(filepath.Join(deviceDir, "driver")); err == nil {
		if filepath.Base(link) == "vfio-pci" {
			v.bound = true
		}
	}
}

func readSysfsAttr(deviceDir, attr string) (string, error) {
	data, err := os.ReadFile(filepath.Join(deviceDir, attr))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
