package domain

// InstanceStatus отражает текущее состояние вычислительного инстанса.
type InstanceStatus string

const (
	InstancePending   InstanceStatus = "pending"
	InstanceRunning   InstanceStatus = "running"
	InstancePaused    InstanceStatus = "paused"
	InstanceRebooting InstanceStatus = "rebooting"
	InstanceError     InstanceStatus = "error"
	InstanceDestroyed InstanceStatus = "destroyed"
)

// InstanceCommand задает желаемое действие над инстансом.
type InstanceCommand string

const (
	CommandStart   InstanceCommand = "start"
	CommandStop    InstanceCommand = "stop"
	CommandReboot  InstanceCommand = "reboot"
	CommandDelete  InstanceCommand = "delete"
	CommandUnknown InstanceCommand = "unknown"
)

// InstancePorts сопоставляет порты контейнера и хоста.
type InstancePorts map[string]string

// InstanceSpec описывает параметры запуска контейнера.
type InstanceSpec struct {
	Image       string
	CPUs        string
	Memory      string
	VolumeSize  int64
	Registry    string
	Login       string
	Password    string
	EnvVars     map[string]string
	Ports       InstancePorts
	Command     string
	SSHEnabled  bool
	TunnelToken string
}
