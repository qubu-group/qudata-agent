package qemu

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"net"
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
	QEMUBinary    string
	OVMFCodePath  string
	OVMFVarsPath  string
	BaseImagePath string
	ImageDir      string
	RunDir        string
	DefaultGPU    string
	SSHKeyPath    string
	DefaultCPUs   string
	DefaultMemory string
	DiskSizeGB    int
	TestMode      bool
}

type Manager struct {
	logger       *slog.Logger
	qemuBin      string
	ovmfCode     string
	ovmfVarsTmpl string
	baseImage    string
	defaultGPU   string
	runDir       string
	sshKeyPath   string
	defaultCPU   string
	defaultMem   string
	diskSizeGB   int
	testMode     bool
	images       *ImageManager

	mu           sync.Mutex
	vmID         string
	cmd          *exec.Cmd
	logFile      *os.File
	vfio         *VFIO
	qmp          *QMPClient
	sshClient    *SSHClient
	diskPath     string
	qmpSocket    string
	gpuAddr      string
	ovmfVarsPath string
	done         chan struct{}
	portPool     map[int]int
}

func NewManager(cfg Config, logger *slog.Logger) *Manager {
	cpus := cfg.DefaultCPUs
	if cpus == "" {
		cpus = "4"
	}
	mem := cfg.DefaultMemory
	if mem == "" {
		mem = "8G"
	}
	diskGB := cfg.DiskSizeGB
	if diskGB == 0 {
		diskGB = 50
	}

	return &Manager{
		logger:       logger,
		qemuBin:      cfg.QEMUBinary,
		ovmfCode:     cfg.OVMFCodePath,
		ovmfVarsTmpl: cfg.OVMFVarsPath,
		baseImage:    cfg.BaseImagePath,
		defaultGPU:   cfg.DefaultGPU,
		runDir:       cfg.RunDir,
		sshKeyPath:   cfg.SSHKeyPath,
		defaultCPU:   cpus,
		defaultMem:   mem,
		diskSizeGB:   diskGB,
		testMode:     cfg.TestMode,
		images:       NewImageManager(cfg.ImageDir),
	}
}

// KillOrphans finds and kills leftover VMs from previous agent runs,
// then unbinds any GPUs still attached to VFIO.
func (m *Manager) KillOrphans() {
	orphans, err := FindOrphanVMs(m.runDir)
	if err != nil {
		m.logger.Warn("failed to scan for orphan VMs", "err", err)
		return
	}
	for _, o := range orphans {
		m.logger.Info("killing orphan VM", "vm_id", o.VMID, "pid", o.PID)
		_ = KillProcess(o.PID)
		_ = os.Remove(o.QMPSocket)
	}

	if m.defaultGPU != "" {
		vfio := NewVFIO(m.defaultGPU)
		vfio.RestoreBinding()
		if vfio.Bound() {
			m.logger.Info("unbinding orphan GPU from VFIO", "addr", m.defaultGPU)
			_ = vfio.Unbind()
		}
	}
}

