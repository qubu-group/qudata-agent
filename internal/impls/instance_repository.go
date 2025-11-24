package impls

import (
	"context"

	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
)

// InstanceRepository управляет жизненным циклом контейнера.
type InstanceRepository interface {
	Create(ctx context.Context, spec domain.InstanceSpec) (string, error)
	Manage(ctx context.Context, cmd domain.InstanceCommand) error
	Stop(ctx context.Context) error
	Cleanup(ctx context.Context) error
	Status(ctx context.Context) domain.InstanceStatus
	AddSSH(ctx context.Context, key string) error
	RemoveSSH(ctx context.Context, key string) error
	IsRunning(ctx context.Context) bool
}
