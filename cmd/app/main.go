package main

import (
	"os"

	"github.com/magicaleks/qudata-agent-alpha/internal/containers"
	"github.com/magicaleks/qudata-agent-alpha/internal/models"
	"github.com/magicaleks/qudata-agent-alpha/internal/runtime"
	"github.com/magicaleks/qudata-agent-alpha/internal/server"
	"github.com/magicaleks/qudata-agent-alpha/internal/storage"
	"github.com/magicaleks/qudata-agent-alpha/internal/utils"
)

func main() {
	utils.InitNetwork()
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

	// Если инстанс не запущен - удаляем все Docker контейнеры и образы
	if !initResponse.InstanceRunning {
		utils.LogInfo("No instance running, cleaning up Docker resources")
		containers.CleanupDocker()
	}

	if !initResponse.HostExists {
		// Используем безопасные обертки, которые автоматически учитывают QUDATA_AGENT_DEBUG
		hostRequest := &models.CreateHostRequest{
			GPUName:       utils.GetGPUNameSafe(),
			GPUAmount:     utils.GetGPUCountSafe(),
			VRAM:          utils.GetVRAMSafe(),
			MaxCUDA:       utils.GetMaxCUDAVersionSafe(),
			Configuration: utils.GetConfiguration(),
		}

		if os.Getenv("QUDATA_AGENT_DEBUG") == "true" {
			utils.LogInfo("DEBUG MODE: using mock GPU data")
		}
		utils.LogInfo("creating host: %s (CUDA %.1f)", hostRequest.GPUName, hostRequest.MaxCUDA)
		runtime.Client.CreateHost(hostRequest)
	}
}
