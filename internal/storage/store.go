package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/qudata/agent/internal/domain"
)

// Store provides persistent file-based storage for agent state.
type Store struct {
	dataDir string
	mu      sync.RWMutex
}

// NewStore creates a Store rooted at dataDir, ensuring the directory exists.
func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", dataDir, err)
	}
	return &Store{dataDir: dataDir}, nil
}

// AgentID returns the persisted agent ID, generating one if it doesn't exist.
func (s *Store) AgentID() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dataDir, "agent_id")
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}

	id := uuid.New().String()
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return "", fmt.Errorf("write agent id: %w", err)
	}
	return id, nil
}

// SaveAPIKey persists the API key to disk.
func (s *Store) SaveAPIKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.WriteFile(filepath.Join(s.dataDir, "api_key"), []byte(key), 0o600)
}

// APIKey reads the persisted API key.
func (s *Store) APIKey() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(s.dataDir, "api_key"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// SaveSecret persists the agent secret key received from the API.
func (s *Store) SaveSecret(secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.WriteFile(filepath.Join(s.dataDir, "agent_secret"), []byte(secret), 0o600)
}

// Secret reads the persisted agent secret key.
func (s *Store) Secret() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(s.dataDir, "agent_secret"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// SaveInstanceState persists the running instance state.
func (s *Store) SaveInstanceState(state *domain.InstanceState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal instance state: %w", err)
	}
	return os.WriteFile(filepath.Join(s.dataDir, "instance_state.json"), data, 0o600)
}

// LoadInstanceState loads the persisted instance state, or nil if none exists.
func (s *Store) LoadInstanceState() (*domain.InstanceState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(s.dataDir, "instance_state.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var state domain.InstanceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal instance state: %w", err)
	}
	return &state, nil
}

// ClearInstanceState removes the persisted instance state.
func (s *Store) ClearInstanceState() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dataDir, "instance_state.json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
