package impls

import "github.com/magicaleks/qudata-agent-alpha/internal/domain"

// StatsCollector возвращает текущие показатели системы.
type StatsCollector interface {
	Collect() domain.StatsSnapshot
}
