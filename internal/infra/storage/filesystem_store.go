package storage

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/logger"
	"github.com/magicaleks/qudata-agent-alpha/internal/infra/paths"
)

const (
	defaultAgentIDPath     = "/var/lib/gpu-agent/agent_id"
	defaultAgentAPIKeyPath = "/var/lib/gpu-agent/api_key"
	defaultAgentSecretPath = "/run/lib/gpu-agent/agent_secret"
)

// FilesystemAgentStore хранит данные агента в файловой системе.
type FilesystemAgentStore struct {
	agentIDPath string
	apiKeyPath  string
	secretPath  string
}

func NewFilesystemAgentStore() *FilesystemAgentStore {
	return &FilesystemAgentStore{
		agentIDPath: paths.Resolve(defaultAgentIDPath, filepath.Join("state", "agent_id")),
		apiKeyPath:  paths.Resolve(defaultAgentAPIKeyPath, filepath.Join("state", "api_key")),
		secretPath:  paths.Resolve(defaultAgentSecretPath, filepath.Join("run", "agent_secret")),
	}
}

func (s *FilesystemAgentStore) AgentID(_ context.Context) (string, error) {
	file, err := os.OpenFile(s.agentIDPath, os.O_RDONLY, 0o666)
	if err == nil {
		defer file.Close()
		buf := bufio.NewReader(file)
		stored, _ := buf.ReadBytes('\n')
		if storedID, err := uuid.FromBytes(stored); err == nil {
			return storedID.String(), nil
		}
	}

	newID := uuid.New()
	bytes, err := newID.MarshalBinary()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(s.agentIDPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(s.agentIDPath, bytes, 0o600); err != nil {
		logger.LogError("failed to persist agent id: %v", err)
		return "", err
	}
	return newID.String(), nil
}

func (s *FilesystemAgentStore) Secret(_ context.Context) (string, error) {
	file, err := os.OpenFile(s.secretPath, os.O_RDONLY, 0o600)
	if err != nil {
		return "", nil
	}
	defer file.Close()

	buf := bufio.NewReader(file)
	secret, _ := buf.ReadString('\n')
	if !strings.HasPrefix(secret, "sk-") {
		_ = os.Remove(s.secretPath)
		return "", nil
	}
	return strings.TrimSpace(secret), nil
}

func (s *FilesystemAgentStore) SaveSecret(_ context.Context, secret string) error {
	if err := os.MkdirAll(filepath.Dir(s.secretPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.secretPath, []byte(secret), 0o600); err != nil {
		logger.LogError("failed to persist secret: %v", err)
		return err
	}
	return nil
}

func (s *FilesystemAgentStore) APIKey(_ context.Context) (string, error) {
	data, err := os.ReadFile(s.apiKeyPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *FilesystemAgentStore) SaveAPIKey(_ context.Context, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.apiKeyPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.apiKeyPath, []byte(apiKey+"\n"), 0o600); err != nil {
		logger.LogError("failed to persist api key: %v", err)
		return err
	}
	return nil
}
