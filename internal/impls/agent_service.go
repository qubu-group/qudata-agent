package impls

import (
	"context"

	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
)

// AgentService описывает взаимодействие с удаленным API Qudata.
type AgentService interface {
	Ping(ctx context.Context) error
	InitAgent(ctx context.Context, req domain.InitAgentRequest) (*domain.InitAgentResponse, error)
	RegisterHost(ctx context.Context, req domain.CreateHostRequest) error
	SendStats(ctx context.Context, stats domain.StatsSnapshot) error
	UseSecret(secret string)
}
