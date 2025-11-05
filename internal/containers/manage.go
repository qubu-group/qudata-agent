package containers

import (
	"fmt"
	"github.com/magicaleks/qudata-agent-alpha/internal/errors"
	"os"
	"os/exec"
	"strings"
)

var (
	currentContainerID string
	allocatedPorts     map[string]string
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

func hasGPU() bool {
	if _, err := os.Stat("/dev/nvidiactl"); err == nil {
		if _, err := os.Stat("/dev/nvidia0"); err == nil {
			return true
		}
	}
	return false
}

func StartInstance(data CreateInstance) error {
	if currentContainerID != "" {
		return errors.InstanceAlreadyRunningError{}
	}

	mountPoint := "/var/lib/qudata/data"
	os.MkdirAll(mountPoint, 0755)

	image := data.Image

	if data.Registry != "" {
		if data.Login != "" && data.Password != "" {
			loginCmd := exec.Command("docker", "login", data.Registry, "-u", data.Login, "-p", data.Password)
			if err := loginCmd.Run(); err != nil {
				return errors.InstanceStartError{Err: err}
			}
		}
		image = data.Registry + "/" + image
	}

	args := []string{
		"run",
		"-d",
		"-t",
		"--init",
		"--restart=unless-stopped",
		"--gpus=all",
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
		args = append(args, "sh", "-c", "trap 'exit 0' SIGTERM; "+data.Command+" & wait")
	} else {
		args = append(args, "tail", "-f", "/dev/null")
	}

	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
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

	var action string
	switch cmd {
	case StartCommand:
		action = "unpause"
	case StopCommand:
		action = "pause"
	case RebootCommand:
		if err := exec.Command("docker", "restart", currentContainerID).Run(); err != nil {
			return errors.InstanceManageError{Err: err}
		}
		return nil
	default:
		return errors.UnknownCommandError{Command: string(cmd)}
	}

	if err := exec.Command("docker", action, currentContainerID).Run(); err != nil {
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

	currentContainerID = ""
	allocatedPorts = nil
	return nil
}
