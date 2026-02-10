package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SSHClient handles SSH communication with a QEMU VM guest.
type SSHClient struct {
	host       string
	port       int
	user       string
	keyPath    string
	timeout    time.Duration
	knownHosts string
}

// NewSSHClient creates an SSH client for connecting to a VM.
func NewSSHClient(host string, port int, keyPath string) *SSHClient {
	return &SSHClient{
		host:       host,
		port:       port,
		user:       "root",
		keyPath:    keyPath,
		timeout:    10 * time.Second,
		knownHosts: "/dev/null",
	}
}

// WaitForBoot polls the SSH port until the VM is ready or timeout is reached.
// It attempts SSH connection every 2 seconds for up to maxWait duration.
func (c *SSHClient) WaitForBoot(ctx context.Context, maxWait time.Duration) error {
	if maxWait == 0 {
		maxWait = 120 * time.Second
	}

	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for VM SSH after %v", maxWait)
			}

			// Try a simple SSH command to check if VM is ready
			if err := c.checkConnection(ctx); err == nil {
				return nil
			}
		}
	}
}

// checkConnection performs a quick SSH connection test.
func (c *SSHClient) checkConnection(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := c.Run(ctx, "true")
	return err
}

// Run executes a command on the VM via SSH and returns the output.
func (c *SSHClient) Run(ctx context.Context, command string) ([]byte, error) {
	args := c.buildArgs(command)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	return cmd.CombinedOutput()
}

// RunWithStdin executes a command on the VM via SSH with stdin input.
func (c *SSHClient) RunWithStdin(ctx context.Context, command string, stdin string) ([]byte, error) {
	args := c.buildArgs(command)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(stdin)
	return cmd.CombinedOutput()
}

// buildArgs constructs SSH command arguments.
func (c *SSHClient) buildArgs(command string) []string {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + c.knownHosts,
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-o", "LogLevel=ERROR",
		"-p", strconv.Itoa(c.port),
	}
	if c.keyPath != "" {
		args = append(args, "-i", c.keyPath)
	}
	args = append(args, fmt.Sprintf("%s@%s", c.user, c.host), command)
	return args
}

// DockerPull pulls a Docker image inside the VM.
func (c *SSHClient) DockerPull(ctx context.Context, image, tag string) error {
	fullImage := image
	if tag != "" {
		fullImage += ":" + tag
	}

	cmd := fmt.Sprintf("docker pull %s", shellQuote(fullImage))
	out, err := c.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("docker pull %s: %w: %s", fullImage, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DockerLogin performs docker login inside the VM.
func (c *SSHClient) DockerLogin(ctx context.Context, registry, username, password string) error {
	// Use stdin for password to avoid exposing it in process list
	cmd := fmt.Sprintf("docker login %s -u %s --password-stdin",
		shellQuote(registry), shellQuote(username))
	out, err := c.RunWithStdin(ctx, cmd, password)
	if err != nil {
		return fmt.Errorf("docker login: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DockerRun starts a Docker container inside the VM with the specified parameters.
func (c *SSHClient) DockerRun(ctx context.Context, opts DockerRunOptions) (string, error) {
	args := []string{"docker", "run", "-d", "--restart=unless-stopped"}

	// GPU support
	if opts.GPUEnabled {
		args = append(args, "--gpus=all")
		args = append(args, "-e", "NVIDIA_VISIBLE_DEVICES=all")
		args = append(args, "-e", "NVIDIA_DRIVER_CAPABILITIES=compute,utility")
	}

	// Resource limits
	if opts.CPUs != "" {
		args = append(args, "--cpus="+opts.CPUs)
	}
	if opts.Memory != "" {
		args = append(args, "--memory="+opts.Memory)
	}

	// Environment variables
	for key, value := range opts.EnvVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Port mappings (bind to 0.0.0.0 so QEMU user-net can forward)
	for containerPort := range opts.Ports {
		args = append(args, "-p", fmt.Sprintf("0.0.0.0:%s:%s", containerPort, containerPort))
	}

	// Data volume
	if opts.DataVolume != "" {
		args = append(args, "-v", opts.DataVolume+":/data")
	}

	// Image
	image := opts.Image
	if opts.Tag != "" {
		image += ":" + opts.Tag
	}
	args = append(args, image)

	// Command
	if opts.Command != "" {
		// Wrap command for proper signal handling
		args = append(args, "sh", "-c", fmt.Sprintf("trap 'exit 0' SIGTERM; %s & wait", opts.Command))
	}

	// Build the full command string with proper escaping
	cmdStr := buildShellCommand(args)
	out, err := c.Run(ctx, cmdStr)
	if err != nil {
		return "", fmt.Errorf("docker run: %w: %s", err, strings.TrimSpace(string(out)))
	}

	containerID := strings.TrimSpace(string(out))
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}
	return containerID, nil
}

// DockerStop stops and removes a container inside the VM.
func (c *SSHClient) DockerStop(ctx context.Context, containerID string) error {
	// Stop with timeout
	cmd := fmt.Sprintf("docker stop -t 30 %s 2>/dev/null; docker rm -f %s 2>/dev/null; true",
		shellQuote(containerID), shellQuote(containerID))
	_, err := c.Run(ctx, cmd)
	return err
}

// GetGPUMetrics retrieves GPU metrics via nvidia-smi.
// Returns: utilization, temperature, memoryUsed, memoryTotal (all as strings).
func (c *SSHClient) GetGPUMetrics(ctx context.Context) (*GPUMetrics, error) {
	cmd := "nvidia-smi --query-gpu=utilization.gpu,temperature.gpu,memory.used,memory.total --format=csv,noheader,nounits"
	out, err := c.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return parseGPUMetrics(string(out))
}

// DockerRunOptions contains all parameters for starting a container.
type DockerRunOptions struct {
	Image      string
	Tag        string
	Command    string
	EnvVars    map[string]string
	Ports      map[string]string // containerPort -> hostPort
	CPUs       string
	Memory     string
	GPUEnabled bool
	DataVolume string
}

// GPUMetrics holds parsed nvidia-smi output.
type GPUMetrics struct {
	Utilization float64 // GPU compute utilization (0-100)
	Temperature int     // Temperature in Celsius
	MemoryUsed  uint64  // Memory used in MiB
	MemoryTotal uint64  // Total memory in MiB
}

// MemoryUtilization returns memory utilization as a percentage.
func (m *GPUMetrics) MemoryUtilization() float64 {
	if m.MemoryTotal == 0 {
		return 0
	}
	return float64(m.MemoryUsed) / float64(m.MemoryTotal) * 100
}

// parseGPUMetrics parses nvidia-smi CSV output.
func parseGPUMetrics(output string) (*GPUMetrics, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, fmt.Errorf("empty nvidia-smi output")
	}

	// Handle multi-GPU case by taking first GPU
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("no data in nvidia-smi output")
	}

	parts := strings.Split(lines[0], ",")
	if len(parts) < 4 {
		return nil, fmt.Errorf("unexpected nvidia-smi format: %q", output)
	}

	metrics := &GPUMetrics{}

	// Parse utilization
	if util, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
		metrics.Utilization = util
	}

	// Parse temperature
	if temp, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
		metrics.Temperature = temp
	}

	// Parse memory used
	if mem, err := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 64); err == nil {
		metrics.MemoryUsed = mem
	}

	// Parse memory total
	if mem, err := strconv.ParseUint(strings.TrimSpace(parts[3]), 10, 64); err == nil {
		metrics.MemoryTotal = mem
	}

	return metrics, nil
}

