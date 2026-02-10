package domain

import "context"

// VMBackend identifies the virtualization technology used for instances.
type VMBackend string

const (
	// BackendDocker runs instances as Docker containers with shared GPU access.
	BackendDocker VMBackend = "docker"

	// BackendQEMU runs instances as QEMU VMs with exclusive VFIO GPU passthrough.
	BackendQEMU VMBackend = "qemu"
)

// VMManager defines the contract for VM backends.
// Both Docker and QEMU implementations satisfy this interface,
// allowing the agent to operate independently of the virtualization technology.
type VMManager interface {
	// Create provisions and starts a new VM instance.
	// hostPorts contains pre-allocated host ports for each entry in spec.Ports.
	Create(ctx context.Context, spec InstanceSpec, hostPorts []int) (InstancePorts, error)

	// Manage executes a lifecycle command (start, stop, restart) on the running instance.
	Manage(ctx context.Context, cmd InstanceCommand) error

	// Stop terminates the running instance and releases associated resources.
	Stop(ctx context.Context) error

	// Status returns the current lifecycle state of the instance.
	Status(ctx context.Context) InstanceStatus

	// IsRunning reports whether an instance is currently active.
	IsRunning() bool

	// VMID returns the identifier of the running instance (container ID or QEMU VM ID).
	VMID() string

	// Ports returns the current guest-to-host port mappings.
	Ports() InstancePorts

	// RestoreState reconstructs manager state from a persisted InstanceState.
	// Passing nil resets the manager to an idle state.
	RestoreState(state *InstanceState)

	// AddSSHKey installs an SSH public key inside the running instance.
	AddSSHKey(ctx context.Context, pubkey string) error

	// RemoveSSHKey removes an SSH public key from the running instance.
	RemoveSSHKey(ctx context.Context, pubkey string) error
}
