package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
	domainerrors "github.com/magicaleks/qudata-agent-alpha/internal/domain/errors"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/state"
)

var (
	currentContainerID string
	allocatedPorts     domain.InstancePorts
	sshEnabled         bool
	isPulling          bool
	currentImage       string
)

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Create(_ context.Context, spec domain.InstanceSpec) (string, error) {
	if currentContainerID != "" || isPulling {
		return "", domainerrors.InstanceAlreadyRunningError{}
	}

	return startInstance(spec)
}

func (m *Manager) Manage(_ context.Context, cmd domain.InstanceCommand) error {
	if currentContainerID == "" {
		return domainerrors.NoInstanceRunningError{}
	}

	switch cmd {
	case domain.CommandStart:
		if err := exec.Command("docker", "unpause", currentContainerID).Run(); err != nil {
			return domainerrors.InstanceManageError{Err: err}
		}
		return nil
	case domain.CommandStop:
		if err := exec.Command("docker", "pause", currentContainerID).Run(); err != nil {
			return domainerrors.InstanceManageError{Err: err}
		}
		return nil
	case domain.CommandReboot:
		if err := exec.Command("docker", "restart", currentContainerID).Run(); err != nil {
			return domainerrors.InstanceManageError{Err: err}
		}
		if sshEnabled {
			go InitSSH()
		}
		return nil
	default:
		return domainerrors.UnknownCommandError{Command: string(cmd)}
	}
}

func (m *Manager) Stop(_ context.Context) error {
	isPulling = false

	if currentContainerID != "" {
		_ = exec.Command("docker", "stop", currentContainerID).Run()
		_ = exec.Command("docker", "rm", "-f", currentContainerID).Run()
	}

	if currentImage != "" {
		_ = exec.Command("docker", "rmi", "-f", currentImage).Run()
	}

	currentContainerID = ""
	currentImage = ""
	allocatedPorts = nil
	sshEnabled = false
	return nil
}

func (m *Manager) Cleanup(_ context.Context) error {
	cleanupDocker()
	return nil
}

func (m *Manager) Status(_ context.Context) domain.InstanceStatus {
	return GetInstanceStatus()
}

func (m *Manager) AddSSH(_ context.Context, key string) error {
	return AddSSH(key)
}

func (m *Manager) RemoveSSH(_ context.Context, key string) error {
	return RemoveSSH(key)
}

func (m *Manager) IsRunning(_ context.Context) bool {
	return InstanceIsRunning()
}

// RestoreState синхронизирует менеджер с сохранённым состоянием.
func (m *Manager) RestoreState(saved *state.InstanceState) {
	if saved == nil {
		return
	}
	currentContainerID = saved.ContainerID
	allocatedPorts = saved.Ports
}

func startInstance(spec domain.InstanceSpec) (string, error) {
	isPulling = true
	defer func() { isPulling = false }()

	image := spec.Image
	if spec.Registry != "" {
		if spec.Login != "" && spec.Password != "" {
			loginCmd := exec.Command("docker", "login", spec.Registry, "-u", spec.Login, "-p", spec.Password)
			_ = loginCmd.Run()
		}
		image = spec.Registry + "/" + image
	}

	currentImage = image

	pullCmd := exec.Command("docker", "pull", image)
	if err := pullCmd.Run(); err != nil {
		currentImage = ""
		return "", err
	}

	mountPoint := "/var/lib/qudata/data"
	_ = os.MkdirAll(mountPoint, 0o755)

	args := []string{"run", "-d", "-t", "--init", "--restart=unless-stopped"}

	if hasGPU() {
		args = append(args, "--gpus=all")
		args = append(args, "-e", "NVIDIA_VISIBLE_DEVICES=all")
		args = append(args, "-e", "NVIDIA_DRIVER_CAPABILITIES=compute,utility")
	}

	if spec.CPUs != "" {
		args = append(args, "--cpus="+spec.CPUs)
	}
	if spec.Memory != "" {
		args = append(args, "--memory="+spec.Memory)
	}

	for key, value := range spec.EnvVars {
		args = append(args, "-e", key+"="+value)
	}

	for containerPort, hostPort := range spec.Ports {
		clean := strings.TrimSuffix(containerPort, "/tcp")
		if clean == "22" {
			args = append(args, "-p", hostPort+":"+clean)
		}
	}

	args = append(args, "-v", mountPoint+":/data", image)

	if spec.Command != "" {
		args = append(args, "sh", "-c", "trap 'exit 0' SIGTERM; "+spec.Command+" & wait")
	} else {
		args = append(args, "tail", "-f", "/dev/null")
	}

	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		currentImage = ""
		msg := strings.TrimSpace(string(output))
		if msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}

	currentContainerID = strings.TrimSpace(string(output))
	allocatedPorts = spec.Ports
	sshEnabled = spec.SSHEnabled

	if spec.SSHEnabled {
		go InitSSH()
	}
	return currentContainerID, nil
}

func cleanupDocker() {
	cmd := exec.Command("docker", "ps", "-aq")
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		containerIDs := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, id := range containerIDs {
			if id != "" {
				_ = exec.Command("docker", "rm", "-f", id).Run()
			}
		}
	}

	cmd = exec.Command("docker", "images", "-q")
	output, err = cmd.Output()
	if err == nil && len(output) > 0 {
		imageIDs := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, imageID := range imageIDs {
			if imageID != "" {
				_ = exec.Command("docker", "rmi", "-f", imageID).Run()
			}
		}
	}

	currentContainerID = ""
	allocatedPorts = nil
	sshEnabled = false
	isPulling = false
	currentImage = ""
}

func hasGPU() bool {
	if _, err := os.Stat("/dev/nvidiactl"); err == nil {
		if _, err := os.Stat("/dev/nvidia0"); err == nil {
			return true
		}
	}
	return false
}
