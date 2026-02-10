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

type SSHClient struct {
	host       string
	port       int
	user       string
	keyPath    string
	timeout    time.Duration
	knownHosts string
}

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
			if err := c.checkConnection(ctx); err == nil {
				return nil
			}
		}
	}
}

func (c *SSHClient) checkConnection(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.Run(ctx, "true")
	return err
}

func (c *SSHClient) Run(ctx context.Context, command string) ([]byte, error) {
	args := c.buildArgs(command)
	return exec.CommandContext(ctx, "ssh", args...).CombinedOutput()
}

func (c *SSHClient) RunWithStdin(ctx context.Context, command, stdin string) ([]byte, error) {
	args := c.buildArgs(command)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(stdin)
	return cmd.CombinedOutput()
}

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

func (c *SSHClient) CheckNVIDIA(ctx context.Context) error {
	out, err := c.Run(ctx, "nvidia-smi >/dev/null 2>&1 && echo ok")
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		return fmt.Errorf("nvidia driver not available in VM")
	}
	return nil
}

type GPUStaticInfo struct {
	Name          string
	Count         int
	VRAMGiB       float64
	DriverVersion string
}

func (c *SSHClient) GetGPUStaticInfo(ctx context.Context) (*GPUStaticInfo, error) {
	cmd := "nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv,noheader,nounits"
	out, err := c.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseGPUStaticInfo(string(out))
}

func parseGPUStaticInfo(output string) (*GPUStaticInfo, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, fmt.Errorf("empty nvidia-smi output")
	}
	lines := strings.Split(output, "\n")
	info := &GPUStaticInfo{Count: len(lines)}
	parts := strings.SplitN(lines[0], ",", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("unexpected nvidia-smi format: %q", output)
	}
	info.Name = strings.TrimSpace(parts[0])
	if mem, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
		info.VRAMGiB = mem / 1024.0
	}
	info.DriverVersion = strings.TrimSpace(parts[2])
	return info, nil
}

func driverVersionToMaxCUDA(driverVersion string) float64 {
	parts := strings.SplitN(driverVersion, ".", 2)
	if len(parts) == 0 {
		return 0
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	switch {
	case major >= 570:
		return 12.8
	case major >= 560:
		return 12.6
	case major >= 550:
		return 12.4
	case major >= 545:
		return 12.3
	case major >= 535:
		return 12.2
	case major >= 530:
		return 12.1
	case major >= 525:
		return 12.0
	case major >= 520:
		return 11.8
	case major >= 515:
		return 11.7
	case major >= 510:
		return 11.6
	case major >= 470:
		return 11.4
	case major >= 460:
		return 11.2
	case major >= 450:
		return 11.0
	default:
		return 0
	}
}

type GPUMetrics struct {
	Utilization float64
	Temperature int
	MemoryUsed  uint64
	MemoryTotal uint64
}

func (c *SSHClient) GetGPUMetrics(ctx context.Context) (*GPUMetrics, error) {
	cmd := "nvidia-smi --query-gpu=utilization.gpu,temperature.gpu,memory.used,memory.total --format=csv,noheader,nounits"
	out, err := c.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseGPUMetrics(string(out))
}

func parseGPUMetrics(output string) (*GPUMetrics, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, fmt.Errorf("empty nvidia-smi output")
	}
	lines := strings.Split(output, "\n")
	parts := strings.Split(lines[0], ",")
	if len(parts) < 4 {
		return nil, fmt.Errorf("unexpected nvidia-smi format: %q", output)
	}
	metrics := &GPUMetrics{}
	if u, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
		metrics.Utilization = u
	}
	if t, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
		metrics.Temperature = t
	}
	if m, err := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 64); err == nil {
		metrics.MemoryUsed = m
	}
	if m, err := strconv.ParseUint(strings.TrimSpace(parts[3]), 10, 64); err == nil {
		metrics.MemoryTotal = m
	}
	return metrics, nil
}

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
	out, err := exec.CommandContext(ctx, "scp", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *SSHClient) WriteFile(ctx context.Context, remotePath, content string, mode os.FileMode) error {
	dir := remotePath[:strings.LastIndex(remotePath, "/")]
	if dir != "" {
		if _, err := c.Run(ctx, fmt.Sprintf("mkdir -p %s", shellQuote(dir))); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
	}
	cmd := fmt.Sprintf("cat > %s << 'QUDATA_EOF'\n%s\nQUDATA_EOF", shellQuote(remotePath), content)
	if out, err := c.Run(ctx, cmd); err != nil {
		return fmt.Errorf("write file: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if mode != 0 {
		cmd = fmt.Sprintf("chmod %o %s", mode, shellQuote(remotePath))
		if out, err := c.Run(ctx, cmd); err != nil {
			return fmt.Errorf("chmod: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func shellQuote(s string) string {
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
	escaped := strings.ReplaceAll(s, "'", "'\"'\"'")
	return "'" + escaped + "'"
}
