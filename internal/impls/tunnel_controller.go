package impls

import (
	"context"

	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
)

// TunnelController управляет туннельными портами агента.
type TunnelController interface {
	Configure(ctx context.Context, containerID, token string, ports domain.InstancePorts) error
	Restore(ctx context.Context) error
	Clear() error
}
