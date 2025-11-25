package agent

import (
	"context"
	"errors"
	"os"

	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
	"github.com/magicaleks/qudata-agent-alpha/internal/impls"
)

type Service struct {
	store     impls.AgentStore
	env       impls.EnvironmentProbe
	api       impls.AgentService
	instances impls.InstanceRepository
	logger    impls.Logger
	version   string
}

func NewService(store impls.AgentStore, env impls.EnvironmentProbe, api impls.AgentService, instances impls.InstanceRepository, version string, logger impls.Logger) *Service {
	return &Service{
		store:     store,
		env:       env,
		api:       api,
		instances: instances,
		logger:    logger,
		version:   version,
	}
}

func (s *Service) Bootstrap(ctx context.Context) (*domain.AgentMetadata, bool, error) {
	agentID, err := s.store.AgentID(ctx)
	if err != nil {
		return nil, false, err
	}

	storedSecret, err := s.store.Secret(ctx)
	if err != nil {
		return nil, false, err
	}

	port, err := s.env.AgentPort()
	if err != nil {
		return nil, false, err
	}

	metadata := &domain.AgentMetadata{
		ID:          agentID,
		Port:        port,
		Address:     s.env.PublicIP(),
		Fingerprint: s.env.Fingerprint(),
		PID:         os.Getpid(),
	}

	if err := s.api.Ping(ctx); err != nil {
		return nil, false, err
	}

	initResp, err := s.api.InitAgent(ctx, domain.InitAgentRequest{
		AgentID:     metadata.ID,
		AgentPort:   metadata.Port,
		Address:     metadata.Address,
		Fingerprint: metadata.Fingerprint,
		PID:         metadata.PID,
		Version:     s.version,
	})
	if err != nil {
		return nil, false, err
	}
	if initResp == nil {
		return nil, false, errors.New("empty init response")
	}

	switch {
	case initResp.SecretKey != "":
		if err := s.store.SaveSecret(ctx, initResp.SecretKey); err != nil {
			return nil, false, err
		}
		s.api.UseSecret(initResp.SecretKey)
	case storedSecret != "":
		s.api.UseSecret(storedSecret)
	}

	if !initResp.InstanceRunning {
		s.logger.Info("No instance running, cleaning up docker resources")
		if err := s.instances.Cleanup(ctx); err != nil {
			s.logger.Warn("cleanup error: %v", err)
		}
	}

	if !initResp.HostExists {
		hostReq := domain.CreateHostRequest{
			GPUName:       s.env.GPUName(),
			GPUAmount:     s.env.GPUCount(),
			VRAM:          s.env.VRAM(),
			MaxCUDA:       s.env.MaxCUDA(),
			Configuration: s.env.Configuration(),
		}

		s.logger.Info("Creating host %s (CUDA %.1f)", hostReq.GPUName, hostReq.MaxCUDA)
		if err := s.api.RegisterHost(ctx, hostReq); err != nil {
			return nil, false, err
		}
	}

	return metadata, initResp.InstanceRunning, nil
}