// Create boots a new VM with GPU passthrough. hostPorts maps guest ports to
// pre-allocated host ports. Blocks until SSH is ready.
func (m *Manager) Create(ctx context.Context, spec domain.InstanceSpec, hostPorts []int) (domain.InstancePorts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.vmID != "" {
		return nil, domain.ErrInstanceAlreadyRunning{}
	}

	gpuAddr := spec.GPUAddr
	if gpuAddr == "" {
		gpuAddr = m.defaultGPU
	}
	if gpuAddr == "" {
		return nil, domain.ErrQEMU{Op: "create", Err: fmt.Errorf("no GPU PCI address")}
	}

	cpus := spec.CPUs
	if cpus == "" {
		cpus = m.defaultCPU
	}
	mem := spec.Memory
	if mem == "" {
		mem = m.defaultMem
	}
	diskGB := spec.DiskSizeGB
	if diskGB == 0 {
		diskGB = m.diskSizeGB
	}

	vfio := NewVFIO(gpuAddr)
	if err := vfio.Bind(); err != nil {
		return nil, domain.ErrVFIO{Op: "bind", Addr: gpuAddr, Err: err}
	}

	vmID := "vm-" + uuid.New().String()[:8]

	diskPath, err := m.prepareDisk(vmID)
	if err != nil {
		_ = vfio.Unbind()
		return nil, domain.ErrQEMU{Op: "disk", Err: err}
	}

	guestPorts := make([]int, 0, len(spec.Ports)+1)
	if spec.SSHEnabled {
		guestPorts = append(guestPorts, 22)
	}
	for _, pm := range spec.Ports {
		guestPorts = append(guestPorts, pm.GuestPort)
	}

	if len(hostPorts) < len(guestPorts) {
		_ = m.images.RemoveDisk(diskPath)
		_ = vfio.Unbind()
		return nil, domain.ErrQEMU{Op: "ports", Err: fmt.Errorf("not enough host ports: need %d, got %d", len(guestPorts), len(hostPorts))}
	}

	pool := make(map[int]int, len(guestPorts))
	for i, gp := range guestPorts {
		pool[gp] = hostPorts[i]
	}

	netCfg := NewNetworkConfig("net0", m.testMode)
	for guestPort, hostPort := range pool {
		netCfg.AddForward("tcp", hostPort, guestPort)
	}

	if err := os.MkdirAll(m.runDir, 0o755); err != nil {
		_ = m.images.RemoveDisk(diskPath)
		_ = vfio.Unbind()
		return nil, domain.ErrQEMU{Op: "rundir", Err: err}
	}

	ovmfVarsPath, err := m.copyOVMFVars(vmID)
	if err != nil {
		_ = m.images.RemoveDisk(diskPath)
		_ = vfio.Unbind()
		return nil, domain.ErrQEMU{Op: "ovmf", Err: err}
	}

	qmpSocket := filepath.Join(m.runDir, vmID+".qmp")
	args := m.buildVMArgs(diskPath, gpuAddr, qmpSocket, netCfg, cpus, mem, ovmfVarsPath)

	logFile, _ := os.Create(filepath.Join(m.runDir, vmID+".log"))

	m.logger.Info("starting VM", "vm_id", vmID, "gpu", gpuAddr, "cpus", cpus, "mem", mem)

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
	m.portPool = pool
	m.diskPath = diskPath
	m.qmpSocket = qmpSocket
	m.gpuAddr = gpuAddr
	m.ovmfVarsPath = ovmfVarsPath

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
		m.logger.Warn("QMP connect failed", "err", err)
	} else {
		m.qmp = qmpClient
	}

	m.logger.Info("VM started", "vm_id", vmID, "pid", cmd.Process.Pid)

	sshPort, hasSSH := pool[22]
	if hasSSH {
		sshClient := NewSSHClient("127.0.0.1", sshPort, m.sshKeyPath)

		m.mu.Unlock()
		sshErr := sshClient.WaitForBoot(ctx, 180*time.Second)
		m.mu.Lock()

		if sshErr != nil {
			m.logger.Error("VM SSH timeout", "err", sshErr)
			m.stopLocked(context.Background())
			return nil, fmt.Errorf("VM SSH not ready: %w", sshErr)
		}

		m.sshClient = sshClient
		m.logger.Info("VM SSH ready", "vm_id", vmID)

		// Set root password and log it.
		rootPass := generatePassword(16)
		if out, err := sshClient.Run(ctx, fmt.Sprintf("echo 'root:%s' | chpasswd", rootPass)); err != nil {
			m.logger.Warn("failed to set root password", "err", err, "output", string(out))
		} else {
			m.logger.Info("VM root credentials", "username", "root", "password", rootPass)
		}
	}

	portMap := make(domain.InstancePorts, len(pool))
	for gp, hp := range pool {
		portMap[strconv.Itoa(gp)] = strconv.Itoa(hp)
	}

	return portMap, nil
}

// Stop gracefully shuts down the VM and releases GPU back to the host.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(ctx)
}

