package frpc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Process manages the FRPC subprocess lifecycle and its configuration.
type Process struct {
	logger     *slog.Logger
	binaryPath string
	configPath string

	mu     sync.Mutex
	cmd    *exec.Cmd
	config *Config
	done   chan struct{}

	// runCtx is created per startProcess call and cancelled in stopProcess.
	// This ensures the auto-restart goroutine is cancelled on both
	// intentional restart() and full Stop().
	runCtx    context.Context
	runCancel context.CancelFunc
}

func NewProcess(binaryPath, configPath string, logger *slog.Logger) *Process {
	return &Process{
		logger:     logger,
		binaryPath: binaryPath,
		configPath: configPath,
	}
}

func (p *Process) Start(agentID, tunnelToken string, agentPort int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := os.Stat(p.binaryPath); err != nil {
		return fmt.Errorf("frpc binary not found at %s: %w", p.binaryPath, err)
	}

	p.config = NewConfig(agentID, tunnelToken, agentPort)

	if err := p.writeConfig(); err != nil {
		return err
	}

	return p.startProcess()
}

func (p *Process) UpdateInstanceProxies(proxies []Proxy) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.config == nil {
		return fmt.Errorf("frpc not initialized")
	}

	for _, proxy := range proxies {
		p.config.AddInstanceProxy(proxy)
	}

	if err := p.writeConfig(); err != nil {
		return err
	}

	return p.restart()
}

func (p *Process) ClearInstanceProxies() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.config == nil {
		return fmt.Errorf("frpc not initialized")
	}

	p.config.ClearInstanceProxies()

	if err := p.writeConfig(); err != nil {
		return err
	}

	return p.restart()
}

func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopProcess()
}

func (p *Process) GetConfig() *Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.config
}

func (p *Process) writeConfig() error {
	if err := os.MkdirAll(filepath.Dir(p.configPath), 0o755); err != nil {
		return fmt.Errorf("frpc write-config: create dir: %w", err)
	}

	data, err := p.config.Render()
	if err != nil {
		return fmt.Errorf("frpc write-config: render: %w", err)
	}

	if err := os.WriteFile(p.configPath, data, 0o644); err != nil {
		return fmt.Errorf("frpc write-config: write file: %w", err)
	}

	p.logger.Info("frpc config written", "path", p.configPath)
	return nil
}

// startProcess launches the frpc binary and starts a monitor goroutine.
// Must be called with p.mu held.
func (p *Process) startProcess() error {
	// Cancel any lingering monitor goroutine from a previous run.
	if p.runCancel != nil {
		p.runCancel()
	}
	p.runCtx, p.runCancel = context.WithCancel(context.Background())

	p.cmd = exec.Command(p.binaryPath, "-c", p.configPath)
	p.cmd.Stdout = os.Stdout
	p.cmd.Stderr = os.Stderr

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("frpc start: %w", err)
	}

	p.logger.Info("frpc started", "pid", p.cmd.Process.Pid, "config", p.configPath)

	p.done = make(chan struct{})
	go p.monitor(p.runCtx)

	// Give the process a moment — detect immediate exit.
	select {
	case <-p.done:
		return fmt.Errorf("frpc exited immediately with code %d", p.cmd.ProcessState.ExitCode())
	case <-time.After(500 * time.Millisecond):
	}

	return nil
}

// monitor waits for the frpc process to exit. If the exit was unexpected
// (ctx not cancelled), it auto-restarts after a delay.
func (p *Process) monitor(ctx context.Context) {
	cmd := p.cmd
	done := p.done

	err := cmd.Wait()
	close(done)

	// If ctx is cancelled, stop or restart was intentional — do not auto-restart.
	select {
	case <-ctx.Done():
		p.logger.Info("frpc process exited (controlled)")
		return
	default:
	}

	if err != nil {
		p.logger.Error("frpc crashed, restarting in 3s", "err", err)
	} else {
		p.logger.Warn("frpc exited unexpectedly, restarting in 3s")
	}

	// Wait before restart, but bail if context is cancelled during the wait.
	select {
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check: context may have been cancelled while waiting for the lock.
	select {
	case <-ctx.Done():
		return
	default:
	}

	if restartErr := p.startProcess(); restartErr != nil {
		p.logger.Error("frpc auto-restart failed", "err", restartErr)
	}
}

// stopProcess terminates the running frpc subprocess.
// Must be called with p.mu held.
func (p *Process) stopProcess() error {
	// Cancel the monitor goroutine first to prevent auto-restart.
	if p.runCancel != nil {
		p.runCancel()
		p.runCancel = nil
	}

	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}

	p.logger.Info("stopping frpc", "pid", p.cmd.Process.Pid)

	if p.done != nil {
		select {
		case <-p.done:
			p.logger.Info("frpc already exited")
			p.cmd = nil
			return nil
		default:
		}
	}

	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		p.logger.Warn("sigterm failed, killing", "err", err)
		_ = p.cmd.Process.Kill()
	}

	if p.done != nil {
		select {
		case <-p.done:
			p.logger.Info("frpc stopped gracefully")
		case <-time.After(5 * time.Second):
			p.logger.Warn("frpc did not stop in time, killing")
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	}

	p.cmd = nil
	return nil
}

func (p *Process) restart() error {
	if err := p.stopProcess(); err != nil {
		p.logger.Warn("error stopping frpc for restart", "err", err)
	}
	return p.startProcess()
}
