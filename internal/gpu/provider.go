package gpu

import (
	"context"

	"github.com/qudata/agent/internal/domain"
)

type HostInfoProvider struct {
	Metrics *Metrics
}

func (p *HostInfoProvider) GPUInfo(_ context.Context) (*domain.GPUInfo, error) {
	return &domain.GPUInfo{
		Name:    p.Metrics.Name(),
		Count:   p.Metrics.Count(),
		VRAM:    p.Metrics.VRAM(),
		MaxCUDA: p.Metrics.MaxCUDAVersion(),
	}, nil
}

type MockInfoProvider struct{}

func (MockInfoProvider) GPUInfo(_ context.Context) (*domain.GPUInfo, error) {
	return &domain.GPUInfo{
		Name:    "Mock GPU",
		Count:   1,
		VRAM:    0,
		MaxCUDA: 0,
	}, nil
}
