package system

import (
	"context"

	"github.com/qudata/agent/internal/domain"
)

type StatsCollector struct {
	vm domain.VMManager
}

func NewStatsCollector(vm domain.VMManager) *StatsCollector {
	return &StatsCollector{vm: vm}
}

func (c *StatsCollector) Collect(ctx context.Context) domain.StatsReport {
	return domain.StatsReport{
		Status: c.vm.Status(ctx),
	}
}
