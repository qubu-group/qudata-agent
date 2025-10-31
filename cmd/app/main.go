package main

import (
	"github.com/magicaleks/qudata-agent-alpha/internal"
	"github.com/magicaleks/qudata-agent-alpha/internal/server"
	"github.com/magicaleks/qudata-agent-alpha/pkg/utils"
)

func main() {
	runtime := internal.NewRuntime()
	if !runtime.Client.Ping() {
		panic("qudata service is unavailable")
	}

	initAgent(runtime)
	go internal.StatsMonitoring(runtime)
	// TODO: run subprocess security monitoring
	s := server.NewServer(runtime)
	s.Run()
}

func initAgent(runtime *internal.Runtime) {
	initRequest := &internal.InitAgentRequest{
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
		internal.SetSecretKey(initResponse.SecretKey)
		runtime.Client.SetSecret(initResponse.SecretKey)
	}
	if !initResponse.HostExists {
		hostRequest := &internal.CreateHostRequest{
			GPUName:       utils.GetGPUName(),
			GPUAmount:     utils.GetGPUCount(),
			VRAM:          utils.GetVRAM(),
			Configuration: utils.GetConfiguration(),
		}
		runtime.Client.CreateHost(hostRequest)
	}
}
