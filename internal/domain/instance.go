package domain

// InstanceStatus represents the lifecycle state of a VM instance.
type InstanceStatus string

const (
	StatusDestroyed InstanceStatus = "destroyed"
	StatusPending   InstanceStatus = "pending"
	StatusRunning   InstanceStatus = "running"
	StatusPaused    InstanceStatus = "paused"
	StatusRebooting InstanceStatus = "rebooting"
	StatusError     InstanceStatus = "error"
)

// InstanceCommand is an action to perform on a running instance.
type InstanceCommand string

const (
	CommandStart  InstanceCommand = "start"
	CommandStop   InstanceCommand = "stop"
	CommandReboot InstanceCommand = "restart"
)

// PortMapping describes how a container port is exposed via FRP.
type PortMapping struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port,omitempty"`
	RemotePort    int    `json:"remote_port"`
	Proto         string `json:"proto"` // "tcp" or "http"
}

// InstanceSpec contains all parameters needed to create a VM instance.
type InstanceSpec struct {
	Image      string            `json:"image"`
	ImageTag   string            `json:"image_tag"`
	Registry   string            `json:"registry"`
	Login      string            `json:"login"`
	Password   string            `json:"password"`
	EnvVars    map[string]string `json:"env_variables"`
	Ports      []PortMapping     `json:"ports"`
	Command    string            `json:"command"`
	SSHEnabled bool              `json:"ssh_enabled"`
	StorageGB  int               `json:"storage_gb"`
	CPUs       string            `json:"cpus,omitempty"`
	Memory     string            `json:"memory,omitempty"`

	// GPUAddr is the PCI address for VFIO passthrough (e.g. "0000:01:00.0").
	// Used only by the QEMU backend; ignored by Docker.
	GPUAddr string `json:"gpu_addr,omitempty"`

	// DiskSizeGB is the qcow2 disk size in gigabytes for QEMU instances.
	DiskSizeGB int `json:"disk_size_gb,omitempty"`
}

// InstancePorts maps container ports to allocated host ports.
// Key: container port (e.g. "22"), Value: host port (e.g. "45001").
type InstancePorts map[string]string

// InstanceState is the persisted state of a running instance.
type InstanceState struct {
	ContainerID string        `json:"container_id"`
	Image       string        `json:"image"`
	Ports       InstancePorts `json:"ports"`
	FRPProxies  []FRPProxy    `json:"frp_proxies"`
	SSHEnabled  bool          `json:"ssh_enabled"`

	// GPUAddr is the PCI address of the GPU bound via VFIO.
	// Present only for QEMU backend instances.
	GPUAddr string `json:"gpu_addr,omitempty"`
}

// FRPProxy describes a single FRP proxy entry for an instance.
type FRPProxy struct {
	Name         string `json:"name"`
	Type         string `json:"type"` // "tcp" or "http"
	LocalPort    int    `json:"local_port"`
	RemotePort   int    `json:"remote_port,omitempty"`
	CustomDomain string `json:"custom_domain,omitempty"`
}
