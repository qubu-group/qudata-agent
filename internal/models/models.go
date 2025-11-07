package models

import (
	"github.com/magicaleks/qudata-agent-alpha/internal/containers"
	"github.com/magicaleks/qudata-agent-alpha/internal/utils"
)

// EmptyResponse is just empty for ping request
type EmptyResponse struct{}

// InitAgentRequest inits agent in qudata backend
type InitAgentRequest struct {
	AgentID     string `json:"agent_id"`
	AgentPort   int    `json:"agent_port"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	PID         int    `json:"pid"`
}

type InitAgentResponse struct {
	AgentCreated    bool   `json:"agent_created"`
	EmergencyReinit bool   `json:"emergency_reinit"`
	HostExists      bool   `json:"host_exists"`
	SecretKey       string `json:"secret_key,omitempty"`
}

type Location struct {
	City    string `json:"city,omitempty"`
	Country string `json:"country,omitempty"`
	Region  string `json:"region,omitempty"`
}

// CreateHostRequest registers host object in qudata
type CreateHostRequest struct {
	GPUName       string                  `json:"gpu_name"`
	GPUAmount     int                     `json:"gpu_amount"`
	VRAM          float64                 `json:"vram"`
	MaxCUDA       float64                 `json:"max_cuda"`
	Location      Location                `json:"location,omitempty"`
	Configuration utils.ConfigurationData `json:"configuration"`
}

// StatsRequest updates instance stats
type StatsRequest struct {
	GPUUtil float64                   `json:"gpu_util"`
	CPUUtil float64                   `json:"cpu_util"`
	RAMUtil float64                   `json:"ram_util"`
	MemUtil float64                   `json:"mem_util"`
	InetIn  int                       `json:"inet_in"`
	InetOut int                       `json:"inet_out"`
	Status  containers.InstanceStatus `json:"status,omitempty"`
}
