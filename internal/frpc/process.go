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

	"github.com/qudata/agent/internal/domain"
)

// Process manages the FRPC subprocess lifecycle and its configuration.
// It automatically restarts the FRPC process if it exits unexpectedly.
type Process struct {
	logger     *slog.Logger
	binaryPath string
	configPath string

	mu     sync.Mutex
	cmd    *exec.Cmd
	config *Config
	done   chan struct{} // closed when the process exits

	// Auto-restart
	stopCtx    context.Context
	stopCancel context.CancelFunc
}

// NewProcess creates a new FRPC process manager.
func NewProcess(binaryPath, configPath string, logger *slog.Logger) *Process {
	return &Process{
		logger:     logger,
		binaryPath: binaryPath,
		configPath: configPath,
	}
}

// Start initializes the FRPC config and starts the process with auto-restart.
func (p *Process) Start(frp *domain.FRPInfo, agentPort int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Validate frpc binary exists
	if _, err := os.Stat(p.binaryPath); err != nil {
		return domain.ErrFRPC{Op: "start", Err: fmt.Errorf("frpc binary not found at %s: %w", p.binaryPath, err)}
	}

	// Create auto-restart context
	p.stopCtx, p.stopCancel = context.WithCancel(context.Background())

	// Create config
	p.config = NewConfig(frp, agentPort)
	if err := p.writeConfig(); err != nil {
		return err
	}

	return p.startProcess()
}

// UpdateInstanceProxies adds instance proxies, rewrites config, and restarts FRPC.
func (p *Process) UpdateInstanceProxies(proxies []domain.FRPProxy) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.config == nil {
		return domain.ErrFRPC{Op: "update", Err: fmt.Errorf("frpc not initialized")}
	}

	p.config.AddInstanceProxies(proxies)

	if err := p.writeConfig(); err != nil {
		return err
	}

	return p.restart()
}

// ClearInstanceProxies removes all instance proxies, rewrites config, and restarts FRPC.
func (p *Process) ClearInstanceProxies() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.config == nil {
		return domain.ErrFRPC{Op: "clear", Err: fmt.Errorf("frpc not initialized")}
	}

	p.config.ClearInstanceProxies()

	if err := p.writeConfig(); err != nil {
		return err
	}

	return p.restart()
}

// Stop gracefully stops the FRPC process and disables auto-restart.
func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Signal that we're stopping intentionally — disable auto-restart
	if p.stopCancel != nil {
		p.stopCancel()
	}

	return p.stopProcess()
}

// GetConfig returns the current config (for state persistence).
func (p *Process) GetConfig() *Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.config
}

// --- internal ---

func (p *Process) writeConfig() error {
	if err := os.MkdirAll(filepath.Dir(p.configPath), 0o755); err != nil {
		return domain.ErrFRPC{Op: "write-config", Err: fmt.Errorf("create config dir: %w", err)}
	}

	data, err := p.config.Render()
	if err != nil {
		return domain.ErrFRPC{Op: "write-config", Err: err}
	}

	if err := os.WriteFile(p.configPath, data, 0o644); err != nil {
		return domain.ErrFRPC{Op: "write-config", Err: fmt.Errorf("write file: %w", err)}
	}

	p.logger.Info("frpc config written", "path", p.configPath)
	return nil
}

func (p *Process) startProcess() error {
	p.cmd = exec.Command(p.binaryPath, "-c", p.configPath)
	p.cmd.Stdout = os.Stdout
	p.cmd.Stderr = os.Stderr

	if err := p.cmd.Start(); err != nil {
		return domain.ErrFRPC{Op: "start", Err: fmt.Errorf("start process: %w", err)}
	}

	p.logger.Info("frpc started",
		"pid", p.cmd.Process.Pid,
		"config", p.configPath,
	)

	// Monitor process in background; signal via done channel
	p.done = make(chan struct{})
	go func() {
		err := p.cmd.Wait()
		close(p.done)

		// Auto-restart if the process exited unexpectedly
		// (not during a controlled stop/restart)
		if p.stopCtx != nil {
			select {
			case <-p.stopCtx.Done():
				// Agent is shutting down — do not restart
				p.logger.Info("frpc process exited during shutdown")
				return
			default:
			}
		}

		if err != nil {
			p.logger.Error("frpc process crashed, restarting in 3s", "err", err)
		} else {
			p.logger.Warn("frpc process exited unexpectedly, restarting in 3s")
		}

		time.Sleep(3 * time.Second)

		p.mu.Lock()
		defer p.mu.Unlock()
		if restartErr := p.startProcess(); restartErr != nil {
			p.logger.Error("frpc auto-restart failed", "err", restartErr)
		}
	}()

	// Brief delay to verify the process started successfully
	select {
	case <-p.done:
		return domain.ErrFRPC{Op: "start", Err: fmt.Errorf("frpc exited immediately with code %d", p.cmd.ProcessState.ExitCode())}
	case <-time.After(500 * time.Millisecond):
		// Process is still running — good
	}

	return nil
}

func (p *Process) stopProcess() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}

	p.logger.Info("stopping frpc", "pid", p.cmd.Process.Pid)

	// Check if the process already exited
	if p.done != nil {
		select {
		case <-p.done:
			p.logger.Info("frpc already exited")
			p.cmd = nil
			return nil
		default:
		}
	}

	// Send SIGTERM first for graceful shutdown
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		p.logger.Warn("sigterm failed, killing", "err", err)
		_ = p.cmd.Process.Kill()
	}

	// Wait for the monitor goroutine to confirm exit
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
