package qemu

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/qudata/agent/internal/domain"
)

type Config struct {
	QEMUBinary     string
	OVMFPath       string
	BaseImagePath  string
	ImageDir       string
	RunDir         string
	DefaultGPUAddr string
	SSHKeyPath     string
}

type Manager struct {
	logger     *slog.Logger
	qemuBin    string
	ovmfPath   string
	baseImage  string
	defaultGPU string
	runDir     string
	sshKeyPath string
	images     *ImageManager

	mu         sync.Mutex
	creating   bool
	vmID       string
	cmd        *exec.Cmd
	logFile    *os.File
	vfio       *VFIO
	qmp        *QMPClient
	sshClient  *SSHClient
	ports      domain.InstancePorts
	diskPath   string
	qmpSocket  string
	gpuAddr    string
	sshEnabled bool
	done       chan struct{}

	containerID string
	spec        *domain.InstanceSpec
}

func NewManager(cfg Config, logger *slog.Logger) *Manager {
	return &Manager{
		logger:     logger,
		qemuBin:    cfg.QEMUBinary,
		ovmfPath:   cfg.OVMFPath,
		baseImage:  cfg.BaseImagePath,
		defaultGPU: cfg.DefaultGPUAddr,
		runDir:     cfg.RunDir,
		sshKeyPath: cfg.SSHKeyPath,
		images:     NewImageManager(cfg.ImageDir),
	}
}

func (m *Manager) Create(ctx context.Context, spec domain.InstanceSpec, hostPorts []int) (domain.InstancePorts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.vmID != "" || m.creating {
		return nil, domain.ErrInstanceAlreadyRunning{}
	}
	m.creating = true
	defer func() { m.creating = false }()

	gpuAddr := spec.GPUAddr
	if gpuAddr == "" {
		gpuAddr = m.defaultGPU
	}
	if gpuAddr == "" {
		return nil, domain.ErrQEMU{Op: "create", Err: fmt.Errorf("no GPU PCI address configured")}
	}

	vfio := NewVFIO(gpuAddr)
	if err := vfio.Bind(); err != nil {
		return nil, domain.ErrVFIO{Op: "bind", Addr: gpuAddr, Err: err}
	}

	vmID := "vm-" + uuid.New().String()[:8]

	diskPath, err := m.prepareDisk(vmID, spec.DiskSizeGB)
	if err != nil {
		_ = vfio.Unbind()
		return nil, domain.ErrQEMU{Op: "disk", Err: err}
	}

	portMap := make(domain.InstancePorts)
	netCfg := NewNetworkConfig("net0")
	for i, pm := range spec.Ports {
		if i < len(hostPorts) {
			portMap[strconv.Itoa(pm.ContainerPort)] = strconv.Itoa(hostPorts[i])
			netCfg.AddForward("tcp", hostPorts[i], pm.ContainerPort)
		}
	}

	qmpSocket := filepath.Join(m.runDir, vmID+".qmp")
	args := m.buildArgs(spec, diskPath, gpuAddr, qmpSocket, netCfg)

	if err := os.MkdirAll(m.runDir, 0o755); err != nil {
		_ = m.images.RemoveDisk(diskPath)
		_ = vfio.Unbind()
		return nil, domain.ErrQEMU{Op: "rundir", Err: err}
	}

	logFile, _ := os.Create(filepath.Join(m.runDir, vmID+".log"))

	m.logger.Info("starting QEMU VM", "vm_id", vmID, "gpu", gpuAddr, "disk", diskPath)

	cmd := exec.Command(m.qemuBin, args...)
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		_ = m.images.RemoveDisk(diskPath)
		_ = vfio.Unbind()
		return nil, domain.ErrQEMU{Op: "start", Err: err}
	}

	m.vmID = vmID
	m.cmd = cmd
	m.logFile = logFile
	m.vfio = vfio
	m.ports = portMap
	m.diskPath = diskPath
	m.qmpSocket = qmpSocket
	m.gpuAddr = gpuAddr
	m.sshEnabled = spec.SSHEnabled
	m.spec = &spec

	m.done = make(chan struct{})
	go func() {
		_ = cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}
		close(m.done)
	}()

	qmpClient := NewQMPClient(qmpSocket)
	if err := m.waitForQMP(qmpClient, 30*time.Second); err != nil {
		m.logger.Warn("QMP connect failed; VM may still be booting", "err", err)
	} else {
		m.qmp = qmpClient
	}

	m.logger.Info("QEMU VM started", "vm_id", vmID, "pid", cmd.Process.Pid, "ports", portMap)

	go m.setupDockerInVM(context.Background(), spec)

	return portMap, nil
}

