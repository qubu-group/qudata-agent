package domain

import "context"

type VMManager interface {
	Create(ctx context.Context, spec InstanceSpec, hostPorts []int) (InstancePorts, error)
	Stop(ctx context.Context) error
	Manage(ctx context.Context, cmd InstanceCommand) error
	Status(ctx context.Context) InstanceStatus
	CollectStats(ctx context.Context) *StatsSnapshot
	VMID() string
	AddSSHKey(ctx context.Context, pubkey string) error
	RemoveSSHKey(ctx context.Context, pubkey string) error
	// MarkFailed signals that instance creation failed so that Status returns StatusError.
	MarkFailed()
	// Invalidate clears cached SSH client so that awaitSSH waits for a fresh one.
	Invalidate()
}
