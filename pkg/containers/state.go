package containers

import (
	"os/exec"
	"strings"
)

type InstanceStatus string

const (
	PendingStatus   InstanceStatus = "pending"
	RunningStatus   InstanceStatus = "running"
	PausedStatus    InstanceStatus = "paused"
	RebootingStatus InstanceStatus = "rebooting"
	ErrorStatus     InstanceStatus = "error"
	DestroyedStatus InstanceStatus = "destroyed"
)

func GetInstanceStatus() InstanceStatus {
	if currentContainerID == "" {
		return DestroyedStatus
	}

	cmd := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", currentContainerID)
	output, err := cmd.Output()
	if err != nil {
		return ErrorStatus
	}

	status := strings.TrimSpace(string(output))
	switch status {
	case "running":
		return RunningStatus
	case "paused":
		return PausedStatus
	case "restarting":
		return RebootingStatus
	case "exited", "dead":
		return DestroyedStatus
	case "created":
		return PendingStatus
	default:
		return ErrorStatus
	}
}

func InstanceIsRunning() bool {
	return currentContainerID != ""
}
