package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/qudata/agent/internal/config"
	"github.com/qudata/agent/internal/domain"
	"github.com/qudata/agent/internal/frpc"
	"github.com/qudata/agent/internal/gpu"
	"github.com/qudata/agent/internal/network"
	"github.com/qudata/agent/internal/qemu"
	"github.com/qudata/agent/internal/qudata"
	"github.com/qudata/agent/internal/server"
	"github.com/qudata/agent/internal/ssh"
	"github.com/qudata/agent/internal/storage"
	"github.com/qudata/agent/internal/system"
)

type Agent struct {
	cfg    *config.Config
	logger *slog.Logger

	store      *storage.Store
	api        *qudata.Client
	vm         domain.VMManager
	frpcProc   *frpc.Process
	ports      *network.PortAllocator
	probe      *system.Probe
	stats      *system.StatsCollector
	gpuMetrics *gpu.Metrics

	httpServer *server.Server
	meta       *domain.AgentMetadata
}

func New(cfg *config.Config, logger *slog.Logger) (*Agent, error) {
	store, err := storage.NewStore(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("init storage: %w", err)
	}

	gpuMetrics := gpu.NewMetrics(cfg.Debug, logger)
	probe := system.NewProbe(gpuMetrics)
	statsCollector := system.NewStatsCollector(gpuMetrics)
	api := qudata.NewClient(cfg.APIKey, cfg.ServiceURL, logger)
	frpcProc := frpc.NewProcess(cfg.FRPCBinary, cfg.FRPCConfigPath, logger)
	portAlloc := network.NewPortAllocator()

	vm, err := newVMManager(cfg, logger, statsCollector)
	if err != nil {
		return nil, fmt.Errorf("init vm backend: %w", err)
	}

	return &Agent{
		cfg:        cfg,
		logger:     logger,
		store:      store,
		api:        api,
		vm:         vm,
		frpcProc:   frpcProc,
		ports:      portAlloc,
		probe:      probe,
		stats:      statsCollector,
		gpuMetrics: gpuMetrics,
	}, nil
}

func newVMManager(cfg *config.Config, logger *slog.Logger, stats *system.StatsCollector) (domain.VMManager, error) {
	sshKeyPath := cfg.ManagementKeyPath
	if sshKeyPath == "" {
		keyPair, err := ssh.EnsureManagementKey(cfg.DataDir + "/.ssh")
		if err != nil {
			return nil, fmt.Errorf("ensure management key: %w", err)
		}
		sshKeyPath = keyPair.PrivateKeyPath
		logger.Info("using management key", "path", sshKeyPath)
	}

	mgr := qemu.NewManager(qemu.Config{
		QEMUBinary:     cfg.QEMUBinary,
		OVMFPath:       cfg.OVMFPath,
		BaseImagePath:  cfg.BaseImagePath,
		ImageDir:       cfg.ImageDir,
		RunDir:         cfg.VMRunDir,
		DefaultGPUAddr: cfg.GPUPCIAddr,
		SSHKeyPath:     sshKeyPath,
	}, logger)

	stats.SetVMMetricsProvider(mgr)
	return mgr, nil
}