func (m *Manager) setupDockerInVM(ctx context.Context, spec domain.InstanceSpec) {
	sshPort := m.sshPort()
	if sshPort == 0 {
		m.logger.Warn("no SSH port configured, skipping Docker setup")
		return
	}

	sshClient := NewSSHClient("127.0.0.1", sshPort, m.sshKeyPath)

	m.logger.Info("waiting for VM SSH", "port", sshPort)
	if err := sshClient.WaitForBoot(ctx, 180*time.Second); err != nil {
		m.logger.Error("VM SSH timeout", "err", err)
		return
	}

	m.mu.Lock()
	m.sshClient = sshClient
	m.mu.Unlock()

	m.logger.Info("VM SSH ready")

	image := spec.Image
	if spec.Registry != "" {
		image = spec.Registry + "/" + image
	}

	if spec.Registry != "" && spec.Login != "" && spec.Password != "" {
		if err := sshClient.DockerLogin(ctx, spec.Registry, spec.Login, spec.Password); err != nil {
			m.logger.Warn("docker login failed", "err", err)
		}
	}

	m.logger.Info("pulling image", "image", image, "tag", spec.ImageTag)
	if err := sshClient.DockerPull(ctx, image, spec.ImageTag); err != nil {
		m.logger.Error("docker pull failed", "err", err)
		return
	}

	ports := make(map[string]string)
	for _, pm := range spec.Ports {
		p := strconv.Itoa(pm.ContainerPort)
		ports[p] = p
	}

	opts := DockerRunOptions{
		Image:      image,
		Tag:        spec.ImageTag,
		Command:    spec.Command,
		EnvVars:    spec.EnvVars,
		Ports:      ports,
		CPUs:       spec.CPUs,
		Memory:     spec.Memory,
		GPUEnabled: true,
		DataVolume: "/var/lib/qudata/data",
	}

	containerID, err := sshClient.DockerRun(ctx, opts)
	if err != nil {
		m.logger.Error("docker run failed", "err", err)
		return
	}

	m.mu.Lock()
	m.containerID = containerID
	m.mu.Unlock()

	m.logger.Info("container started in VM", "container_id", containerID)
}

func (m *Manager) Manage(ctx context.Context, cmd domain.InstanceCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.vmID == "" {
		return domain.ErrNoInstanceRunning{}
	}

	if m.qmp == nil || !m.qmp.Connected() {
		return domain.ErrQEMU{Op: "manage", Err: fmt.Errorf("QMP not connected")}
	}

	switch cmd {
	case domain.CommandStart:
		return m.qmp.Resume()
	case domain.CommandStop:
		return m.qmp.Pause()
	case domain.CommandReboot:
		return m.qmp.Reset()
	default:
		return domain.ErrUnknownCommand{Command: string(cmd)}
	}
}

