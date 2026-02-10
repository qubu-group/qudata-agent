package domain

type InstanceStatus string

const (
	StatusDestroyed InstanceStatus = "destroyed"
	StatusPending   InstanceStatus = "pending"
	StatusRunning   InstanceStatus = "running"
	StatusPaused    InstanceStatus = "paused"
	StatusRebooting InstanceStatus = "rebooting"
	StatusError     InstanceStatus = "error"
)

type InstanceCommand string

const (
	CommandStart  InstanceCommand = "start"
	CommandStop   InstanceCommand = "stop"
	CommandReboot InstanceCommand = "restart"
)

type PortMapping struct {
	Name       string `json:"name,omitempty"`
	GuestPort  int    `json:"guest_port"`
	RemotePort int    `json:"remote_port"`
	Proto      string `json:"proto"`
}

type InstanceSpec struct {
	Ports        []PortMapping `json:"ports"`
	SSHEnabled   bool          `json:"ssh_enabled"`
	SecretDomain string        `json:"secret_domain"`
	GPUAddr      string        `json:"gpu_addr,omitempty"`
	DiskSizeGB   int           `json:"disk_size_gb,omitempty"`
	CPUs         string        `json:"cpus,omitempty"`
	Memory       string        `json:"memory,omitempty"`
}

// InstancePorts maps guest port (e.g. "22") to allocated host port (e.g. "45001").
type InstancePorts map[string]string

type InstanceState struct {
	VMID         string        `json:"vm_id"`
	Ports        InstancePorts `json:"ports"`
	SSHEnabled   bool          `json:"ssh_enabled"`
	GPUAddr      string        `json:"gpu_addr"`
	SecretDomain string        `json:"secret_domain"`
}
