package main

import (
	"github.com/magicaleks/qudata-agent-alpha/internal/models"
	"github.com/magicaleks/qudata-agent-alpha/internal/runtime"
	"github.com/magicaleks/qudata-agent-alpha/internal/server"
	"github.com/magicaleks/qudata-agent-alpha/internal/storage"
	"github.com/magicaleks/qudata-agent-alpha/internal/utils"
)

func main() {
	rt := runtime.NewRuntime()
	if !rt.Client.Ping() {
		panic("qudata service is unavailable")
	}

	initAgent(rt)
	go rt.StatsMonitoring()
	s := server.NewServer(rt)
	s.Run()
}

func initAgent(runtime *runtime.Runtime) {
	initRequest := &models.InitAgentRequest{
		AgentID:     runtime.AgentId,
		AgentPort:   runtime.AgentPort,
		Address:     runtime.AgentAddress,
		Fingerprint: runtime.Fingerprint,
		PID:         runtime.AgentPID,
	}
	initResponse := runtime.Client.Init(initRequest)
	if initResponse == nil {
		utils.LogError("init agent error")
		panic("init agent error")
	}
	if initResponse.SecretKey != "" {
		storage.SetSecretKey(initResponse.SecretKey)
		runtime.Client.SetSecret(initResponse.SecretKey)
	}
	if !initResponse.HostExists {
		hostRequest := &models.CreateHostRequest{
			GPUName:       utils.GetGPUName(),
			GPUAmount:     utils.GetGPUCount(),
			VRAM:          utils.GetVRAM(),
			MaxCUDA:       utils.GetMaxCUDAVersion(),
			Configuration: utils.GetConfiguration(),
		}
		utils.LogInfo("creating host: %s (CUDA %.1f)", hostRequest.GPUName, hostRequest.MaxCUDA)
		runtime.Client.CreateHost(hostRequest)
	}
}