func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.vmID == "" {
		return nil
	}

	if m.containerID != "" && m.sshClient != nil {
		_ = m.sshClient.DockerStop(ctx, m.containerID)
	}

	if m.qmp != nil && m.qmp.Connected() {
		if err := m.qmp.Shutdown(); err != nil {
			m.logger.Warn("QMP shutdown failed, will force-kill", "err", err)
		}
	}

	if m.done != nil {
		select {
		case <-m.done:
			m.logger.Info("QEMU exited gracefully")
		case <-time.After(30 * time.Second):
			m.logger.Warn("QEMU did not exit in time, killing")
			m.forceKill()
		}
	}

	m.cleanup()
	return nil
}

func (m *Manager) Status(ctx context.Context) domain.InstanceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.creating {
		return domain.StatusPending
	}
	if m.vmID == "" {
		return domain.StatusDestroyed
	}

	if m.done != nil {
		select {
		case <-m.done:
			return domain.StatusError
		default:
		}
	}

	if m.qmp != nil && m.qmp.Connected() {
		status, _, err := m.qmp.QueryStatus()
		if err == nil {
			return mapQMPStatus(status)
		}
	}

	return domain.StatusRunning
}

func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vmID != ""
}

func (m *Manager) VMID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vmID
}

func (m *Manager) Ports() domain.InstancePorts {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ports
}

func (m *Manager) RestoreState(state *domain.InstanceState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if state == nil {
		m.reset()
		return
	}

	m.vmID = state.ContainerID
	m.ports = state.Ports
	m.sshEnabled = state.SSHEnabled
	m.gpuAddr = state.GPUAddr

	m.qmpSocket = filepath.Join(m.runDir, m.vmID+".qmp")
	m.diskPath = m.images.DiskPath(m.vmID)

	if m.gpuAddr != "" {
		m.vfio = NewVFIO(m.gpuAddr)
		m.vfio.RestoreBinding()
	}

	qmp := NewQMPClient(m.qmpSocket)
	if err := qmp.Connect(); err != nil {
		m.logger.Warn("QMP reconnect failed during restore", "err", err)
	} else {
		m.qmp = qmp
		m.logger.Info("QMP reconnected after restore", "vm_id", m.vmID)
	}

	sshPort := 0
	if p, ok := m.ports["22"]; ok {
		sshPort, _ = strconv.Atoi(p)
	}
	if sshPort > 0 {
		m.sshClient = NewSSHClient("127.0.0.1", sshPort, m.sshKeyPath)
	}
}

func (m *Manager) AddSSHKey(ctx context.Context, pubkey string) error {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return fmt.Errorf("empty SSH public key")
	}

	cmd := fmt.Sprintf(
		`mkdir -p /root/.ssh && echo '%s' >> /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys`,
		pubkey,
	)
	if out, err := m.sshExec(ctx, cmd); err != nil {
		return fmt.Errorf("add ssh key: %w: %s", err, string(out))
	}
	return nil
}

func (m *Manager) RemoveSSHKey(ctx context.Context, pubkey string) error {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return fmt.Errorf("empty SSH public key")
	}

	escaped := strings.ReplaceAll(pubkey, "/", `\/`)
	cmd := fmt.Sprintf(`sed -i '/%s/d' /root/.ssh/authorized_keys`, escaped)
	if out, err := m.sshExec(ctx, cmd); err != nil {
		return fmt.Errorf("remove ssh key: %w: %s", err, string(out))
	}
	return nil
}

func (m *Manager) GetGPUMetrics(ctx context.Context) (*domain.VMGPUMetrics, error) {
	m.mu.Lock()
	sshClient := m.sshClient
	m.mu.Unlock()

	if sshClient == nil {
		return nil, fmt.Errorf("SSH not ready")
	}

	metrics, err := sshClient.GetGPUMetrics(ctx)
	if err != nil {
		return nil, err
	}

	return &domain.VMGPUMetrics{
		Utilization: metrics.Utilization,
		Temperature: metrics.Temperature,
		MemoryUsed:  metrics.MemoryUsed,
		MemoryTotal: metrics.MemoryTotal,
	}, nil
}

func (m *Manager) SSHReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sshClient != nil
}

