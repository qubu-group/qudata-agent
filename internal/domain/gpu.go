package domain

import "context"

type GPUInfo struct {
	Name    string
	Count   int
	VRAM    float64
	MaxCUDA float64
}

type GPUInfoProvider interface {
	GPUInfo(ctx context.Context) (*GPUInfo, error)
}
