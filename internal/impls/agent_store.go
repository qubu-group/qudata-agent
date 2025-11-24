package impls

import "context"

// AgentStore отвечает за хранение идентификатора агента и секрета.
type AgentStore interface {
	AgentID(ctx context.Context) (string, error)
	Secret(ctx context.Context) (string, error)
	SaveSecret(ctx context.Context, secret string) error
	APIKey(ctx context.Context) (string, error)
	SaveAPIKey(ctx context.Context, apiKey string) error
}
