package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// initSSH installs and starts the SSH server inside the container.
func (m *Manager) initSSH() {
	m.mu.Lock()
	cid := m.containerID
	m.mu.Unlock()

	if cid == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	commands := [][]string{
		{"apt-get", "update"},
		{"apt-get", "install", "-y", "openssh-server"},
		{"mkdir", "-p", "/var/run/sshd"},
		{"mkdir", "-p", "/root/.ssh"},
		{"chmod", "700", "/root/.ssh"},
		{"sh", "-c", `sed -i 's/#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config`},
		{"sh", "-c", `sed -i 's/#PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config`},
		{"sh", "-c", `echo "PubkeyAuthentication yes" >> /etc/ssh/sshd_config`},
		{"/usr/sbin/sshd"},
	}

	for _, cmdArgs := range commands {
		dockerArgs := append([]string{"exec", cid}, cmdArgs...)
		cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
		if out, err := cmd.CombinedOutput(); err != nil {
			m.logger.Warn("ssh setup step failed",
				"cmd", strings.Join(cmdArgs, " "),
				"err", err,
				"output", string(out),
			)
			// Continue — some commands may fail on non-Debian images
		}
	}

	m.logger.Info("SSH server initialized", "container", cid[:min(12, len(cid))])
}

// addSSHKey appends a public key to the container's authorized_keys.
func addSSHKey(ctx context.Context, containerID, pubkey string) error {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return fmt.Errorf("empty SSH public key")
	}

	cmd := exec.CommandContext(ctx, "docker", "exec", containerID,
		"sh", "-c", fmt.Sprintf(`mkdir -p /root/.ssh && echo '%s' >> /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys`, pubkey))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("add ssh key: %w: %s", err, string(out))
	}
	return nil
}

// removeSSHKey removes a public key from the container's authorized_keys.
func removeSSHKey(ctx context.Context, containerID, pubkey string) error {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return fmt.Errorf("empty SSH public key")
	}

	// Escape special characters for sed
	escaped := strings.ReplaceAll(pubkey, "/", `\/`)
	cmd := exec.CommandContext(ctx, "docker", "exec", containerID,
		"sh", "-c", fmt.Sprintf(`sed -i '/%s/d' /root/.ssh/authorized_keys`, escaped))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remove ssh key: %w: %s", err, string(out))
	}
	return nil
}
