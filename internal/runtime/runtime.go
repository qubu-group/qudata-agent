package runtime

import (
	"github.com/magicaleks/qudata-agent-alpha/internal/containers"
	"github.com/magicaleks/qudata-agent-alpha/internal/models"
	"github.com/magicaleks/qudata-agent-alpha/internal/service"
	"github.com/magicaleks/qudata-agent-alpha/internal/storage"
	"github.com/magicaleks/qudata-agent-alpha/internal/utils"
	"os"
	"time"
)

var (
	_ = os.MkdirAll("/var/lib/gpu-agent", 0777)
	_ = os.MkdirAll("/run/lib/gpu-agent", 0777)
)

type Runtime struct {
	Status         containers.InstanceStatus
	InstanceExists bool
	AgentId        string // Unique agent ID
	AgentPID       int    // PID of main process
	AgentAddress   string // Host public address
	AgentPort      int    // Port the agent serves
	Fingerprint    string // Machine fingerprint
	Client         *service.Client
}

func NewRuntime() *Runtime {
	client := service.NewServiceClient()
	return &Runtime{
		Status:         containers.GetInstanceStatus(),
		InstanceExists: containers.InstanceIsRunning(),
		AgentId:        storage.GetAgentId(),
		AgentPID:       os.Getpid(),
		AgentAddress:   utils.GetPublicIP(),
		AgentPort:      utils.GetFreePort(),
		Fingerprint:    utils.GetFingerprint(),
		Client:         client,
	}
}

// StatsMonitoring is background task which sends instance stats to qudata
func (r *Runtime) StatsMonitoring() {
	var request *models.StatsRequest
	for {
		if r.InstanceExists {
			request = &models.StatsRequest{
				Status: containers.GetInstanceStatus(),
			}
			r.Client.Stats(request)
			time.Sleep(800 * time.Millisecond)
		} else {
			time.Sleep(5 * time.Second)
			r.InstanceExists = containers.InstanceIsRunning()
		}
	}
}
