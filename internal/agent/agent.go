package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
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

	store    *storage.Store
	api      *qudata.Client
	mgr      *qemu.Manager
	frpcProc *frpc.Process
	ports    *network.PortAllocator
	stats    *system.StatsCollector

	httpServer *server.Server
	meta       *domain.AgentMetadata
}

func New(cfg *config.Config, logger *slog.Logger) (*Agent, error) {
	store, err := storage.NewStore(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("init storage: %w", err)
	}

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
		QEMUBinary:    cfg.QEMUBinary,
		OVMFCodePath:  cfg.OVMFCodePath,
		OVMFVarsPath:  cfg.OVMFVarsPath,
		BaseImagePath: cfg.BaseImagePath,
		ImageDir:      cfg.ImageDir,
		RunDir:        cfg.VMRunDir,
		DefaultGPU:    cfg.GPUPCIAddr,
		SSHKeyPath:    sshKeyPath,
		DefaultCPUs:   cfg.VMDefaultCPUs,
		DefaultMemory: cfg.VMDefaultMemory,
		DiskSizeGB:    cfg.VMDiskSizeGB,
	}, logger)

	api := qudata.NewClient(cfg.APIKey, cfg.ServiceURL, logger)
	frpcProc := frpc.NewProcess(cfg.FRPCBinary, cfg.FRPCConfigPath, logger)
	portAlloc := network.NewPortAllocator()

	return &Agent{
		cfg:      cfg,
		logger:   logger,
		store:    store,
		api:      api,
		mgr:      mgr,
		frpcProc: frpcProc,
		ports:    portAlloc,
	}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	a.mgr.KillOrphans()

	meta, err := a.bootstrap(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	a.meta = meta

	if meta.SecretDomain == "" {
		return fmt.Errorf("secret_domain not received from API — cannot start FRPC tunnel")
	}

	if err := a.frpcProc.Start(meta.ID, meta.SecretDomain, meta.Port); err != nil {
		return fmt.Errorf("start frpc: %w", err)
	}
	a.logger.Info("frpc tunnel established",
		"subdomain", meta.SecretDomain,
		"domain", meta.SecretDomain+frpc.DomainSuffix,
	)

	_ = a.store.ClearInstanceState()

	if !meta.HostExists {
		var gpuProvider domain.GPUInfoProvider
		if a.cfg.Debug {
			gpuProvider = gpu.MockInfoProvider{}
		} else {
			gpuInfoPath := a.cfg.DataDir + "/gpu-info.json"
			gpuProvider = &gpu.FileInfoProvider{Path: gpuInfoPath}
		}
		probe := system.NewProbe(gpuProvider)
		hostReq := probe.HostRegistration(ctx)
		a.logger.Info("registering host",
			"gpu", hostReq.GPUName,
			"gpu_count", hostReq.GPUAmount,
			"vram", hostReq.VRAM,
			"max_cuda", hostReq.MaxCUDA,
		)
		if err := a.api.RegisterHost(ctx, hostReq); err != nil {
			return fmt.Errorf("register host: %w", err)
		}
		a.logger.Info("host registered successfully")
	}

	a.stats = system.NewStatsCollector(a.mgr)
	go a.publishStats(ctx)

	a.httpServer = server.New(
		meta.Port,
		meta.SecretKey,
		meta.Subdomain(),
		a.mgr,
		a.frpcProc,
		a.ports,
		a.store,
		a.logger,
	)

	a.logger.Info("agent ready",
		"version", config.Version,
		"agent_id", meta.ID,
		"port", meta.Port,
	)

	errCh := make(chan error, 1)
	go func() { errCh <- a.httpServer.Start() }()

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
	fingerprint := machineFingerprint()

	a.logger.Info("pinging API", "url", a.cfg.ServiceURL)
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

	a.logger.Info("initializing agent",
		"agent_id", agentID,
		"port", agentPort,
		"address", address,
		"fingerprint", fingerprint,
		"version", config.Version,
	)

	initResp, err := a.api.InitAgent(ctx, initReq)
	if err != nil {
		return nil, fmt.Errorf("init agent: %w", err)
	}

	a.logger.Info("init response",
		"host_exists", initResp.HostExists,
		"has_secret", initResp.SecretKey != "",
		"secret_domain", initResp.SecretDomain,
	)

	secretKey := initResp.SecretKey
	if secretKey != "" {
		_ = a.store.SaveSecret(secretKey)
		a.api.UseSecret(secretKey)
	} else {
		secretKey, _ = a.store.Secret()
		if secretKey != "" {
			a.api.UseSecret(secretKey)
		}
	}

	secretDomain := initResp.SecretDomain
	if secretDomain != "" {
		_ = a.store.SaveSecretDomain(secretDomain)
	} else {
		secretDomain, _ = a.store.SecretDomain()
	}

	_ = a.store.SaveAPIKey(a.cfg.APIKey)

	return &domain.AgentMetadata{
		ID:           agentID,
		Port:         agentPort,
		Address:      address,
		SecretKey:    secretKey,
		SecretDomain: secretDomain,
		HostExists:   initResp.HostExists,
	}, nil
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
			report := a.stats.Collect(ctx)
			if err := a.api.SendStats(ctx, report); err != nil {
				if count%40 == 0 {
					a.logger.Warn("failed to send stats", "err", err)
				}
			}
			count++
		}
	}
}

func (a *Agent) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if a.httpServer != nil {
		if err := a.httpServer.Shutdown(ctx); err != nil {
			a.logger.Error("http server shutdown error", "err", err)
		}
	}

	if err := a.frpcProc.Stop(); err != nil {
		a.logger.Error("frpc stop error", "err", err)
	}

	if !a.cfg.Debug {
		if err := a.mgr.Stop(ctx); err != nil {
			a.logger.Error("VM stop error", "err", err)
		}
	}

	a.logger.Info("agent stopped")
	return nil
}

func machineFingerprint() string {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}