func (m *Manager) stopLocked(ctx context.Context) error {
	if m.vmID == "" {
		return nil
	}

	if m.qmp != nil && m.qmp.Connected() {
		if err := m.qmp.Shutdown(); err != nil {
			m.logger.Warn("QMP shutdown failed, will force-kill", "err", err)
		}
	}

	if m.done != nil {
		select {
		case <-m.done:
			m.logger.Info("VM exited gracefully")
		case <-time.After(30 * time.Second):
			m.logger.Warn("VM did not exit in time, killing")
			m.forceKill()
		}
	}

	m.cleanup()
	return nil
}

// Manage executes a lifecycle command (pause/resume/reboot) on the running VM.
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

// Status returns the current lifecycle status of the VM.
func (m *Manager) Status(ctx context.Context) domain.InstanceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

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

func (m *Manager) VMID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vmID
}

func (m *Manager) HostPortForGuest(guestPort int) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hp, ok := m.portPool[guestPort]
	return hp, ok
}

// CollectStats gathers GPU, CPU and RAM metrics from the running VM via SSH.
func (m *Manager) CollectStats(ctx context.Context) *domain.StatsSnapshot {
	m.mu.Lock()
	ssh := m.sshClient
	m.mu.Unlock()

	if ssh == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Single SSH call: GPU (nvidia-smi) + CPU/RAM (from /proc).
	cmd := `nvidia-smi --query-gpu=utilization.gpu,temperature.gpu,memory.used,memory.total --format=csv,noheader,nounits 2>/dev/null; ` +
		`echo "---"; ` +
		`awk '{u=$2+$4; t=$2+$4+$5; if(NR>1) printf "%.1f\n", (u-pu)/(t-pt)*100; pu=u; pt=t}' <(head -1 /proc/stat; sleep 0.3; head -1 /proc/stat); ` +
		`awk '/MemTotal/{t=$2} /MemAvailable/{a=$2} END{printf "%.1f\n", (t-a)/t*100}' /proc/meminfo`

	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return nil
	}

	return parseVMStats(string(out))
}

func parseVMStats(output string) *domain.StatsSnapshot {
	parts := strings.SplitN(output, "---\n", 2)
	snap := &domain.StatsSnapshot{}

	// GPU part (before "---")
	if len(parts) >= 1 {
		gpuLine := strings.TrimSpace(parts[0])
		if gpuLine != "" {
			fields := strings.Split(gpuLine, ",")
			if len(fields) >= 4 {
				snap.GPUUtil, _ = strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
				snap.GPUTemp, _ = strconv.Atoi(strings.TrimSpace(fields[1]))
				memUsed, _ := strconv.ParseFloat(strings.TrimSpace(fields[2]), 64)
				memTotal, _ := strconv.ParseFloat(strings.TrimSpace(fields[3]), 64)
				if memTotal > 0 {
					snap.MemUtil = memUsed / memTotal * 100
				}
			}
		}
	}

	// CPU + RAM part (after "---")
	if len(parts) >= 2 {
		lines := strings.Split(strings.TrimSpace(parts[1]), "\n")
		if len(lines) >= 1 {
			snap.CPUUtil, _ = strconv.ParseFloat(strings.TrimSpace(lines[0]), 64)
		}
		if len(lines) >= 2 {
			snap.RAMUtil, _ = strconv.ParseFloat(strings.TrimSpace(lines[1]), 64)
		}
	}

	return snap
}

func (m *Manager) AddSSHKey(_ context.Context, pubkey string) error {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return fmt.Errorf("empty SSH public key")
	}

	// Use a dedicated context with generous timeout — never inherit the short HTTP request context.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := fmt.Sprintf(
		`mkdir -p /root/.ssh && chmod 700 /root/.ssh && echo '%s' >> /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys`,
		pubkey,
	)
	m.logger.Info("injecting SSH key into VM")
	out, err := m.sshExec(ctx, cmd)
	if err != nil {
		m.logger.Error("SSH key injection failed", "err", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("add ssh key: %w: %s", err, string(out))
	}

	verifyOut, verifyErr := m.sshExec(ctx, "wc -l /root/.ssh/authorized_keys")
	m.logger.Info("SSH key injected", "authorized_keys_check", strings.TrimSpace(string(verifyOut)), "verify_err", verifyErr)
	return nil
}

