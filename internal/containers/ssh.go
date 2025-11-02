package containers

import (
	"github.com/magicaleks/qudata-agent-alpha/internal/errors"
	"os/exec"
)

func InitSSH() error {
	if currentContainerID == "" {
		return errors.NoInstanceRunningError{}
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
			return errors.SSHInitError{Err: err}
		}
	}

	return nil
}

func AddSSH(key string) error {
	if currentContainerID == "" {
		return errors.NoInstanceRunningError{}
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
			return errors.SSHKeyAddError{Err: err}
		}
	}

	return nil
}

func RemoveSSH(key string) error {
	if currentContainerID == "" {
		return errors.NoInstanceRunningError{}
	}

	args := []string{"exec", currentContainerID, "sed", "-i", "/" + key + "/d", "/root/.ssh/authorized_keys"}
	if err := exec.Command("docker", args...).Run(); err != nil {
		return errors.SSHKeyRemoveError{Err: err}
	}

	return nil
}
