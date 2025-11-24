package docker

import (
	"os/exec"
	"strings"

	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/logger"
)

func GetInstanceStatus() domain.InstanceStatus {
	if isPulling {
		return domain.InstancePending
	}

	if currentContainerID == "" {
		return domain.InstanceDestroyed
	}

	cmd := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", currentContainerID)
	output, err := cmd.Output()
	if err != nil {
		logger.LogWarn("Failed to get container status for %s", currentContainerID)
		return domain.InstanceError
	}

	switch strings.TrimSpace(string(output)) {
	case "running":
		return domain.InstanceRunning
	case "paused":
		return domain.InstancePaused
	case "restarting":
		return domain.InstanceRebooting
	case "exited", "dead":
		if currentContainerID == "" {
			return domain.InstanceDestroyed
		}
		return domain.InstancePaused
	case "created":
		return domain.InstancePending
	default:
		return domain.InstanceError
	}
}

func InstanceIsRunning() bool {
	return currentContainerID != "" || isPulling
}
