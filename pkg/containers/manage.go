package containers

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/magicaleks/qudata-agent-alpha/pkg/errors"
	"github.com/magicaleks/qudata-agent-alpha/pkg/security"
)

var (
	currentContainerID string
	allocatedPorts     map[string]string
)

type CreateInstance struct {
	Image      string
	CPUs       string
	Memory     string
	VolumeSize string
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

func StartInstance(data CreateInstance) error {
	if currentContainerID != "" {
		return errors.InstanceAlreadyRunningError{}
	}

	mountPoint := "/var/lib/qudata/secure"
	sizeMB := int64(10240)
	if data.VolumeSize != "" {
		if s, err := strconv.ParseInt(data.VolumeSize, 10, 64); err == nil {
			sizeMB = s
		}
	}

	key := security.CreateVolume(security.VolumeConfig{
		MountPoint: mountPoint,
		SizeMB:     sizeMB,
	})
	if key == "" {
		return errors.LUKSVolumeCreateError{}
	}

	exec.Command("chmod", "755", mountPoint).Run()

	image := data.Image

	if data.Registry != "" {
		if data.Login != "" && data.Password != "" {
			loginCmd := exec.Command("docker", "login", data.Registry, "-u", data.Login, "-p", data.Password)
			if err := loginCmd.Run(); err != nil {
				security.DeleteVolume()
				return errors.InstanceStartError{Err: err}
			}
		}
		image = data.Registry + "/" + image
	}

	args := []string{"run", "-d", "--runtime=kata-runtime"}

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

	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		security.DeleteVolume()
		return errors.InstanceStartError{Err: err}
	}

	currentContainerID = strings.TrimSpace(string(output))
	allocatedPorts = data.Ports

	if data.SSHEnabled {
		go InitSSH()
	}

	return nil
}

func GetAllocatedPorts() map[string]string {
	return allocatedPorts
}

func ManageInstance(cmd InstanceCommand) error {
	if currentContainerID == "" {
		return errors.NoInstanceRunningError{}
	}

	if cmd == StartCommand && !security.IsActive() {
		return errors.LUKSVolumeNotActiveError{}
	}

	var dockerCmd string
	switch cmd {
	case StartCommand:
		dockerCmd = "start"
	case StopCommand:
		dockerCmd = "stop"
	case RebootCommand:
		if err := exec.Command("docker", "restart", currentContainerID).Run(); err != nil {
			return errors.InstanceManageError{Err: err}
		}
		return nil
	default:
		return errors.UnknownCommandError{Command: string(cmd)}
	}

	if err := exec.Command("docker", dockerCmd, currentContainerID).Run(); err != nil {
		return errors.InstanceManageError{Err: err}
	}
	return nil
}

func StopInstance() error {
	if currentContainerID == "" {
		return nil
	}

	exec.Command("docker", "stop", currentContainerID).Run()
	exec.Command("docker", "rm", "-f", currentContainerID).Run()

	security.DeleteVolume()

	currentContainerID = ""
	allocatedPorts = nil
	return nil
}
