package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/magicaleks/qudata-agent-alpha/internal/infra/paths"
)

var instanceStatePath = paths.Resolve("/var/lib/gpu-agent/instance_state.json", filepath.Join("state", "instance_state.json"))

// InstanceState описывает состояние активного инстанса, требующее восстановления.
type InstanceState struct {
	ContainerID string            `json:"container_id"`
	Ports       map[string]string `json:"ports,omitempty"` // container port -> external port
	TunnelToken string            `json:"tunnel_token,omitempty"`
}

func ensureDir() error {
	return os.MkdirAll(filepath.Dir(instanceStatePath), 0o755)
}

// LoadInstanceState читает состояние инстанса.
func LoadInstanceState() (*InstanceState, error) {
	data, err := os.ReadFile(instanceStatePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state InstanceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// SaveInstanceState сохраняет состояние инстанса.
func SaveInstanceState(state *InstanceState) error {
	if state == nil {
		return ClearInstanceState()
	}
	if err := ensureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(instanceStatePath, data, 0o644)
}

// ClearInstanceState удаляет сохранённое состояние.
func ClearInstanceState() error {
	if err := os.Remove(instanceStatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
