package impls

import "github.com/magicaleks/qudata-agent-alpha/internal/domain"

// EnvironmentProbe предоставляет информацию об окружении узла.
type EnvironmentProbe interface {
	AgentPort() (int, error)
	PublicIP() string
	Fingerprint() string
	Configuration() domain.ConfigurationData
	GPUName() string
	GPUCount() int
	VRAM() float64
	MaxCUDA() float64
}
