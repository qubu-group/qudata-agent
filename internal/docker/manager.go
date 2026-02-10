package docker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/qudata/agent/internal/domain"
)

// Manager handles Docker container lifecycle for VM instances.
type Manager struct {
	logger *slog.Logger
	mu     sync.Mutex

	containerID string
	image       string
	ports       domain.InstancePorts
	sshEnabled  bool
	isPulling   bool
}

// NewManager creates a Docker manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{logger: logger}
}

// Create pulls the image and starts a container with the given spec.
// Returns allocated host ports for each container port.
func (m *Manager) Create(ctx context.Context, spec domain.InstanceSpec, hostPorts []int) (domain.InstancePorts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.containerID != "" || m.isPulling {
		return nil, domain.ErrInstanceAlreadyRunning{}
	}

	m.isPulling = true
	defer func() { m.isPulling = false }()

	image := spec.Image
	if spec.ImageTag != "" {
		image += ":" + spec.ImageTag
	}

	// Docker registry login if needed
	if spec.Registry != "" {
		fullImage := spec.Registry + "/" + image
		if spec.Login != "" && spec.Password != "" {
			loginCmd := exec.CommandContext(ctx, "docker", "login", spec.Registry,
				"-u", spec.Login, "-p", spec.Password)
			if out, err := loginCmd.CombinedOutput(); err != nil {
				m.logger.Error("docker login failed", "err", err, "output", string(out))
			}
		}
		image = fullImage
	}

	// Pull image
	m.logger.Info("pulling image", "image", image)
	pullCmd := exec.CommandContext(ctx, "docker", "pull", image)
	if out, err := pullCmd.CombinedOutput(); err != nil {
		return nil, domain.ErrImagePull{Image: image, Err: fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))}
	}

	// Build port mapping
	portMap := make(domain.InstancePorts)
	for i, pm := range spec.Ports {
		if i < len(hostPorts) {
			portMap[strconv.Itoa(pm.ContainerPort)] = strconv.Itoa(hostPorts[i])
		}
	}

	// Build docker run command
	args := []string{"run", "-d", "-t", "--init", "--restart=unless-stopped"}

	if hasGPU() {
		args = append(args,
			"--gpus=all",
			"-e", "NVIDIA_VISIBLE_DEVICES=all",
			"-e", "NVIDIA_DRIVER_CAPABILITIES=compute,utility",
		)
	}

	if spec.CPUs != "" {
		args = append(args, "--cpus="+spec.CPUs)
	}
	if spec.Memory != "" {
		args = append(args, "--memory="+spec.Memory)
	}

	// Environment variables
	for key, value := range spec.EnvVars {
		args = append(args, "-e", key+"="+value)
	}

	// Port mappings: bind container ports to allocated host ports
	for containerPort, hostPort := range portMap {
		args = append(args, "-p", "127.0.0.1:"+hostPort+":"+containerPort)
	}

	// Data volume
	mountPoint := "/var/lib/qudata/data"
	_ = os.MkdirAll(mountPoint, 0o755)
	args = append(args, "-v", mountPoint+":/data")

	// Image
	args = append(args, image)

	// Command
	if spec.Command != "" {
		args = append(args, "sh", "-c", "trap 'exit 0' SIGTERM; "+spec.Command+" & wait")
	} else {
		args = append(args, "tail", "-f", "/dev/null")
	}

	m.logger.Info("starting container", "args", args)
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker run: %w: %s", err, strings.TrimSpace(string(output)))
	}

	m.containerID = strings.TrimSpace(string(output))
	m.image = image
	m.ports = portMap
	m.sshEnabled = spec.SSHEnabled

	// Set up SSH if requested
	if spec.SSHEnabled {
		go m.initSSH()
	}

	m.logger.Info("container started",
		"container_id", m.containerID[:12],
		"ports", portMap,
	)

	return portMap, nil
}

// Manage executes a lifecycle command on the running container.
func (m *Manager) Manage(ctx context.Context, cmd domain.InstanceCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.containerID == "" {
		return domain.ErrNoInstanceRunning{}
	}

	switch cmd {
	case domain.CommandStart:
		return m.dockerExec(ctx, "unpause", m.containerID)
	case domain.CommandStop:
		return m.dockerExec(ctx, "pause", m.containerID)
	case domain.CommandReboot:
		if err := m.dockerExec(ctx, "restart", m.containerID); err != nil {
			return err
		}
		if m.sshEnabled {
			go m.initSSH()
		}
		return nil
	default:
		return domain.ErrUnknownCommand{Command: string(cmd)}
	}
}

// Stop stops and removes the current container, cleaning up the image.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.isPulling = false

	if m.containerID != "" {
		_ = exec.CommandContext(ctx, "docker", "stop", m.containerID).Run()
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", m.containerID).Run()
	}
	if m.image != "" {
		_ = exec.CommandContext(ctx, "docker", "rmi", "-f", m.image).Run()
	}

	m.containerID = ""
	m.image = ""
	m.ports = nil
	m.sshEnabled = false

	return nil
}

// Status returns the current instance status by inspecting Docker.
func (m *Manager) Status(ctx context.Context) domain.InstanceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isPulling {
		return domain.StatusPending
	}
	if m.containerID == "" {
		return domain.StatusDestroyed
	}

	out, err := exec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{.State.Status}}", m.containerID).Output()
	if err != nil {
		return domain.StatusError
	}

	switch strings.TrimSpace(string(out)) {
	case "running":
		return domain.StatusRunning
	case "paused":
		return domain.StatusPaused
	case "restarting":
		return domain.StatusRebooting
	case "exited", "dead":
		return domain.StatusError
	default:
		return domain.StatusError
	}
}

// IsRunning returns true if a container is active.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.containerID != ""
}

// VMID returns the current container ID.
func (m *Manager) VMID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.containerID
}

// Ports returns the current port mappings.
func (m *Manager) Ports() domain.InstancePorts {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ports
}

// RestoreState synchronizes the manager with a persisted InstanceState.
func (m *Manager) RestoreState(state *domain.InstanceState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if state == nil {
		m.containerID = ""
		m.ports = nil
		m.sshEnabled = false
		return
	}

	m.containerID = state.ContainerID
	m.image = state.Image
	m.ports = state.Ports
	m.sshEnabled = state.SSHEnabled
}

// AddSSHKey adds an SSH public key to the running container.
func (m *Manager) AddSSHKey(ctx context.Context, pubkey string) error {
	m.mu.Lock()
	cid := m.containerID
	m.mu.Unlock()

	if cid == "" {
		return domain.ErrNoInstanceRunning{}
	}
	return addSSHKey(ctx, cid, pubkey)
}

// RemoveSSHKey removes an SSH public key from the running container.
func (m *Manager) RemoveSSHKey(ctx context.Context, pubkey string) error {
	m.mu.Lock()
	cid := m.containerID
	m.mu.Unlock()

	if cid == "" {
		return domain.ErrNoInstanceRunning{}
	}
	return removeSSHKey(ctx, cid, pubkey)
}

// --- internal helpers ---

func (m *Manager) dockerExec(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("docker command failed",
			"args", args,
			"err", err,
			"output", string(out),
		)
		return domain.ErrInstanceManage{Err: err}
	}
	return nil
}

func hasGPU() bool {
	if _, err := os.Stat("/dev/nvidiactl"); err != nil {
		return false
	}
	_, err := os.Stat("/dev/nvidia0")
	return err == nil
}
