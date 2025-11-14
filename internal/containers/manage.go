package containers

import (
	"github.com/magicaleks/qudata-agent-alpha/internal/errors"
	"os"
	"os/exec"
	"strings"
)

var (
	currentContainerID string
	allocatedPorts     map[string]string
	sshEnabled         bool
	isPulling          bool
	currentImage       string
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

func CleanupDocker() {
	// Останавливаем и удаляем все контейнеры
	cmd := exec.Command("docker", "ps", "-aq")
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		containerIDs := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, containerID := range containerIDs {
			if containerID != "" {
				exec.Command("docker", "rm", "-f", containerID).Run()
			}
		}
	}

	// Удаляем все образы
	cmd = exec.Command("docker", "images", "-q")
	output, err = cmd.Output()
	if err == nil && len(output) > 0 {
		imageIDs := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, imageID := range imageIDs {
			if imageID != "" {
				exec.Command("docker", "rmi", "-f", imageID).Run()
			}
		}
	}

	// Очищаем глобальные переменные
	currentContainerID = ""
	allocatedPorts = nil
	sshEnabled = false
	isPulling = false
	currentImage = ""
}

func StartInstance(data CreateInstance) error {
	if currentContainerID != "" || isPulling {
		return errors.InstanceAlreadyRunningError{}
	}

	go startInstanceAsync(data)
	return nil
}

func startInstanceAsync(data CreateInstance) {
	isPulling = true

	image := data.Image
	if data.Registry != "" {
		if data.Login != "" && data.Password != "" {
			loginCmd := exec.Command("docker", "login", data.Registry, "-u", data.Login, "-p", data.Password)
			loginCmd.Run()
		}
		image = data.Registry + "/" + image
	}

	currentImage = image

	pullCmd := exec.Command("docker", "pull", image)
	if err := pullCmd.Run(); err != nil {
		isPulling = false
		currentImage = ""
		return
	}

	isPulling = false

	mountPoint := "/var/lib/qudata/data"
	os.MkdirAll(mountPoint, 0755)

	args := []string{
		"run",
		"-d",
		"-t",
		"--init",
		"--restart=unless-stopped",
	}

	if hasGPU() {
		args = append(args, "--gpus=all")
		args = append(args, "-e", "NVIDIA_VISIBLE_DEVICES=all")
		args = append(args, "-e", "NVIDIA_DRIVER_CAPABILITIES=compute,utility")
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
		currentImage = ""
		return
	}

	currentContainerID = strings.TrimSpace(string(output))
	allocatedPorts = data.Ports
	sshEnabled = data.SSHEnabled

	if data.SSHEnabled {
		go InitSSH()
	}
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
		if sshEnabled {
			go InitSSH()
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
	isPulling = false

	if currentContainerID != "" {
		exec.Command("docker", "stop", currentContainerID).Run()
		exec.Command("docker", "rm", "-f", currentContainerID).Run()
	}

	if currentImage != "" {
		exec.Command("docker", "rmi", "-f", currentImage).Run()
	}

	currentContainerID = ""
	currentImage = ""
	allocatedPorts = nil
	sshEnabled = false
	return nil
}