func (m *Manager) prepareDisk(vmID string, sizeGB int) (string, error) {
	if m.baseImage != "" {
		return m.images.CreateOverlay(vmID, m.baseImage)
	}
	if sizeGB == 0 {
		sizeGB = 50
	}
	return m.images.CreateDisk(vmID, sizeGB)
}

func (m *Manager) buildArgs(spec domain.InstanceSpec, diskPath, gpuAddr, qmpSocket string, net *NetworkConfig) []string {
	cpus := parseSMP(spec.CPUs, "4")
	mem := parseMemory(spec.Memory, "8G")

	args := []string{
		"-machine", "q35,accel=kvm",
		"-cpu", "host",
		"-smp", cpus,
		"-m", mem,
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", diskPath),
		"-device", fmt.Sprintf("vfio-pci,host=%s", gpuAddr),
		"-qmp", fmt.Sprintf("unix:%s,server,nowait", qmpSocket),
		"-nographic",
	}

	if m.ovmfPath != "" {
		args = append(args, "-bios", m.ovmfPath)
	}

	args = append(args, net.Args()...)
	return args
}

func (m *Manager) waitForQMP(qmp *QMPClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-m.done:
			return fmt.Errorf("QEMU process exited before QMP was ready")
		default:
		}

		if _, err := os.Stat(m.qmpSocket); err == nil {
			if err := qmp.Connect(); err == nil {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for QMP socket %s", m.qmpSocket)
}

func (m *Manager) sshExec(ctx context.Context, command string) ([]byte, error) {
	port := m.sshPort()
	if port == 0 {
		return nil, fmt.Errorf("no SSH port forwarding configured")
	}

	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-p", strconv.Itoa(port),
	}
	if m.sshKeyPath != "" {
		args = append(args, "-i", m.sshKeyPath)
	}
	args = append(args, "root@127.0.0.1", command)

	return exec.CommandContext(ctx, "ssh", args...).CombinedOutput()
}

func (m *Manager) sshPort() int {
	portStr, ok := m.ports["22"]
	if !ok {
		return 0
	}
	p, _ := strconv.Atoi(portStr)
	return p
}

func (m *Manager) forceKill() {
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		if m.done != nil {
			<-m.done
		}
	}
}

func (m *Manager) cleanup() {
	if m.qmp != nil {
		_ = m.qmp.Close()
		m.qmp = nil
	}

	if m.vfio != nil {
		if err := m.vfio.Unbind(); err != nil {
			m.logger.Warn("VFIO unbind error during cleanup", "err", err)
		}
		m.vfio = nil
	}

	if m.diskPath != "" {
		_ = m.images.RemoveDisk(m.diskPath)
	}

	if m.qmpSocket != "" {
		_ = os.Remove(m.qmpSocket)
	}

	m.reset()
}

func (m *Manager) reset() {
	m.vmID = ""
	m.cmd = nil
	m.logFile = nil
	m.vfio = nil
	m.qmp = nil
	m.sshClient = nil
	m.ports = nil
	m.diskPath = ""
	m.qmpSocket = ""
	m.gpuAddr = ""
	m.sshEnabled = false
	m.done = nil
	m.containerID = ""
	m.spec = nil
}

func parseSMP(s, fallback string) string {
	if s == "" {
		return fallback
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		n := int(f)
		if n < 1 {
			n = 1
		}
		return strconv.Itoa(n)
	}
	return s
}

func parseMemory(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return strings.ToUpper(strings.TrimSpace(s))
}

func mapQMPStatus(s string) domain.InstanceStatus {
	switch s {
	case "running":
		return domain.StatusRunning
	case "paused":
		return domain.StatusPaused
	case "prelaunch", "inmigrate":
		return domain.StatusPending
	case "shutdown", "postmigrate":
		return domain.StatusDestroyed
	default:
		return domain.StatusError
	}
}
