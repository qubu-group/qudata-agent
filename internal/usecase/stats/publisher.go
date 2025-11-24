package stats

import (
	"context"
	"time"

	"github.com/magicaleks/qudata-agent-alpha/internal/impls"
)

type Publisher struct {
	collector impls.StatsCollector
	api       impls.AgentService
	instances impls.InstanceRepository
	logger    impls.Logger
	interval  time.Duration
}

func NewPublisher(collector impls.StatsCollector, api impls.AgentService, instances impls.InstanceRepository, logger impls.Logger, interval time.Duration) *Publisher {
	return &Publisher{
		collector: collector,
		api:       api,
		instances: instances,
		logger:    logger,
		interval:  interval,
	}
}

func (p *Publisher) Start(ctx context.Context) {
	go p.loop(ctx)
}

func (p *Publisher) loop(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	counter := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !p.instances.IsRunning(ctx) {
				continue
			}

			snapshot := p.collector.Collect()
			snapshot.Status = p.instances.Status(ctx)

			if counter%20 == 0 {
				p.logger.Warn("Current stats: %s GPU: %.1f%% (%d°C) CPU: %.1f%%",
					snapshot.Status, snapshot.GPUUtil, snapshot.GPUTemp, snapshot.CPUUtil)
			}
			counter++

			if err := p.api.SendStats(ctx, snapshot); err != nil {
				p.logger.Warn("failed to send stats: %v", err)
			}
		}
	}
}
