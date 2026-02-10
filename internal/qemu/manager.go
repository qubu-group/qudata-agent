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

// Config holds the configuration required by the QEMU VM manager.
type Config struct {
	QEMUBinary     string
	OVMFPath       string
	BaseImagePath  string
	ImageDir       string
	RunDir         string
	DefaultGPUAddr string
	SSHKeyPath     string
}

// Manager implements domain.VMManager for QEMU virtual machines with VFIO
// GPU passthrough. It manages the full VM lifecycle: VFIO binding, disk
// provisioning, QEMU process control via QMP, and SSH-based guest management.
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
	ports      domain.InstancePorts
	diskPath   string
	qmpSocket  string
	gpuAddr    string
	sshEnabled bool
	done       chan struct{} // closed when the QEMU process exits
}

// NewManager creates a QEMU VM manager from the given configuration.
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

// Create provisions a QEMU VM with VFIO GPU passthrough and starts it.
//
// Steps: bind GPU → create disk overlay → build QEMU args → start process → connect QMP.
func (m *Manager) Create(ctx context.Context, spec domain.InstanceSpec, hostPorts []int) (domain.InstancePorts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.vmID != "" || m.creating {
		return nil, domain.ErrInstanceAlreadyRunning{}
	}
	m.creating = true
	defer func() { m.creating = false }()

	// Resolve GPU PCI address.
	gpuAddr := spec.GPUAddr
	if gpuAddr == "" {
		gpuAddr = m.defaultGPU
	}
	if gpuAddr == "" {
		return nil, domain.ErrQEMU{Op: "create", Err: fmt.Errorf("no GPU PCI address configured")}
	}

	// Bind GPU to VFIO.
	vfio := NewVFIO(gpuAddr)
	if err := vfio.Bind(); err != nil {
		return nil, domain.ErrVFIO{Op: "bind", Addr: gpuAddr, Err: err}
	}

	// Generate a unique VM identifier.
	vmID := "vm-" + uuid.New().String()[:8]

	// Prepare disk image.
	diskPath, err := m.prepareDisk(vmID, spec.DiskSizeGB)
	if err != nil {
		_ = vfio.Unbind()
		return nil, domain.ErrQEMU{Op: "disk", Err: err}
	}

	// Build host↔guest port mappings.
	portMap := make(domain.InstancePorts)
	netCfg := NewNetworkConfig("net0")
	for i, pm := range spec.Ports {
		if i < len(hostPorts) {
			portMap[strconv.Itoa(pm.ContainerPort)] = strconv.Itoa(hostPorts[i])
			netCfg.AddForward("tcp", hostPorts[i], pm.ContainerPort)
		}
	}

	// Build QEMU command line.
	qmpSocket := filepath.Join(m.runDir, vmID+".qmp")
	args := m.buildArgs(spec, diskPath, gpuAddr, qmpSocket, netCfg)

	// Ensure runtime directory exists.
	if err := os.MkdirAll(m.runDir, 0o755); err != nil {
		_ = m.images.RemoveDisk(diskPath)
		_ = vfio.Unbind()
		return nil, domain.ErrQEMU{Op: "rundir", Err: err}
	}

	// Open log file for QEMU stdout/stderr.
	logFile, _ := os.Create(filepath.Join(m.runDir, vmID+".log"))

	// Start the QEMU process.
	m.logger.Info("starting QEMU VM",
		"vm_id", vmID,
		"gpu", gpuAddr,
		"disk", diskPath,
	)

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

	// Populate manager state.
	m.vmID = vmID
	m.cmd = cmd
	m.logFile = logFile
	m.vfio = vfio
	m.ports = portMap
	m.diskPath = diskPath
	m.qmpSocket = qmpSocket
	m.gpuAddr = gpuAddr
	m.sshEnabled = spec.SSHEnabled

	// Monitor the process in the background.
	m.done = make(chan struct{})
	go func() {
		_ = cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}
		close(m.done)
	}()

	// Wait for the QMP socket to appear and establish a control connection.
	qmpClient := NewQMPClient(qmpSocket)
	if err := m.waitForQMP(qmpClient, 30*time.Second); err != nil {
		m.logger.Warn("QMP connect failed; VM may still be booting", "err", err)
	} else {
		m.qmp = qmpClient
	}

	m.logger.Info("QEMU VM started",
		"vm_id", vmID,
		"pid", cmd.Process.Pid,
		"ports", portMap,
	)

	return portMap, nil
}

// Manage executes a lifecycle command on the running VM.
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

// Stop gracefully shuts down the VM via ACPI, waits for the process to exit,
// then releases the GPU and removes the disk.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.vmID == "" {
		return nil
	}

	// Request ACPI shutdown.
	if m.qmp != nil && m.qmp.Connected() {
		if err := m.qmp.Shutdown(); err != nil {
			m.logger.Warn("QMP shutdown failed, will force-kill", "err", err)
		}
	}

	// Wait for the process to exit with a timeout.
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

