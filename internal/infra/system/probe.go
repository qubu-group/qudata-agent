package system

import (
	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/network"
)

// Probe реализует impls.EnvironmentProbe.
type Probe struct {
	allocator *network.Allocator
}

func NewProbe(allocator *network.Allocator) *Probe {
	return &Probe{allocator: allocator}
}

func (p *Probe) AgentPort() (int, error) {
	return p.allocator.AgentPort()
}

func (p *Probe) PublicIP() string {
	return network.PublicIP()
}

func (p *Probe) Fingerprint() string {
	return machineFingerprint()
}

func (p *Probe) Configuration() domain.ConfigurationData {
	return configurationSnapshot()
}

func (p *Probe) GPUName() string {
	return gpuNameSafe()
}

func (p *Probe) GPUCount() int {
	return gpuCountSafe()
}

func (p *Probe) VRAM() float64 {
	return gpuVRAMSafe()
}

func (p *Probe) MaxCUDA() float64 {
	return gpuMaxCUDASafe()
}
