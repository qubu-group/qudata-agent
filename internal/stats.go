package internal

import (
	"github.com/magicaleks/qudata-agent-alpha/pkg/containers"
	"time"
)

// StatsMonitoring is background task which sends instance stats to qudata
func StatsMonitoring(runtime *Runtime) {
	var request *StatsRequest
	for {
		if runtime.InstanceExists {
			request = &StatsRequest{
				Status: containers.GetInstanceStatus(),
			}
			runtime.Client.Stats(request)
			time.Sleep(800 * time.Millisecond)
		} else {
			time.Sleep(5 * time.Second)
		}
	}
}