func (m *Manager) RemoveSSHKey(_ context.Context, pubkey string) error {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return fmt.Errorf("empty SSH public key")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	escaped := strings.ReplaceAll(pubkey, "/", `\/`)
	cmd := fmt.Sprintf(`sed -i '/%s/d' /root/.ssh/authorized_keys`, escaped)
	if out, err := m.sshExec(ctx, cmd); err != nil {
		return fmt.Errorf("remove ssh key: %w: %s", err, string(out))
	}
	return nil
}

func (m *Manager) prepareDisk(vmID string) (string, error) {
	if m.baseImage != "" {
		return m.images.CreateOverlay(vmID, m.baseImage)
	}
	return m.images.CreateDisk(vmID, m.diskSizeGB)
}

func (m *Manager) buildVMArgs(diskPath, gpuAddr, qmpSocket string, net *NetworkConfig, cpus, mem, ovmfVarsPath string) []string {
	args := []string{
		"-machine", "q35,accel=kvm",
		"-cpu", "host",
		"-smp", cpus,
		"-m", strings.ToUpper(strings.TrimSpace(mem)),
	}
	if m.ovmfCode != "" && ovmfVarsPath != "" {
		args = append(args,
			"-drive", fmt.Sprintf("if=pflash,format=raw,readonly=on,file=%s", m.ovmfCode),
			"-drive", fmt.Sprintf("if=pflash,format=raw,file=%s", ovmfVarsPath),
		)
	}
	args = append(args,
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", diskPath),
		"-device", fmt.Sprintf("vfio-pci,host=%s", gpuAddr),
		"-qmp", fmt.Sprintf("unix:%s,server,nowait", qmpSocket),
		"-nographic",
	)
	args = append(args, net.Args()...)
	return args
}

func (m *Manager) copyOVMFVars(vmID string) (string, error) {
	if m.ovmfVarsTmpl == "" {
		return "", nil
	}
	dst := filepath.Join(m.runDir, vmID+"-OVMF_VARS.fd")
	src, err := os.ReadFile(m.ovmfVarsTmpl)
	if err != nil {
		return "", fmt.Errorf("read OVMF_VARS template %s: %w", m.ovmfVarsTmpl, err)
	}
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		return "", fmt.Errorf("write OVMF_VARS %s: %w", dst, err)
	}
	return dst, nil
}

func (m *Manager) waitForQMP(qmp *QMPClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-m.done:
			return fmt.Errorf("QEMU exited before QMP ready")
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
	sshPort, ok := m.portPool[22]
	if !ok {
		return nil, fmt.Errorf("no SSH port forwarding configured")
	}
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-p", strconv.Itoa(sshPort),
	}
	if m.sshKeyPath != "" {
		args = append(args, "-i", m.sshKeyPath)
	}
	args = append(args, "root@127.0.0.1", command)
	return exec.CommandContext(ctx, "ssh", args...).CombinedOutput()
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
	if m.ovmfVarsPath != "" {
		_ = os.Remove(m.ovmfVarsPath)
	}

	m.vmID = ""
	m.cmd = nil
	m.logFile = nil
	m.sshClient = nil
	m.portPool = nil
	m.diskPath = ""
	m.qmpSocket = ""
	m.gpuAddr = ""
	m.ovmfVarsPath = ""
	m.done = nil
}

func allocatePortPool(guestPorts []int) (map[int]int, error) {
	pool := make(map[int]int, len(guestPorts))
	for _, gp := range guestPorts {
		hp, err := findFreePort()
		if err != nil {
			return nil, fmt.Errorf("allocate host port for guest %d: %w", gp, err)
		}
		pool[gp] = hp
	}
	return pool, nil
}

func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
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

func generatePassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[n.Int64()]
	}
	return string(b)
}
