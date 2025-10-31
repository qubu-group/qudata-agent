package internal

import (
	"github.com/magicaleks/qudata-agent-alpha/pkg/containers"
	"github.com/magicaleks/qudata-agent-alpha/pkg/utils"
	"os"
)

const (
	AgentIdPATH     = "/var/lib/gpu-agent/agent_id"
	AgentSecretPATH = "/run/lib/gpu-agent/agent_secret"
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
	Client         *ServiceClient
}

func NewRuntime() *Runtime {
	client := NewServiceClient()
	return &Runtime{
		Status:         containers.GetInstanceStatus(),
		InstanceExists: containers.InstanceIsRunning(),
		AgentId:        GetAgentId(),
		AgentPID:       os.Getpid(),
		AgentAddress:   utils.GetPublicIP(),
		AgentPort:      utils.GetFreePort(),
		Fingerprint:    utils.GetFingerprint(),
		Client:         client,
	}
}
