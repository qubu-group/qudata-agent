package domain

import "context"

// VMManager defines the contract for VM backends.
type VMManager interface {
	Create(ctx context.Context, spec InstanceSpec, hostPorts []int) (InstancePorts, error)
	Manage(ctx context.Context, cmd InstanceCommand) error
	Stop(ctx context.Context) error
	Status(ctx context.Context) InstanceStatus
	IsRunning() bool
	VMID() string
	Ports() InstancePorts
	RestoreState(state *InstanceState)
	AddSSHKey(ctx context.Context, pubkey string) error
	RemoveSSHKey(ctx context.Context, pubkey string) error
}

// VMGPUMetrics holds GPU metrics collected from inside a VM.
type VMGPUMetrics struct {
	Utilization float64
	Temperature int
	MemoryUsed  uint64
	MemoryTotal uint64
}

func (m *VMGPUMetrics) MemoryUtilization() float64 {
	if m.MemoryTotal == 0 {
		return 0
	}
	return float64(m.MemoryUsed) / float64(m.MemoryTotal) * 100
}

// VMGPUMetricsProvider provides GPU metrics from inside the VM.
type VMGPUMetricsProvider interface {
	GetGPUMetrics(ctx context.Context) (*VMGPUMetrics, error)
	SSHReady() bool
}

// VMWithGPUMetrics combines VMManager with GPU metrics capability.
type VMWithGPUMetrics interface {
	VMManager
	VMGPUMetricsProvider
}
