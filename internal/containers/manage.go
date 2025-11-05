package containers

import (
	"fmt"
	"github.com/magicaleks/qudata-agent-alpha/internal/errors"
	"github.com/magicaleks/qudata-agent-alpha/internal/security"
	"github.com/magicaleks/qudata-agent-alpha/internal/utils"
	"os"
	"os/exec"
	"strings"
)

var (
	currentContainerID string
	allocatedPorts     map[string]string
	detectedRuntime    string
)

type CreateInstance struct {
	Image      string
	CPUs       string
	Memory     string
	VolumeSize int64
	Registry   string
	Login      string
	Password   string
	EnvVars    map[string]string
	Ports      map[string]string
	Command    string
	SSHEnabled bool
}

type InstanceCommand string

const (
	StartCommand  InstanceCommand = "start"
	StopCommand   InstanceCommand = "stop"
	RebootCommand InstanceCommand = "reboot"
)

func init() {
	detectedRuntime = detectRuntime()
	utils.LogInfo(fmt.Sprintf("Detected container runtime: %s", detectedRuntime))
}

func hasGPU() bool {
	if _, err := os.Stat("/dev/nvidiactl"); err == nil {
		if _, err := os.Stat("/dev/nvidia0"); err == nil {
			return true
		}
	}
	return false
}

func detectRuntime() string {
	if _, err := os.Stat("/dev/kvm"); err == nil {
		if exec.Command("kata-runtime", "--version").Run() == nil {
			return "kata"
		}
	}

	if exec.Command("runsc", "--version").Run() == nil {
		return "runsc"
	}

	return "runc"
}

func GetRuntime() string {
	return detectedRuntime
}

func usesLUKS() bool {
	return detectedRuntime == "kata"
}

func getContainerCmd() string {
	if detectedRuntime == "kata" {
		return "nerdctl"
	}
	return "docker"
}

func StartInstance(data CreateInstance) error {
	if currentContainerID != "" {
		return errors.InstanceAlreadyRunningError{}
	}

	mountPoint := "/var/lib/qudata/secure"

	if usesLUKS() {
		if security.IsActive() {
			security.DeleteVolume()
		}

		key := security.CreateVolume(security.VolumeConfig{
			MountPoint: mountPoint,
			SizeMB:     data.VolumeSize,
		})
		if key == "" {
			utils.LogError("Failed to create volume with runtime: %s", GetRuntime())
			return errors.LUKSVolumeCreateError{}
		}

		exec.Command("chmod", "755", mountPoint).Run()
	} else {
		os.MkdirAll(mountPoint, 0755)
	}

	image := data.Image

	if data.Registry != "" {
		if data.Login != "" && data.Password != "" {
			containerCmd := getContainerCmd()
			loginCmd := exec.Command(containerCmd, "login", data.Registry, "-u", data.Login, "-p", data.Password)
			if err := loginCmd.Run(); err != nil {
				if usesLUKS() {
					security.DeleteVolume()
				}
				return errors.InstanceStartError{Err: err}
			}
		}
		image = data.Registry + "/" + image
	}

	runtime := detectedRuntime
	containerCmd := getContainerCmd()
	var args []string

	if runtime == "kata" {
		args = []string{"run", "-d", "--runtime=io.containerd.kata.v2"}
	} else {
		args = []string{"run", "-d"}
		if runtime == "runsc" {
			args = append(args, "--runtime="+runtime)
		}
	}

	if hasGPU() {
		if runtime == "kata" {
			args = append(args, "--gpus=all")
		} else if runtime == "runsc" {
			args = append(args, "--gpus=all")
			args = append(args, "-e", "NVIDIA_VISIBLE_DEVICES=all")
			args = append(args, "-e", "NVIDIA_DRIVER_CAPABILITIES=compute,utility")
		}
	}

	if data.CPUs != "" {
		args = append(args, "--cpus="+data.CPUs)
	}
	if data.Memory != "" {
		args = append(args, "--memory="+data.Memory)
	}

	for key, value := range data.EnvVars {
		args = append(args, "-e", key+"="+value)
	}

	for containerPort, hostPort := range data.Ports {
		args = append(args, "-p", hostPort+":"+containerPort)
	}

	args = append(args, "-v", mountPoint+":/data")
	args = append(args, image)

	if data.Command != "" {
		args = append(args, "sh", "-c", data.Command)
	} else {
		args = append(args, "sleep", "infinity")
	}

	cmd := exec.Command(containerCmd, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if usesLUKS() {
			security.DeleteVolume()
		}
		return errors.InstanceStartError{Err: fmt.Errorf("%v: %s", err, string(output))}
	}

	currentContainerID = strings.TrimSpace(string(output))
	allocatedPorts = data.Ports

	if data.SSHEnabled {
		go InitSSH()
	}

	return nil
}

func ManageInstance(cmd InstanceCommand) error {
	if currentContainerID == "" {
		return errors.NoInstanceRunningError{}
	}

	if cmd == StartCommand && usesLUKS() && !security.IsActive() {
		return errors.LUKSVolumeNotActiveError{}
	}

	containerCmd := getContainerCmd()
	var action string
	switch cmd {
	case StartCommand:
		action = "unpause"
	case StopCommand:
		action = "pause"
	case RebootCommand:
		if err := exec.Command(containerCmd, "restart", currentContainerID).Run(); err != nil {
			return errors.InstanceManageError{Err: err}
		}
		return nil
	default:
		return errors.UnknownCommandError{Command: string(cmd)}
	}

	utils.LogWarn("Manage command: %s %s %s", containerCmd, action, currentContainerID)

	if err := exec.Command(containerCmd, action, currentContainerID).Run(); err != nil {
		return errors.InstanceManageError{Err: err}
	}
	return nil
}

func StopInstance() error {
	if currentContainerID == "" {
		return nil
	}

	containerCmd := getContainerCmd()
	exec.Command(containerCmd, "stop", currentContainerID).Run()
	exec.Command(containerCmd, "rm", "-f", currentContainerID).Run()

	if usesLUKS() {
		security.DeleteVolume()
	}

	currentContainerID = ""
	allocatedPorts = nil
	return nil
}