func (a *Agent) Run(ctx context.Context) error {
	meta, err := a.bootstrap(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	a.meta = meta

	if meta.FRP != nil {
		if err := a.frpcProc.Start(meta.FRP, meta.Port); err != nil {
			return fmt.Errorf("start frpc: %w", err)
		}
		a.logger.Info("FRPC tunnel established", "server", meta.FRP.ServerAddr, "subdomain", meta.FRP.Subdomain)
	} else {
		a.logger.Warn("no FRP info received from API, running without tunnel")
	}

	if err := a.restoreState(ctx); err != nil {
		a.logger.Warn("failed to restore instance state", "err", err)
	}

	go a.publishStats(ctx)

	a.httpServer = server.New(
		meta.Port,
		meta.SecretKey,
		a.subdomain(),
		a.vm,
		a.frpcProc,
		a.ports,
		a.store,
		a.logger,
	)

	a.logger.Info("agent ready",
		"version", config.Version,
		"agent_id", meta.ID,
		"port", meta.Port,
		"address", meta.Address,
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.httpServer.Start()
	}()

	select {
	case <-ctx.Done():
		a.logger.Info("shutting down agent")
		return a.shutdown()
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	}
}

func (a *Agent) bootstrap(ctx context.Context) (*domain.AgentMetadata, error) {
	agentID, err := a.store.AgentID()
	if err != nil {
		return nil, fmt.Errorf("agent id: %w", err)
	}

	agentPort, err := a.ports.AllocateOne()
	if err != nil {
		return nil, fmt.Errorf("allocate agent port: %w", err)
	}

	address := system.PublicIP()
	fingerprint := a.probe.Fingerprint()

	a.logger.Info("pinging Qudata API", "url", a.cfg.ServiceURL)
	if err := a.api.Ping(ctx); err != nil {
		return nil, fmt.Errorf("api ping: %w", err)
	}

	initReq := domain.InitAgentRequest{
		AgentID:     agentID,
		AgentPort:   agentPort,
		Address:     address,
		Fingerprint: fingerprint,
		PID:         os.Getpid(),
		Version:     config.Version,
	}

	a.logger.Info("initializing agent with API", "agent_id", agentID, "port", agentPort)

	initResp, err := a.api.InitAgent(ctx, initReq)
	if err != nil {
		return nil, fmt.Errorf("init agent: %w", err)
	}

	secretKey := initResp.SecretKey
	if secretKey != "" {
		if err := a.store.SaveSecret(secretKey); err != nil {
			a.logger.Warn("failed to save secret", "err", err)
		}
		a.api.UseSecret(secretKey)
	} else {
		secretKey, _ = a.store.Secret()
		if secretKey != "" {
			a.api.UseSecret(secretKey)
		}
	}

	if initResp.FRP != nil {
		if err := a.store.SaveFRPInfo(initResp.FRP); err != nil {
			a.logger.Warn("failed to save FRP info", "err", err)
		}
	} else {
		initResp.FRP, _ = a.store.FRPInfo()
	}

	if !initResp.HostExists {
		hostReq := a.probe.HostRegistration()
		a.logger.Info("registering host", "gpu", hostReq.GPUName, "gpu_count", hostReq.GPUAmount, "vram", hostReq.VRAM)
		if err := a.api.RegisterHost(ctx, hostReq); err != nil {
			return nil, fmt.Errorf("register host: %w", err)
		}
	}

	if err := a.store.SaveAPIKey(a.cfg.APIKey); err != nil {
		a.logger.Warn("failed to save api key", "err", err)
	}

	return &domain.AgentMetadata{
		ID:          agentID,
		Port:        agentPort,
		Address:     address,
		Fingerprint: fingerprint,
		SecretKey:   secretKey,
		FRP:         initResp.FRP,
	}, nil
}

func (a *Agent) restoreState(ctx context.Context) error {
	state, err := a.store.LoadInstanceState()
	if err != nil {
		return fmt.Errorf("load instance state: %w", err)
	}
	if state == nil {
		return nil
	}

	a.logger.Info("restoring instance state", "vm_id", state.ContainerID, "ports", state.Ports)

	a.vm.RestoreState(state)

	status := a.vm.Status(ctx)
	if status == domain.StatusDestroyed || status == domain.StatusError {
		a.logger.Warn("saved instance is not running, clearing state")
		a.vm.RestoreState(nil)
		_ = a.store.ClearInstanceState()
		return nil
	}

	if len(state.FRPProxies) > 0 {
		if err := a.frpcProc.UpdateInstanceProxies(state.FRPProxies); err != nil {
			a.logger.Warn("failed to restore frpc proxies", "err", err)
		}
	}

	return nil
}

func (a *Agent) publishStats(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	count := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap := a.stats.Collect()
			status := a.vm.Status(ctx)

			report := domain.StatsReport{
				StatsSnapshot: snap,
				Status:        status,
			}

			if err := a.api.SendStats(ctx, report); err != nil {
				if count%40 == 0 {
					a.logger.Warn("failed to send stats", "err", err)
				}
			}

			if count%20 == 0 && status == domain.StatusRunning {
				a.logger.Info("stats",
					"gpu_util", snap.GPUUtil,
					"gpu_temp", snap.GPUTemp,
					"cpu_util", fmt.Sprintf("%.1f%%", snap.CPUUtil),
					"ram_util", fmt.Sprintf("%.1f%%", snap.RAMUtil),
				)
			}
			count++
		}
	}
}

func (a *Agent) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if a.httpServer != nil {
		if err := a.httpServer.Shutdown(ctx); err != nil {
			a.logger.Error("http server shutdown error", "err", err)
		}
	}

	if err := a.frpcProc.Stop(); err != nil {
		a.logger.Error("frpc stop error", "err", err)
	}

	a.logger.Info("agent stopped")
	return nil
}

func (a *Agent) subdomain() string {
	if a.meta != nil && a.meta.FRP != nil {
		return a.meta.FRP.Subdomain
	}
	return ""
}