// Status returns the current VM lifecycle state by querying QMP.
func (m *Manager) Status(ctx context.Context) domain.InstanceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.creating {
		return domain.StatusPending
	}
	if m.vmID == "" {
		return domain.StatusDestroyed
	}

	// Check if the process has exited.
	if m.done != nil {
		select {
		case <-m.done:
			return domain.StatusError
		default:
		}
	}

	// Query QMP if connected.
	if m.qmp != nil && m.qmp.Connected() {
		status, _, err := m.qmp.QueryStatus()
		if err == nil {
			return mapQMPStatus(status)
		}
	}

	return domain.StatusRunning
}

// IsRunning reports whether a VM is currently active.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vmID != ""
}

// VMID returns the identifier of the running VM.
func (m *Manager) VMID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vmID
}

// Ports returns the current guest-to-host port mappings.
func (m *Manager) Ports() domain.InstancePorts {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ports
}

// RestoreState reconstructs the manager from a previously persisted InstanceState.
// It attempts to reconnect to a running QEMU process via the QMP socket.
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

	// Derive runtime paths from VM ID.
	m.qmpSocket = filepath.Join(m.runDir, m.vmID+".qmp")
	m.diskPath = m.images.DiskPath(m.vmID)

	// Restore VFIO state.
	if m.gpuAddr != "" {
		m.vfio = NewVFIO(m.gpuAddr)
		m.vfio.RestoreBinding()
	}

	// Attempt to reconnect QMP.
	qmp := NewQMPClient(m.qmpSocket)
	if err := qmp.Connect(); err != nil {
		m.logger.Warn("QMP reconnect failed during restore", "err", err)
	} else {
		m.qmp = qmp
		m.logger.Info("QMP reconnected after restore", "vm_id", m.vmID)
	}
}

// AddSSHKey installs an SSH public key inside the running VM via SSH.
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

// RemoveSSHKey removes an SSH public key from the running VM via SSH.
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

// --- internal helpers ---

// prepareDisk creates the VM disk, preferring a CoW overlay on the base image.
func (m *Manager) prepareDisk(vmID string, sizeGB int) (string, error) {
	if m.baseImage != "" {
		return m.images.CreateOverlay(vmID, m.baseImage)
	}
	if sizeGB == 0 {
		sizeGB = 50
	}
	return m.images.CreateDisk(vmID, sizeGB)
}

// buildArgs constructs the full qemu-system-x86_64 command line.
func (m *Manager) buildArgs(
	spec domain.InstanceSpec,
	diskPath, gpuAddr, qmpSocket string,
	net *NetworkConfig,
) []string {
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

// waitForQMP polls for the QMP Unix socket to appear, then connects.
func (m *Manager) waitForQMP(qmp *QMPClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check if QEMU already exited.
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

// sshExec runs a command inside the VM over SSH through the forwarded port.
func (m *Manager) sshExec(ctx context.Context, command string) ([]byte, error) {
	port := m.sshPort()
	if port == 0 {
		return nil, fmt.Errorf("no SSH port forwarding configured (guest port 22 not mapped)")
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

// sshPort returns the host port forwarded to guest port 22, or 0 if absent.
func (m *Manager) sshPort() int {
	portStr, ok := m.ports["22"]
	if !ok {
		return 0
	}
	p, _ := strconv.Atoi(portStr)
	return p
}

// forceKill sends SIGKILL to the QEMU process.
func (m *Manager) forceKill() {
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		if m.done != nil {
			<-m.done
		}
	}
}

// cleanup releases all resources held by a stopped VM.
func (m *Manager) cleanup() {
	// Close QMP connection.
	if m.qmp != nil {
		_ = m.qmp.Close()
		m.qmp = nil
	}

	// Unbind GPU from VFIO.
	if m.vfio != nil {
		if err := m.vfio.Unbind(); err != nil {
			m.logger.Warn("VFIO unbind error during cleanup", "err", err)
		}
		m.vfio = nil
	}

	// Remove disk image.
	if m.diskPath != "" {
		_ = m.images.RemoveDisk(m.diskPath)
	}

	// Remove QMP socket.
	if m.qmpSocket != "" {
		_ = os.Remove(m.qmpSocket)
	}

	m.reset()
}

// reset zeroes all runtime state fields.
func (m *Manager) reset() {
	m.vmID = ""
	m.cmd = nil
	m.logFile = nil
	m.vfio = nil
	m.qmp = nil
	m.ports = nil
	m.diskPath = ""
	m.qmpSocket = ""
	m.gpuAddr = ""
	m.sshEnabled = false
	m.done = nil
}

// parseSMP extracts an integer CPU count from the spec, falling back to a default.
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

// parseMemory normalizes the memory size string to QEMU format (uppercase suffix).
func parseMemory(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return strings.ToUpper(strings.TrimSpace(s))
}

// mapQMPStatus converts a QMP status string to a domain.InstanceStatus.
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
