package system

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/qudata/agent/internal/domain"
	"github.com/qudata/agent/internal/gpu"
)

// Probe collects static system information for host registration.
type Probe struct {
	gpu *gpu.Metrics
}

// NewProbe creates a system probe backed by the given GPU metrics provider.
func NewProbe(gpuMetrics *gpu.Metrics) *Probe {
	return &Probe{gpu: gpuMetrics}
}

// HostRegistration builds a CreateHostRequest from detected hardware.
func (p *Probe) HostRegistration() domain.CreateHostRequest {
	ramGB := totalRAMGB()
	diskGB := totalDiskGB()

	return domain.CreateHostRequest{
		GPUName:   p.gpu.Name(),
		GPUAmount: p.gpu.Count(),
		VRAM:      p.gpu.VRAM(),
		MaxCUDA:   p.gpu.MaxCUDAVersion(),
		Location:  detectLocation(),
		Configuration: domain.HostConfig{
			RAM:            domain.ResourceUnit{Amount: ramGB, Unit: "gb"},
			Disk:           domain.ResourceUnit{Amount: diskGB, Unit: "gb"},
			CPUName:        cpuName(),
			CPUCores:       runtime.NumCPU(),
			CPUFreq:        cpuFreqGHz(),
			MaxCUDAVersion: p.gpu.MaxCUDAVersion(),
		},
	}
}

// Fingerprint returns a unique machine fingerprint.
func (p *Probe) Fingerprint() string {
	return p.gpu.GetFingerprint()
}

// PublicIP detects the public IPv4 address of this machine.
func PublicIP() string {
	client := &http.Client{Timeout: 5 * time.Second}
	for _, url := range []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	} {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		ip := strings.TrimSpace(string(body))
		if ip != "" {
			return ip
		}
	}
	return "0.0.0.0"
}

// --- internal helpers ---

func totalRAMGB() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			var kb uint64
			fmt.Sscanf(line, "MemTotal: %d kB", &kb)
			return float64(kb) / (1024 * 1024)
		}
	}
	return 0
}

func totalDiskGB() float64 {
	// Use /proc/partitions or statvfs for root filesystem
	var stat struct {
		Bavail uint64
		Bsize  uint64
		Blocks uint64
	}
	// Simple fallback: read from df-like mechanism
	data, err := os.ReadFile("/proc/partitions")
	if err != nil {
		return 0
	}
	var totalKB uint64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 4 {
			var blocks uint64
			fmt.Sscanf(fields[2], "%d", &blocks)
			name := fields[3]
			// Only count whole disks (sda, nvme0n1, vda), not partitions
			if isWholeDisk(name) {
				totalKB += blocks
			}
		}
	}
	_ = stat
	return float64(totalKB) / (1024 * 1024)
}

func isWholeDisk(name string) bool {
	// sda, sdb, vda, nvme0n1 — but not sda1, nvme0n1p1
	if strings.HasPrefix(name, "sd") && len(name) == 3 {
		return true
	}
	if strings.HasPrefix(name, "vd") && len(name) == 3 {
		return true
	}
	if strings.HasPrefix(name, "nvme") && strings.HasSuffix(name, "n1") && !strings.Contains(name, "p") {
		return true
	}
	return false
}

func cpuName() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "unknown"
}

func cpuFreqGHz() float64 {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "cpu MHz") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				var mhz float64
				fmt.Sscanf(strings.TrimSpace(parts[1]), "%f", &mhz)
				return mhz / 1000.0
			}
		}
	}
	return 0
}

func detectLocation() domain.HostLocation {
	// Default location; can be enhanced with GeoIP later.
	return domain.HostLocation{
		City:    "",
		Country: "",
		Region:  "",
	}
}
