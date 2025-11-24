package instance

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/magicaleks/qudata-agent-alpha/internal/domain"
	"github.com/magicaleks/qudata-agent-alpha/internal/impls"
)

type Service struct {
	ctx     context.Context
	repo    impls.InstanceRepository
	env     impls.EnvironmentProbe
	ports   impls.PortAllocator
	tunnels impls.TunnelController
	logger  impls.Logger
}

func NewService(ctx context.Context, repo impls.InstanceRepository, env impls.EnvironmentProbe, allocator impls.PortAllocator, tunnels impls.TunnelController, logger impls.Logger) *Service {
	return &Service{
		ctx:     ctx,
		repo:    repo,
		env:     env,
		ports:   allocator,
		tunnels: tunnels,
		logger:  logger,
	}
}

type CreateInput struct {
	Image       string
	ImageTag    string
	StorageGB   int
	Registry    string
	Login       string
	Password    string
	EnvVars     map[string]string
	Ports       []string
	Command     string
	SSHEnabled  bool
	TunnelToken string
}

func (s *Service) Status(ctx context.Context) domain.InstanceStatus {
	return s.repo.Status(ctx)
}

func (s *Service) Create(ctx context.Context, input CreateInput) (domain.InstancePorts, error) {
	if input.TunnelToken == "" {
		return nil, fmt.Errorf("tunnel token is required")
	}

	image := strings.TrimSpace(input.Image)
	if image == "" {
		image = "ubuntu:22.04"
	} else if input.ImageTag != "" && !strings.Contains(image, ":") {
		image += ":" + input.ImageTag
	}

	allocatedPorts := make(domain.InstancePorts)
	if len(input.Ports) > 0 {
		ports, err := s.ports.Allocate(len(input.Ports))
		if err != nil {
			return nil, err
		}
		if len(ports) < len(input.Ports) {
			return nil, fmt.Errorf("requested %d ports, allocated %d", len(input.Ports), len(ports))
		}
		for idx, containerPort := range input.Ports {
			allocatedPorts[containerPort] = strconv.Itoa(ports[idx])
		}
	}

	conf := s.env.Configuration()
	requestedDisk := float64(input.StorageGB) * 1024
	maxDisk := conf.Disk.Amount * 1024
	volumeSize := int64(math.Min(requestedDisk, maxDisk))

	spec := domain.InstanceSpec{
		Image:       image,
		VolumeSize:  volumeSize,
		Registry:    input.Registry,
		Login:       input.Login,
		Password:    input.Password,
		EnvVars:     input.EnvVars,
		Ports:       allocatedPorts,
		Command:     input.Command,
		SSHEnabled:  input.SSHEnabled,
		TunnelToken: input.TunnelToken,
	}

	containerID, err := s.repo.Create(ctx, spec)
	if err != nil {
		return nil, err
	}

	if err := s.tunnels.Configure(s.ctx, containerID, input.TunnelToken, allocatedPorts); err != nil {
		_ = s.repo.Stop(ctx)
		return nil, err
	}

	s.logger.Info("Instance creation requested for image %s", image)
	return allocatedPorts, nil
}

func (s *Service) Manage(ctx context.Context, cmd domain.InstanceCommand) error {
	return s.repo.Manage(ctx, cmd)
}

func (s *Service) Delete(ctx context.Context) error {
	if err := s.repo.Stop(ctx); err != nil {
		return err
	}
	return s.tunnels.Clear()
}

func (s *Service) AddSSH(ctx context.Context, key string) error {
	return s.repo.AddSSH(ctx, key)
}

func (s *Service) RemoveSSH(ctx context.Context, key string) error {
	return s.repo.RemoveSSH(ctx, key)
}