// shellQuote returns a shell-safe quoted string.
func shellQuote(s string) string {
	// If the string is simple (alphanumeric, dashes, underscores, dots, colons, slashes)
	// we can use it directly
	safe := true
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' ||
			c == '.' || c == ':' || c == '/' || c == '@') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}

	// Use single quotes and escape any single quotes in the string
	escaped := strings.ReplaceAll(s, "'", "'\"'\"'")
	return "'" + escaped + "'"
}

// buildShellCommand joins arguments into a properly escaped shell command.
func buildShellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

// CheckDocker verifies Docker is running inside the VM.
func (c *SSHClient) CheckDocker(ctx context.Context) error {
	out, err := c.Run(ctx, "docker info >/dev/null 2>&1 && echo ok")
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		return fmt.Errorf("docker not available in VM")
	}
	return nil
}

// CheckNVIDIA verifies NVIDIA driver is working inside the VM.
func (c *SSHClient) CheckNVIDIA(ctx context.Context) error {
	out, err := c.Run(ctx, "nvidia-smi >/dev/null 2>&1 && echo ok")
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		return fmt.Errorf("nvidia driver not available in VM")
	}
	return nil
}

// CopyFile copies a local file to the VM via SCP.
func (c *SSHClient) CopyFile(ctx context.Context, localPath, remotePath string) error {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + c.knownHosts,
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-P", strconv.Itoa(c.port),
	}
	if c.keyPath != "" {
		args = append(args, "-i", c.keyPath)
	}
	args = append(args, localPath, fmt.Sprintf("%s@%s:%s", c.user, c.host, remotePath))

	cmd := exec.CommandContext(ctx, "scp", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// WriteFile writes content to a file on the VM.
func (c *SSHClient) WriteFile(ctx context.Context, remotePath, content string, mode os.FileMode) error {
	// Create parent directory
	dir := remotePath[:strings.LastIndex(remotePath, "/")]
	if dir != "" {
		if _, err := c.Run(ctx, fmt.Sprintf("mkdir -p %s", shellQuote(dir))); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
	}

	// Write content via heredoc
	cmd := fmt.Sprintf("cat > %s << 'QUDATA_EOF'\n%s\nQUDATA_EOF", shellQuote(remotePath), content)
	if out, err := c.Run(ctx, cmd); err != nil {
		return fmt.Errorf("write file: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Set permissions
	if mode != 0 {
		cmd = fmt.Sprintf("chmod %o %s", mode, shellQuote(remotePath))
		if out, err := c.Run(ctx, cmd); err != nil {
			return fmt.Errorf("chmod: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}
