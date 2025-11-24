package docker

import (
	"os/exec"
	"time"

	domainerrors "github.com/magicaleks/qudata-agent-alpha/internal/domain/errors"
)

func InitSSH() error {
	if currentContainerID == "" {
		return domainerrors.NoInstanceRunningError{}
	}

	time.Sleep(2 * time.Second)

	checkCmd := exec.Command("docker", "exec", currentContainerID, "pgrep", "sshd")
	if err := checkCmd.Run(); err == nil {
		return nil
	}

	commands := [][]string{
		{"apt-get", "update"},
		{"apt-get", "install", "-y", "openssh-server"},
		{"mkdir", "-p", "/var/run/sshd"},
		{"sed", "-i", "s/#PermitRootLogin prohibit-password/PermitRootLogin yes/", "/etc/ssh/sshd_config"},
		{"/usr/sbin/sshd"},
	}

	for _, cmdArgs := range commands {
		args := append([]string{"exec", currentContainerID}, cmdArgs...)
		if err := exec.Command("docker", args...).Run(); err != nil {
			return domainerrors.SSHInitError{Err: err}
		}
	}

	return nil
}

func AddSSH(key string) error {
	if currentContainerID == "" {
		return domainerrors.NoInstanceRunningError{}
	}

	commands := [][]string{
		{"mkdir", "-p", "/root/.ssh"},
		{"sh", "-c", "echo '" + key + "' >> /root/.ssh/authorized_keys"},
		{"chmod", "600", "/root/.ssh/authorized_keys"},
		{"chmod", "700", "/root/.ssh"},
	}

	for _, cmdArgs := range commands {
		args := append([]string{"exec", currentContainerID}, cmdArgs...)
		if err := exec.Command("docker", args...).Run(); err != nil {
			return domainerrors.SSHKeyAddError{Err: err}
		}
	}

	return nil
}

func RemoveSSH(key string) error {
	if currentContainerID == "" {
		return domainerrors.NoInstanceRunningError{}
	}

	args := []string{"exec", currentContainerID, "sed", "-i", "/" + key + "/d", "/root/.ssh/authorized_keys"}
	if err := exec.Command("docker", args...).Run(); err != nil {
		return domainerrors.SSHKeyRemoveError{Err: err}
	}

	return nil
}
