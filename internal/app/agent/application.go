package agentapp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/magicaleks/qudata-agent-alpha/internal/adapter/httpserver"
	appversion "github.com/magicaleks/qudata-agent-alpha/internal/app/version"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/docker"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/logger"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/network"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/qudata"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/state"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/storage"
	systeminfra "github.com/magicaleks/qudata-agent-alpha/internal/infra/system"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/tunnel"
	agentuc "github.com/magicaleks/qudata-agent-alpha/internal/usecase/agent"
	instanceuc "github.com/magicaleks/qudata-agent-alpha/internal/usecase/instance"
	"github.com/magicaleks/qudata-agent-alpha/internal/usecase/maintenance"
	statsuc "github.com/magicaleks/qudata-agent-alpha/internal/usecase/stats"
)

const (
	AgentVersion = "a0.0.2"
)

type Application struct {
	agentSvc *agentuc.Service
	stats    *statsuc.Publisher
	api      *httpserver.API
	logger   *logger.FileLogger
	store    *storage.FilesystemAgentStore
	tunnels  *tunnel.Manager
	docker   *docker.Manager
}

func NewApplication(ctx context.Context) (*Application, error) {
	log := logger.NewFileLogger()
	allocator := network.NewAllocator(log)
	allocator.Configure(os.Getenv("QUDATA_PORTS"))

	env := systeminfra.NewProbe(allocator)
	statsCollector := systeminfra.NewStatsCollector()
	store := storage.NewFilesystemAgentStore()
	apiKey := strings.TrimSpace(os.Getenv("QUDATA_API_KEY"))
	if apiKey != "" {
		if err := store.SaveAPIKey(ctx, apiKey); err != nil {
			log.Warn("failed to store api key: %v", err)
		}
	}

	client := qudata.NewClient(apiKey)
	secret, err := store.Secret(ctx)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		client.UseSecret(secret)
	}

	dockerManager := docker.NewManager()
	savedState, _ := state.LoadInstanceState()
	dockerManager.RestoreState(savedState)

	tunnelManager := tunnel.NewManager(log)

	instanceSvc := instanceuc.NewService(ctx, dockerManager, env, allocator, tunnelManager, log)
	agentSvc := agentuc.NewService(store, env, client, dockerManager, appversion.AgentVersion, log)
	statsPublisher := statsuc.NewPublisher(statsCollector, client, dockerManager, log, 500*time.Millisecond)
	updater := maintenance.NewUpdater(store, log)

	api := httpserver.NewAPI(instanceSvc, updater, log)

	return &Application{
		agentSvc: agentSvc,
		stats:    statsPublisher,
		api:      api,
		logger:   log,
		store:    store,
		tunnels:  tunnelManager,
		docker:   dockerManager,
	}, nil
}

func (a *Application) Run(ctx context.Context) error {
	meta, instanceRunning, err := a.agentSvc.Bootstrap(ctx)
	if err != nil {
		return err
	}

	if instanceRunning {
		if err := a.tunnels.Restore(ctx); err != nil {
			return err
		}
	} else {
		a.docker.RestoreState(nil)
		if err := a.tunnels.Clear(); err != nil {
			return err
		}
	}

	a.stats.Start(ctx)
	secret, err := a.store.Secret(ctx)
	if err != nil {
		return err
	}
	if secret == "" {
		secret = "agent_secret"
	}

	server := httpserver.NewServer(meta.Port, a.api, secret, a.logger)
	a.logger.Info("server starting on %s", fmt.Sprintf("0.0.0.0:%d", meta.Port))

	return server.Run()
}
