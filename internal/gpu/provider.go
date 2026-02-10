package gpu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/qudata/agent/internal/domain"
)

// gpuInfoFile is the JSON produced by install.py during the GPU passthrough test.
type gpuInfoFile struct {
	Name          string  `json:"name"`
	Count         int     `json:"count"`
	VRAMGB        float64 `json:"vram_gb"`
	MaxCUDA       float64 `json:"max_cuda"`
	DriverVersion string  `json:"driver_version"`
}

// FileInfoProvider reads GPU info from a JSON file written by the installer.
// This is the primary provider because NVIDIA drivers are blacklisted on the
// host (for VFIO) and NVML is unavailable. The installer queries nvidia-smi
// inside a test VM where drivers are always present.
type FileInfoProvider struct {
	Path string
}

func (p *FileInfoProvider) GPUInfo(_ context.Context) (*domain.GPUInfo, error) {
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, fmt.Errorf("read gpu info %s: %w", p.Path, err)
	}

	var info gpuInfoFile
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse gpu info: %w", err)
	}

	if info.Name == "" {
		return nil, fmt.Errorf("gpu info file has empty name")
	}

	count := info.Count
	if count < 1 {
		count = 1
	}

	return &domain.GPUInfo{
		Name:    formatGPUName(info.Name),
		Count:   count,
		VRAM:    info.VRAMGB,
		MaxCUDA: info.MaxCUDA,
	}, nil
}

// formatGPUName removes "NVIDIA " prefix and collapses spaces: "NVIDIA Tesla T4" -> "TeslaT4".
func formatGPUName(name string) string {
	name = strings.TrimPrefix(name, "NVIDIA ")
	name = strings.ReplaceAll(name, " ", "")
	return name
}

// MockInfoProvider returns zero-value GPU info for debug mode.
type MockInfoProvider struct{}

func (MockInfoProvider) GPUInfo(_ context.Context) (*domain.GPUInfo, error) {
	return &domain.GPUInfo{
		Name:  "Debug GPU",
		Count: 1,
	}, nil
}
